// Package pipeline orchestrates the full reup pipeline locally on the user's
// machine. It is the desktop port of the server's worker/process_job.go: instead
// of running on a VPS across three Asynq tasks, it runs the three stages
// sequentially in-process and calls the third-party APIs directly with the
// user's own keys (Hướng A).
package pipeline

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sync"

	"github.com/sirupsen/logrus"

	"github.com/thq-solution/auto-reup-studio-desktop/engine/ipc"
	"github.com/thq-solution/auto-reup-studio-desktop/engine/ocr"
	"github.com/thq-solution/auto-reup-studio-desktop/engine/services/facebook"
	"github.com/thq-solution/auto-reup-studio-desktop/engine/services/ffmpeg"
	"github.com/thq-solution/auto-reup-studio-desktop/engine/services/openai"
	"github.com/thq-solution/auto-reup-studio-desktop/engine/services/srtvoice"
	"github.com/thq-solution/auto-reup-studio-desktop/engine/services/thq"
	"github.com/thq-solution/auto-reup-studio-desktop/engine/store"
)

// Deps are the orchestrator's external dependencies.
type Deps struct {
	Logger      *logrus.Logger
	Store       *store.Store
	OCRURL      string // local OCR sidecar base URL, e.g. http://127.0.0.1:8000
	FFmpegPath  string // path to bundled ffmpeg ("" → "ffmpeg" on PATH)
	FFprobePath string // path to bundled ffprobe ("" → "ffprobe" on PATH)
	FontsDir    string // dir with bundled fonts for drawtext overlays
	DataDir     string // base dir for per-job temp files
}

// Keys holds the user's third-party API keys. Supplied per-job by the shell,
// which reads them from the OS keychain — they are never persisted by the engine.
type Keys struct {
	OpenAI        string `json:"openai"`
	THQ           string `json:"thq"`
	SRTVoice      string `json:"srt_voice"`
	FacebookToken string `json:"facebook_token"`
}

// Settings carries the FINAL resolved render/voice options for a job. The
// frontend merges the backend's non-secret defaults (GET /api/settings) with the
// per-job choices and sends the result here. Field names mirror the backend
// settings response so the merge is a straight copy.
type Settings struct {
	TargetLanguage string `json:"target_language"`

	// Voice / TTS
	Voice           string `json:"voice"`
	VoiceProvider   string `json:"voice_provider"`
	SpeedAdjustment int    `json:"speed_adjustment"`

	// Subtitle style
	SubtitleFont        string  `json:"subtitle_font"`
	SubtitleSize        int     `json:"subtitle_size"`
	SubtitleColor       string  `json:"subtitle_color"`
	SubtitleStrokeColor string  `json:"subtitle_stroke_color"`
	SubtitleStrokeWidth float64 `json:"subtitle_stroke_width"`
	SubtitleScaleX      int     `json:"subtitle_scale_x"`

	// Cover (hides the original burned-in subtitle). Cover colour reuses the
	// subtitle stroke colour, matching the server's behaviour.
	CoverOpacity float64 `json:"cover_opacity"`

	// Video transforms
	FlipVideo           bool    `json:"flip_video"`
	VideoZoom           float64 `json:"video_zoom"`
	OriginalAudioVolume float64 `json:"original_audio_volume"`

	// Brand watermark
	BrandEnabled   bool    `json:"brand_enabled"`
	BrandType      string  `json:"brand_type"`
	BrandText      string  `json:"brand_text"`
	BrandTextColor string  `json:"brand_text_color"`
	BrandFont      string  `json:"brand_font"`
	BrandFontSize  int     `json:"brand_font_size"`
	BrandPosition  string  `json:"brand_position"`
	BrandOpacity   float64 `json:"brand_opacity"`
	BrandImageURL  string  `json:"brand_image_url"`

	// Content / hook
	AutoGenerateContent bool    `json:"auto_generate_content"`
	HookEnabled         bool    `json:"hook_enabled"`
	HookDuration        float64 `json:"hook_duration"`

	// Pipeline toggles (pointer so an omitted value defaults to ON, matching the
	// backend default of true). Subtitle off → no burn/cover; voice off → keep
	// original audio (no srt-to-voice).
	EnableSubtitle *bool `json:"enable_subtitle"`
	EnableVoice    *bool `json:"enable_voice"`

	// Posting
	AutoPostToFacebook bool   `json:"auto_post_to_facebook"`
	FacebookPageID     string `json:"facebook_page_id"`

	// Flow
	ManualMode bool `json:"manual_mode"` // pause for subtitle + content confirmation
}

