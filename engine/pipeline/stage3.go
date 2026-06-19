package pipeline

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/thq-solution/auto-reup-studio-desktop/engine/services/ffmpeg"
	"github.com/thq-solution/auto-reup-studio-desktop/engine/services/srtvoice"
)

// stage3 synthesises the AI voice, renders the final video, and optionally posts
// to Facebook. Ported from ProcessStage3Task (render-slot semaphore dropped —
// a desktop runs a single user's jobs).
func (o *Orchestrator) stage3(ctx context.Context, pr *progress, st *jobState) error {
	s := st.params.Settings

	// Validate SRT has at least one timestamp before spending on TTS/render.
	if st.translatedSRT == "" || !strings.Contains(st.translatedSRT, " --> ") {
		return fmt.Errorf("subtitle SRT rỗng hoặc không hợp lệ (không có timestamp)")
	}
	// Persist any edits made during manual confirmation.
	if err := os.WriteFile(st.srtPath, []byte(st.translatedSRT), 0o644); err != nil {
		return fmt.Errorf("write srt: %w", err)
	}

	// ── Generate voice ──────────────────────────────────────────────────────────
	if st.params.Keys.SRTVoice == "" {
		return fmt.Errorf("SRT-To-Voice API key chưa được cấu hình. Vào mục API Keys để thêm")
	}
	pr.step("generate_voice", 70, "Đang tạo giọng đọc AI…")
	voiceParams := srtvoice.ConvertParams{
		SRTFilePath:     st.srtPath,
		Provider:        s.VoiceProvider,
		Voice:           s.Voice,
		OutputFormat:    "mp3",
		Strategy:        "speed",
		Rate:            "+0%",
		SpeedAdjustment: s.SpeedAdjustment,
		OnProgress: func(_ string, progress, currentEntry, entryCount int) {
			msg := "Đang tạo giọng đọc AI…"
			if entryCount > 0 {
				msg = fmt.Sprintf("Đang tạo giọng đọc %d/%d (%d%%)", currentEntry, entryCount, progress)
			} else if progress > 0 {
				msg = fmt.Sprintf("Đang tạo giọng đọc (%d%%)", progress)
			}
			pct := 70 + progress/10 // map 0–100 into the 70–80 band
			if pct > 80 {
				pct = 80
			}
			pr.progress("generate_voice", pct, msg)
		},
	}
	voiceBytes, _, err := o.srtvoice.ConvertAndDownload(ctx, st.params.Keys.SRTVoice, voiceParams)
	if err != nil {
		return fmt.Errorf("generate voice: %w", err)
	}
	if err := os.WriteFile(st.voicePath, voiceBytes, 0o644); err != nil {
		return fmt.Errorf("write voice: %w", err)
	}
	pr.progress("generate_voice", 80, "Giọng đọc đã được tạo")

	// ── Render ───────────────────────────────────────────────────────────────────
	pr.step("render", 82, "Đang render video cuối…")
	cfg := o.buildRenderConfig(ctx, st)
	if err := o.ffmpeg.RenderFinalVideo(ctx, cfg); err != nil {
		return fmt.Errorf("render: %w", err)
	}
	pr.progress("render", 92, "Video đã render xong")

	// ── Post to Facebook (optional) ───────────────────────────────────────────────
	if s.AutoPostToFacebook && st.params.Keys.FacebookToken != "" && s.FacebookPageID != "" {
		pr.step("auto_post", 95, "Đang đăng lên Facebook…")
		if err := o.postFacebook(ctx, pr, st); err != nil {
			// Non-fatal: the video is rendered; surface the failure but still complete.
			pr.log("warn", "Đăng Facebook thất bại: "+err.Error())
		}
	}
	return nil
}

