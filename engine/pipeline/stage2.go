package pipeline

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/thq-solution/auto-reup-studio-desktop/engine/services/openai"
)

// stage2 extracts audio, transcribes it (Whisper), translates the subtitle to the
// target language, and generates AI content + hook. Ported from ProcessStage2Task.
func (o *Orchestrator) stage2(ctx context.Context, pr *progress, st *jobState) error {
	// When both subtitle and voice are off, nothing needs the transcript /
	// translation / AI content — skip all OpenAI work (no transcribe/translate/gen).
	if !st.params.Settings.subtitleEnabled() && !st.params.Settings.voiceEnabled() {
		pr.log("info", "Phụ đề & lồng tiếng đều TẮT — bỏ qua transcribe/translate/AI content")
		pr.progress("generate_ai_content", 68, "Bỏ qua xử lý phụ đề & nội dung")
		return nil
	}

	key := st.params.Keys.OpenAI
	if key == "" {
		return fmt.Errorf("OpenAI API key chưa được cấu hình. Vào mục API Keys để thêm")
	}

	// ── Extract audio ─────────────────────────────────────────────────────────
	pr.step("extract_audio", 28, "Đang tách audio…")
	if err := o.ffmpeg.ExtractAudio(ctx, st.videoPath, st.audioPath); err != nil {
		return fmt.Errorf("extract audio: %w", err)
	}
	pr.progress("extract_audio", 35, "Audio đã tách xong")

	// ── Transcribe (Whisper, source language auto-detected) ────────────────────
	pr.step("transcribe", 38, "Đang transcribe audio bằng Whisper…")
	audioBytes, err := os.ReadFile(st.audioPath)
	if err != nil {
		return fmt.Errorf("read audio: %w", err)
	}
	originalSRT, err := o.openai.TranscribeAudio(ctx, key, audioBytes, "mp3", "")
	if err != nil {
		return fmt.Errorf("transcribe: %w", err)
	}
	pr.progress("transcribe", 50, "Transcription hoàn tất")

	// ── Translate ──────────────────────────────────────────────────────────────
	lang := st.params.Settings.TargetLanguage
	pr.step("translate", 52, fmt.Sprintf("Đang dịch subtitle sang %s…", lang))
	translatedSRT, err := o.openai.TranslateSubtitle(ctx, key, originalSRT, lang)
	if err != nil {
		return fmt.Errorf("translate: %w", err)
	}
	st.translatedSRT = translatedSRT
	if err := os.WriteFile(st.srtPath, []byte(translatedSRT), 0o644); err != nil {
		return fmt.Errorf("write srt: %w", err)
	}
	pr.progress("translate", 60, "Subtitle đã được dịch")

	// ── AI content (+ hook) ─────────────────────────────────────────────────────
	pr.step("generate_ai_content", 62, "Đang tạo AI title/caption…")
	if st.params.Settings.AutoGenerateContent {
		content, cErr := o.openai.GenerateAIContent(ctx, key, translatedSRT, lang, openai.ToneNatural)
		if cErr != nil {
			pr.log("warn", "Tạo AI content thất bại: "+cErr.Error())
		} else {
			st.aiContent = content
			if st.params.Settings.HookEnabled {
				if ht, htErr := o.openai.GenerateHookText(ctx, key, translatedSRT, lang); htErr != nil {
					pr.log("warn", "Tạo hook text thất bại: "+htErr.Error())
				} else {
					st.hookText = strings.TrimSpace(ht)
				}
			}
		}
		pr.progress("generate_ai_content", 68, "AI content đã được tạo")
	} else {
		pr.progress("generate_ai_content", 68, "Bỏ qua tạo AI content (tắt trong cài đặt)")
	}
	return nil
}