// subtitleEnabled / voiceEnabled default to true when the flag is omitted.
func (s *Settings) subtitleEnabled() bool { return s.EnableSubtitle == nil || *s.EnableSubtitle }
func (s *Settings) voiceEnabled() bool    { return s.EnableVoice == nil || *s.EnableVoice }

// normalize fills in safe defaults so a sparse settings blob still renders.
func (s *Settings) normalize() {
	if s.TargetLanguage == "" {
		s.TargetLanguage = "Vietnamese"
	}
	if s.Voice == "" {
		s.Voice = "vi-VN-HoaiMyNeural"
	}
	if s.VoiceProvider == "" {
		s.VoiceProvider = "edge-tts"
	}
	if s.SubtitleSize == 0 {
		s.SubtitleSize = 28
	}
	if s.SubtitleColor == "" {
		s.SubtitleColor = "FFFFFF"
	}
	if s.SubtitleStrokeColor == "" {
		s.SubtitleStrokeColor = "000000"
	}
	if s.SubtitleScaleX == 0 {
		s.SubtitleScaleX = 100
	}
	if s.VideoZoom == 0 {
		s.VideoZoom = 1.0
	}
}

// StartJobParams is the payload of a start_job command.
type StartJobParams struct {
	JobID     string   `json:"job_id"` // client-generated id, also the temp dir name
	SourceURL string   `json:"source_url"`
	Keys      Keys     `json:"keys"`
	Settings  Settings `json:"settings"`
}

// jobState threads artifacts between the three stages.
type jobState struct {
	params                                               StartJobParams
	jobDir                                               string
	videoPath, audioPath, srtPath, voicePath, outputPath string

	videoInfo *thq.VideoInfo
	vw, vh    int

	region       *ocr.SubtitleRegion
	previewFrame string // path to the preview frame for manual confirm

	translatedSRT string
	aiContent     *openai.AIContent
	hookText      string
}

// jobHandle tracks a running job so it can be cancelled or resumed (manual mode).
type jobHandle struct {
	cancel         context.CancelFunc
	confirmSub     chan json.RawMessage
	confirmContent chan json.RawMessage
}

// Orchestrator runs jobs and owns the service clients.
type Orchestrator struct {
	deps Deps

	thq      *thq.Client
	srtvoice *srtvoice.Client
	openai   *openai.Client
	facebook *facebook.Client
	ffmpeg   *ffmpeg.Service
	ocr      *ocr.Service

	mu   sync.Mutex
	jobs map[string]*jobHandle
}

// NewOrchestrator constructs the orchestrator and its (stateless) service clients.
func NewOrchestrator(d Deps) *Orchestrator {
	return &Orchestrator{
		deps:     d,
		thq:      thq.NewClient(d.Logger, ""),
		srtvoice: srtvoice.NewClient(d.Logger, ""),
		openai:   openai.NewClient(d.Logger),
		facebook: facebook.NewClient(d.Logger),
		ffmpeg:   ffmpeg.NewService(d.Logger, d.FFmpegPath, d.FFprobePath, d.FontsDir),
		ocr:      ocr.NewService(d.Logger, d.OCRURL),
		jobs:     make(map[string]*jobHandle),
	}
}