// buildRenderConfig assembles the ffmpeg RenderConfig from settings + the detected
// (or user-confirmed) subtitle region. Ported from ProcessStage3Task render setup.
func (o *Orchestrator) buildRenderConfig(ctx context.Context, st *jobState) ffmpeg.RenderConfig {
	s := st.params.Settings

	// Cover box: a strip centred on the detected subtitle that hides the original.
	var coverArea *ffmpeg.SubtitleArea
	marginV := 0
	if st.region != nil && st.region.Height > 0 {
		const coverPadV = 24
		coverH := s.SubtitleSize + coverPadV*2 // font size, not the (oversized) OCR height
		centerY := st.region.Y + st.region.Height/2
		coverY := centerY - coverH/2
		if coverY < 0 {
			coverY = 0
		}
		if s.CoverOpacity > 0 {
			coverArea = &ffmpeg.SubtitleArea{
				X:       0,
				Y:       coverY,
				Width:   st.vw,
				Height:  coverH,
				Color:   s.SubtitleStrokeColor, // cover reuses the stroke colour
				Opacity: s.CoverOpacity,
			}
		}
		marginV = coverY + (coverH-s.SubtitleSize)/2
		if marginV < 0 {
			marginV = 0
		}
	}

	// Brand watermark.
	var brand *ffmpeg.BrandConfig
	if s.BrandEnabled {
		brandImagePath := ""
		if s.BrandType == "image" && s.BrandImageURL != "" && s.BrandImageURL != "local" {
			imgPath := filepath.Join(st.jobDir, "brand_image.png")
			if imgBytes, derr := downloadBrandImage(ctx, s.BrandImageURL); derr == nil {
				_ = os.WriteFile(imgPath, imgBytes, 0o644)
				brandImagePath = imgPath
			}
		}
		brand = &ffmpeg.BrandConfig{
			Type:      s.BrandType,
			Text:      s.BrandText,
			Color:     s.BrandTextColor,
			Font:      s.BrandFont,
			FontSize:  s.BrandFontSize,
			Position:  s.BrandPosition,
			Opacity:   s.BrandOpacity,
			ImagePath: brandImagePath,
		}
	}

	cfg := ffmpeg.RenderConfig{
		VideoPath:    st.videoPath,
		AudioPath:    st.voicePath,
		SRTPath:      st.srtPath,
		OutputPath:   st.outputPath,
		VideoWidth:   st.vw,
		VideoHeight:  st.vh,
		SubtitleArea: coverArea,
		Style: ffmpeg.SubtitleStyle{
			FontName:    subtitleFontForLanguage(s.TargetLanguage, s.SubtitleFont),
			FontSize:    s.SubtitleSize,
			Color:       s.SubtitleColor,
			StrokeColor: s.SubtitleStrokeColor,
			StrokeWidth: s.SubtitleStrokeWidth,
			Position:    "top",
			MarginV:     marginV,
			ScaleX:      s.SubtitleScaleX,
		},
		FlipVideo:   s.FlipVideo,
		VideoZoom:   s.VideoZoom,
		VideoVolume: s.OriginalAudioVolume,
		AudioVolume: 1.8,
		Brand:       brand,
	}
	if s.HookEnabled && st.hookText != "" {
		cfg.HookText = st.hookText
		cfg.HookDuration = s.HookDuration
		cfg.HookLang = s.TargetLanguage
	}
	return cfg
}

// postFacebook uploads the rendered video to the configured page.
func (o *Orchestrator) postFacebook(ctx context.Context, pr *progress, st *jobState) error {
	s := st.params.Settings
	outputBytes, err := os.ReadFile(st.outputPath)
	if err != nil {
		return fmt.Errorf("read output: %w", err)
	}
	title, description := "", ""
	if st.aiContent != nil {
		title = st.aiContent.Title
		description = st.aiContent.Caption
	}
	if title == "" && st.videoInfo != nil {
		title = st.videoInfo.Title
	}
	res, err := o.facebook.UploadVideo(ctx, s.FacebookPageID, st.params.Keys.FacebookToken, outputBytes, title, description)
	if err != nil {
		return err
	}
	pr.log("info", "Đã đăng Facebook: "+res.PostURL)
	return nil
}