// StartJob runs the full pipeline for one job, emitting progress events.
func (o *Orchestrator) StartJob(parent context.Context, cmdID string, raw json.RawMessage, emit ipc.EmitFunc) {
	var p StartJobParams
	if err := json.Unmarshal(raw, &p); err != nil {
		emit(ipc.Event{ID: cmdID, Type: ipc.EvtFailed, Payload: errPayload("download", "invalid start_job payload", err)})
		return
	}
	p.Settings.normalize()

	ctx, cancel := context.WithCancel(parent)
	h := &jobHandle{
		cancel:         cancel,
		confirmSub:     make(chan json.RawMessage, 1),
		confirmContent: make(chan json.RawMessage, 1),
	}
	o.register(cmdID, h)
	defer o.unregister(cmdID)

	st := &jobState{
		params: p,
		jobDir: filepath.Join(o.deps.DataDir, "jobs", p.JobID),
		vw:     1080, vh: 1920,
	}
	st.videoPath = filepath.Join(st.jobDir, "video.mp4")
	st.audioPath = filepath.Join(st.jobDir, "audio.mp3")
	st.srtPath = filepath.Join(st.jobDir, "subtitle.srt")
	st.voicePath = filepath.Join(st.jobDir, "voice.mp3")
	st.outputPath = filepath.Join(st.jobDir, "output.mp4")

	pr := &progress{emit: emit, cmdID: cmdID}
	_ = o.deps.Store.CreateJob(p.JobID, p.SourceURL)
	o.update(p.JobID, "processing", "download", 5)

	// ── Stage 1: download → normalize → OCR ──────────────────────────────────
	if err := o.stage1(ctx, pr, st); err != nil {
		o.fail(emit, cmdID, p.JobID, "detect_subtitle", err)
		return
	}
	if p.Settings.ManualMode {
		if err := o.waitSubtitleConfirm(ctx, h, pr, st); err != nil {
			o.fail(emit, cmdID, p.JobID, "detect_subtitle", err)
			return
		}
	}

	// ── Stage 2: audio → transcribe → translate → content ────────────────────
	if err := o.stage2(ctx, pr, st); err != nil {
		o.fail(emit, cmdID, p.JobID, "generate_ai_content", err)
		return
	}
	if p.Settings.ManualMode {
		if err := o.waitContentConfirm(ctx, h, pr, st); err != nil {
			o.fail(emit, cmdID, p.JobID, "generate_ai_content", err)
			return
		}
	}

	// ── Stage 3: voice → render → post ───────────────────────────────────────
	if err := o.stage3(ctx, pr, st); err != nil {
		o.fail(emit, cmdID, p.JobID, "render", err)
		return
	}

	title := st.params.SourceURL
	if st.aiContent != nil && st.aiContent.Title != "" {
		title = st.aiContent.Title
	} else if st.videoInfo != nil && st.videoInfo.Title != "" {
		title = st.videoInfo.Title
	}
	_ = o.deps.Store.Complete(p.JobID, title, st.outputPath)
	emit(ipc.Event{ID: cmdID, Type: ipc.EvtCompleted, Payload: map[string]interface{}{
		"percent":     100,
		"title":       title,
		"output_path": st.outputPath,
		"ai_content":  st.aiContent, // title, caption, hashtags
		"hook_text":   st.hookText,
	}})
}

// waitSubtitleConfirm emits the detected region + preview frame and blocks until
// the user confirms (manual mode). The confirm payload may override the region.
func (o *Orchestrator) waitSubtitleConfirm(ctx context.Context, h *jobHandle, pr *progress, st *jobState) error {
	o.update(st.params.JobID, "waiting_subtitle", "detect_subtitle", 25)
	pr.emitRaw(ipc.EvtWaitingSubtitle, map[string]interface{}{
		"video_width":   st.vw,
		"video_height":  st.vh,
		"preview_frame": st.previewFrame,
		"region":        st.region,
	})
	select {
	case <-ctx.Done():
		return ctx.Err()
	case payload := <-h.confirmSub:
		var r ocr.SubtitleRegion
		if err := json.Unmarshal(payload, &r); err == nil && r.Width > 0 && r.Height > 0 {
			st.region = &r
		}
		return nil
	}
}

// waitContentConfirm emits the AI content + SRT and blocks until the user
// confirms (manual mode). The confirm payload may override SRT/content/hook.
func (o *Orchestrator) waitContentConfirm(ctx context.Context, h *jobHandle, pr *progress, st *jobState) error {
	o.update(st.params.JobID, "waiting_content", "generate_ai_content", 68)
	pr.emitRaw(ipc.EvtWaitingContent, map[string]interface{}{
		"translated_srt": st.translatedSRT,
		"ai_content":     st.aiContent,
		"hook_text":      st.hookText,
	})
	select {
	case <-ctx.Done():
		return ctx.Err()
	case payload := <-h.confirmContent:
		var edit struct {
			TranslatedSRT string            `json:"translated_srt"`
			AIContent     *openai.AIContent `json:"ai_content"`
			HookText      *string           `json:"hook_text"`
		}
		if err := json.Unmarshal(payload, &edit); err == nil {
			if edit.TranslatedSRT != "" {
				st.translatedSRT = edit.TranslatedSRT
			}
			if edit.AIContent != nil {
				st.aiContent = edit.AIContent
			}
			if edit.HookText != nil {
				st.hookText = *edit.HookText
			}
		}
		return nil
	}
}

// ListJobs returns the local job history (newest first) as a result event.
func (o *Orchestrator) ListJobs(cmdID string, emit ipc.EmitFunc) {
	jobs, err := o.deps.Store.ListJobs(100)
	if err != nil {
		emit(ipc.Event{ID: cmdID, Type: ipc.EvtError, Payload: map[string]string{"error": err.Error()}})
		return
	}
	emit(ipc.Event{ID: cmdID, Type: ipc.EvtResult, Payload: map[string]interface{}{"jobs": jobs}})
}

// Cancel aborts a running job.
func (o *Orchestrator) Cancel(cmdID string, _ json.RawMessage) {
	o.mu.Lock()
	h := o.jobs[cmdID]
	o.mu.Unlock()
	if h != nil {
		h.cancel()
	}
}

// Delete cancels a running job if needed, then removes its local history and artifacts.
func (o *Orchestrator) Delete(cmdID string, _ json.RawMessage, emit ipc.EmitFunc) {
	o.Cancel(cmdID, nil)

	jobDir := filepath.Join(o.deps.DataDir, "jobs", cmdID)
	_ = os.RemoveAll(jobDir)
	if err := o.deps.Store.DeleteJob(cmdID); err != nil {
		emit(ipc.Event{ID: cmdID, Type: ipc.EvtError, Payload: map[string]string{"error": err.Error()}})
		return
	}

	emit(ipc.Event{ID: cmdID, Type: ipc.EvtResult, Payload: map[string]interface{}{
		"deleted": true,
		"job_id":  cmdID,
	}})
}

// ConfirmSubtitle delivers the user-approved subtitle area to a paused job.
func (o *Orchestrator) ConfirmSubtitle(cmdID string, payload json.RawMessage) {
	o.deliver(cmdID, payload, true)
}

// ConfirmContent delivers the user-approved AI content to a paused job.
func (o *Orchestrator) ConfirmContent(cmdID string, payload json.RawMessage) {
	o.deliver(cmdID, payload, false)
}

func (o *Orchestrator) deliver(cmdID string, payload json.RawMessage, subtitle bool) {
	o.mu.Lock()
	h := o.jobs[cmdID]
	o.mu.Unlock()
	if h == nil {
		return
	}
	ch := h.confirmContent
	if subtitle {
		ch = h.confirmSub
	}
	select {
	case ch <- payload:
	default:
	}
}

func (o *Orchestrator) register(id string, h *jobHandle) {
	o.mu.Lock()
	o.jobs[id] = h
	o.mu.Unlock()
}

func (o *Orchestrator) unregister(id string) {
	o.mu.Lock()
	delete(o.jobs, id)
	o.mu.Unlock()
}

func (o *Orchestrator) update(jobID, status, step string, percent int) {
	_ = o.deps.Store.UpdateStatus(jobID, status, step, percent)
}

func (o *Orchestrator) fail(emit ipc.EmitFunc, cmdID, jobID, step string, err error) {
	o.deps.Logger.WithError(err).WithField("job_id", jobID).Errorf("pipeline: job failed at %s", step)
	_ = o.deps.Store.Fail(jobID, step, err.Error())
	emit(ipc.Event{ID: cmdID, Type: ipc.EvtFailed, Payload: map[string]string{"step": step, "error": err.Error()}})
}

// ─── progress helpers ───────────────────────────────────────────────────────

type progress struct {
	emit  ipc.EmitFunc
	cmdID string
}

func (p *progress) step(step string, percent int, msg string) {
	p.emit(ipc.Event{ID: p.cmdID, Type: ipc.EvtStep, Payload: map[string]interface{}{
		"step": step, "percent": percent, "message": msg,
	}})
}

func (p *progress) progress(step string, percent int, msg string) {
	p.emit(ipc.Event{ID: p.cmdID, Type: ipc.EvtProgress, Payload: map[string]interface{}{
		"step": step, "percent": percent, "message": msg,
	}})
}

func (p *progress) log(level, msg string) {
	p.emit(ipc.Event{ID: p.cmdID, Type: ipc.EvtLog, Payload: map[string]string{"level": level, "message": msg}})
}

func (p *progress) emitRaw(typ string, payload interface{}) {
	p.emit(ipc.Event{ID: p.cmdID, Type: typ, Payload: payload})
}

func errPayload(step, msg string, err error) map[string]string {
	return map[string]string{"step": step, "error": msg + ": " + err.Error()}
}
