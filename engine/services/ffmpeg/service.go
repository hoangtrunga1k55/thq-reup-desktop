package ffmpeg

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/sirupsen/logrus"
)

// SubtitleArea defines the region where old subtitles appear (to cover with a box).
type SubtitleArea struct {
	X, Y, Width, Height int
	Color               string  // hex without #
	Opacity             float64 // 0.0–1.0
	BorderRadius        int
}

// SubtitleStyle defines burned-in subtitle appearance.
type SubtitleStyle struct {
	FontName    string
	FontSize    int
	Color       string
	StrokeColor string
	StrokeWidth float64
	Position    string // "bottom" | "top" | "center"
	MarginV     int
	ScaleX      int // horizontal scale % (100=normal, 110=10% wider). 0 treated as 100.
}

// BrandConfig holds watermark settings.
type BrandConfig struct {
	Type      string // "text" | "image"
	Text      string
	Color     string // hex
	Font      string
	FontSize  int
	Position  string // top_left | top_right | bottom_left | bottom_right | top_center | bottom_center
	Opacity   float64
	ImagePath string // local path for image brand
}

// RenderConfig holds all parameters for the final render.
type RenderConfig struct {
	VideoPath    string
	AudioPath    string
	SRTPath      string
	OutputPath   string
	SubtitleArea *SubtitleArea
	Style        SubtitleStyle
	VideoVolume  float64 // original audio mix level
	AudioVolume  float64 // AI voice level
	VideoWidth   int
	VideoHeight  int

	// Anti-copyright
	FlipVideo bool
	VideoZoom float64 // 1.0 = no zoom, 1.05 = 5% zoom

	// Brand watermark
	Brand *BrandConfig

	// Hook text overlay (shown at video start, then disappears)
	HookText     string
	HookDuration float64 // seconds to show hook text (0 = disabled)
	HookLang     string  // target language for font selection
}

// fontFiles maps friendly font names to FILE NAMES (basenames). The actual file
// is resolved against the configured fonts dir at runtime (see Service.fontSpec),
// so the same map works for a bundled fonts dir on any OS.
var fontFiles = map[string]string{
	"Noto Sans":        "NotoSans-Regular.ttf",
	"Noto Sans Bold":   "NotoSans-Bold.ttf",
	"DejaVu Sans":      "DejaVuSans.ttf",
	"DejaVu Sans Bold": "DejaVuSans-Bold.ttf",
	// CJK fonts — support Korean, Chinese, Japanese
	"Noto Sans CJK Bold":    "NotoSansCJK-Bold.ttc",
	"Noto Sans CJK Regular": "NotoSansCJK-Regular.ttc",
	// Thai
	"Noto Sans Thai Bold": "NotoSansThai-ExtraBold.ttf",
}

// hookFontForLang returns the friendly font name to use for the target language.
func hookFontForLang(lang string) string {
	switch lang {
	case "Korean", "Chinese", "Japanese":
		return "Noto Sans CJK Bold"
	case "Thai":
		return "Noto Sans Thai Bold"
	default:
		return "Noto Sans Bold"
	}
}

// Service provides FFmpeg-based video processing.
type Service struct {
	logger     *logrus.Logger
	ffmpegBin  string
	ffprobeBin string
	fontsDir   string // dir holding bundled font files (for drawtext overlays)
}

// NewService creates a Service. ffmpegBin/ffprobeBin may be absolute paths to
// bundled binaries; empty values fall back to "ffmpeg"/"ffprobe" on PATH.
// fontsDir points to the bundled fonts; when empty/unresolved, drawtext overlays
// fall back to resolving fonts by name via fontconfig.
func NewService(logger *logrus.Logger, ffmpegBin, ffprobeBin, fontsDir string) *Service {
	if ffmpegBin == "" {
		ffmpegBin = "ffmpeg"
	}
	if ffprobeBin == "" {
		ffprobeBin = "ffprobe"
	}
	return &Service{logger: logger, ffmpegBin: ffmpegBin, ffprobeBin: ffprobeBin, fontsDir: fontsDir}
}

// fontSpec returns the drawtext font fragment for a friendly font name. It
// prefers a bundled file ("fontfile=<escaped path>") and falls back to resolving
// by name via fontconfig ("font=<name>"). Path escaping matters on Windows,
// where "C:\..." would otherwise break the filter's ':' option separator.
func (s *Service) fontSpec(friendly string) string {
	if file, ok := fontFiles[friendly]; ok && s.fontsDir != "" {
		p := filepath.Join(s.fontsDir, file)
		if _, err := os.Stat(p); err == nil {
			return "fontfile=" + escapeFilterPath(p)
		}
	}
	// Fallback: resolve by name via fontconfig. Quote in case the name has spaces.
	return "font='" + strings.ReplaceAll(friendly, "'", `\'`) + "'"
}

// escapeFilterPath makes a filesystem path safe inside an ffmpeg filter argument:
// backslashes → forward slashes, and ':' (e.g. the Windows drive colon) escaped.
// escapeFilterPath wraps a filesystem path for use as an ffmpeg filter option
// value on any OS. ffmpeg parses filter args in TWO passes:
//   - pass 1 (filtergraph): handles quotes; inside '...' a '\' is literal.
//   - pass 2 (the filter, e.g. ass/drawtext): splits options on ':'.
//
// So a Windows path needs BOTH: single quotes (so spaces survive pass 1 and the
// backslash isn't eaten) AND a '\:'-escaped colon (so pass 2 doesn't read the
// drive colon as an option separator). Result e.g. 'C\:/Users/.../Auto ReUp Studio/x.ttf'.
func escapeFilterPath(p string) string {
	p = strings.TrimPrefix(p, `\\?\`)    // drop Windows verbatim prefix
	p = strings.ReplaceAll(p, "\\", "/") // forward slashes
	p = strings.ReplaceAll(p, "'", `\'`) // escape single quotes
	p = strings.ReplaceAll(p, ":", `\:`) // escape colon for the filter's pass-2 parser
	return "'" + p + "'"
}

// NormalizeToFullHD re-encodes videoPath to exactly 1080×1920 (portrait Full HD).
// Smaller/different-ratio videos are scaled to fit then padded with black.
// If the video is already 1080×1920 this is a no-op.
// The output overwrites videoPath in-place via a temp file.
func (s *Service) NormalizeToFullHD(ctx context.Context, videoPath string) error {
	if w, h, err := s.VideoSize(ctx, videoPath); err == nil && w == 1080 && h == 1920 {
		return nil
	}
	tmpPath := videoPath + ".norm.mp4"
	err := s.run(ctx, []string{
		"-y", "-i", videoPath,
		"-vf", "scale=1080:1920:force_original_aspect_ratio=decrease,pad=1080:1920:(ow-iw)/2:(oh-ih)/2:black,setsar=1",
		"-c:v", "libx264", "-preset", "ultrafast", "-crf", "23",
		"-c:a", "copy",
		tmpPath,
	})
	if err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("ffmpeg: normalize to full hd: %w", err)
	}
	return os.Rename(tmpPath, videoPath)
}

// ExtractAudio extracts audio as MP3.
func (s *Service) ExtractAudio(ctx context.Context, videoPath, outputPath string) error {
	s.logger.WithFields(logrus.Fields{"video": videoPath, "out": outputPath}).Info("ffmpeg: extracting audio")
	return s.run(ctx, []string{
		"-y", "-i", videoPath, "-vn",
		"-acodec", "libmp3lame", "-q:a", "2",
		outputPath,
	})
}

// ExtractFrame captures a single frame at timestampSec.
func (s *Service) ExtractFrame(ctx context.Context, videoPath string, timestampSec float64, outputPath string) error {
	s.logger.WithFields(logrus.Fields{"video": videoPath, "ts": timestampSec}).Info("ffmpeg: extracting frame")
	return s.run(ctx, []string{
		"-y",
		"-ss", strconv.FormatFloat(timestampSec, 'f', 2, 64),
		"-i", videoPath,
		"-vframes", "1", "-q:v", "2",
		outputPath,
	})
}

// VideoSize returns the pixel dimensions of a video file using ffprobe.
func (s *Service) VideoSize(ctx context.Context, videoPath string) (w, h int, err error) {
	cmd := exec.CommandContext(ctx, s.ffprobeBin,
		"-v", "error",
		"-select_streams", "v:0",
		"-show_entries", "stream=width,height",
		"-of", "csv=s=x:p=0",
		videoPath,
	)
	var out bytes.Buffer
	cmd.Stdout = &out
	if runErr := cmd.Run(); runErr != nil {
		return 0, 0, runErr
	}
	parts := strings.SplitN(strings.TrimSpace(out.String()), "x", 2)
	if len(parts) != 2 {
		return 0, 0, fmt.Errorf("ffprobe: unexpected output %q", out.String())
	}
	w, err = strconv.Atoi(parts[0])
	if err != nil {
		return 0, 0, err
	}
	h, err = strconv.Atoi(parts[1])
	return w, h, err
}

// RenderFinalVideo creates the final reup video with all effects applied.
func (s *Service) RenderFinalVideo(ctx context.Context, cfg RenderConfig) error {
	s.logger.WithField("output", cfg.OutputPath).Info("ffmpeg: rendering final video")

	// Always use actual video dimensions so the zoom/crop math is correct
	// regardless of what VideoWidth/VideoHeight is stored in the DB.
	if actualW, actualH, probeErr := s.VideoSize(ctx, cfg.VideoPath); probeErr == nil && actualW > 0 {
		if cfg.VideoWidth != actualW || cfg.VideoHeight != actualH {
			s.logger.Infof("ffmpeg: overriding dimensions %dx%d → %dx%d from probe",
				cfg.VideoWidth, cfg.VideoHeight, actualW, actualH)
		}
		cfg.VideoWidth = actualW
		cfg.VideoHeight = actualH
	}

	// Subtitle: only burn when a subtitle area is set (subtitle disabled → nil).
	withSubtitle := cfg.SubtitleArea != nil
	assPath := ""
	if withSubtitle {
		assPath = strings.TrimSuffix(cfg.SRTPath, filepath.Ext(cfg.SRTPath)) + ".ass"
		if err := s.ConvertSRTtoASS(ctx, cfg.SRTPath, assPath, cfg.Style, cfg.VideoWidth, cfg.VideoHeight); err != nil {
			return fmt.Errorf("ffmpeg: convert srt to ass: %w", err)
		}
	}

	// Voice: only mix the AI voice when an audio path is set (voice disabled → "").
	withVoice := cfg.AudioPath != ""

	videoFilter := s.buildVideoFilter(cfg, assPath)
	// With voice: mix original + voice (duration=first anchors to the ORIGINAL
	// audio length so a shorter voice is padded with silence). Without voice:
	// keep the original audio at full volume.
	var audioFC string
	if withVoice {
		videoVol := cfg.VideoVolume
		if videoVol == 0 {
			videoVol = 0.15
		}
		audioVol := cfg.AudioVolume
		if audioVol == 0 {
			audioVol = 1.8
		}
		audioFC = fmt.Sprintf(
			"[0:a]volume=%.2f[a0];[1:a]volume=%.2f[a1];[a0][a1]amix=inputs=2:duration=first[aout]",
			videoVol, audioVol,
		)
	} else {
		audioFC = "[0:a]volume=1.0[aout]"
	}

	hasImageBrand := cfg.Brand != nil && cfg.Brand.Type == "image" && cfg.Brand.ImagePath != ""

	args := []string{"-y", "-i", cfg.VideoPath}
	if withVoice {
		args = append(args, "-i", cfg.AudioPath)
	}

	// Always use filter_complex for everything — mixing -vf with -filter_complex
	// causes "No such filter: '0'" in FFmpeg 6.x when [0:a] is referenced.
	if hasImageBrand {
		// Brand image input index shifts when there is no voice input.
		brandIdx := 1
		if withVoice {
			brandIdx = 2
		}
		args = append(args, "-i", cfg.Brand.ImagePath)

		logoSize := cfg.Brand.FontSize * 5
		if logoSize < 80 {
			logoSize = 80
		}
		logoScale := fmt.Sprintf("[%d:v]scale=%d:-1,format=rgba,colorchannelmixer=aa=%.2f[logo]",
			brandIdx, logoSize, cfg.Brand.Opacity)
		lx, ly := brandPositionExpr(cfg.Brand.Position, "overlay_w", "overlay_h")
		fc := fmt.Sprintf(
			"[0:v]%s[v_base];%s;[v_base][logo]overlay=%s:%s[vout];%s",
			videoFilter, logoScale, lx, ly, audioFC,
		)
		args = append(args, "-filter_complex", fc, "-map", "[vout]", "-map", "[aout]")
	} else {
		fc := fmt.Sprintf("[0:v]%s[vout];%s", videoFilter, audioFC)
		args = append(args, "-filter_complex", fc, "-map", "[vout]", "-map", "[aout]")
	}

	args = append(args,
		"-c:v", "libx264", "-preset", "veryfast", "-crf", "20",
		"-c:a", "aac", "-b:a", "128k",
		"-map_metadata", "-1", // strip metadata
		"-shortest",
		cfg.OutputPath,
	)

	if err := s.run(ctx, args); err != nil {
		return fmt.Errorf("ffmpeg: render final video: %w", err)
	}
	s.logger.WithField("output", cfg.OutputPath).Info("ffmpeg: render complete")
	return nil
}

func (s *Service) buildVideoFilter(cfg RenderConfig, assPath string) string {
	var parts []string

	// 1. Zoom + scale to target size
	zoom := cfg.VideoZoom
	if zoom < 1.0 {
		zoom = 1.0
	}
	cropW := cfg.VideoWidth
	cropH := cfg.VideoHeight
	if cropW%2 != 0 {
		cropW--
	}
	if cropH%2 != 0 {
		cropH--
	}
	scaledW := int(float64(cropW) * zoom)
	scaledH := int(float64(cropH) * zoom)
	// FFmpeg scale rounds to even; ensure pad target is also even to avoid "padded dimensions < input"
	if scaledW%2 != 0 {
		scaledW++
	}
	if scaledH%2 != 0 {
		scaledH++
	}
	parts = append(parts, fmt.Sprintf(
		"scale=%d:%d:force_original_aspect_ratio=decrease,pad=%d:%d:(ow-iw)/2:(oh-ih)/2:black,crop=%d:%d",
		scaledW, scaledH, scaledW, scaledH, cropW, cropH,
	))

	// 2. Flip
	if cfg.FlipVideo {
		parts = append(parts, "hflip")
	}

	// 3. Cover original subtitle area
	if cfg.SubtitleArea != nil {
		coverColor := hexToFFmpegColor(cfg.SubtitleArea.Color, cfg.SubtitleArea.Opacity)
		parts = append(parts, fmt.Sprintf(
			"drawbox=x=%d:y=%d:w=%d:h=%d:color=%s:t=fill",
			cfg.SubtitleArea.X, cfg.SubtitleArea.Y,
			cfg.SubtitleArea.Width, cfg.SubtitleArea.Height,
			coverColor,
		))
	}

	// 4. Burn in new subtitle (only when subtitle is enabled → assPath set).
	// Escape the path for the filter (Windows "C:\…" would otherwise break on the
	// ':' option separator) and point libass at the bundled fonts dir so subtitle
	// fonts resolve without system installation.
	if assPath != "" {
		assArg := "ass=" + escapeFilterPath(assPath)
		if s.fontsDir != "" {
			assArg += ":fontsdir=" + escapeFilterPath(s.fontsDir)
		}
		parts = append(parts, assArg)
	}

	// 5. Hook text overlay (shown at start of video, then disappears)
	if cfg.HookText != "" && cfg.HookDuration > 0 {
		if hook := s.buildHookText(cfg.HookText, cfg.HookDuration, cfg.HookLang); hook != "" {
			parts = append(parts, hook)
		}
	}

	// 6. Brand text watermark (always visible)
	if cfg.Brand != nil && cfg.Brand.Type == "text" && cfg.Brand.Text != "" {
		parts = append(parts, s.buildDrawText(cfg.Brand))
	}

	return strings.Join(parts, ",")
}

func (s *Service) buildDrawText(b *BrandConfig) string {
	x, y := brandPositionExpr(b.Position, "text_w", "text_h")

	// Resolve font (bundled file or fontconfig name).
	font := "Noto Sans Bold"
	if _, ok := fontFiles[b.Font]; ok {
		font = b.Font
	}

	color := strings.TrimPrefix(b.Color, "#")
	text := escapeBrandText(b.Text)

	return fmt.Sprintf(
		"drawtext=%s:text='%s':fontsize=%d:fontcolor=#%s@%.2f:x=%s:y=%s:shadowcolor=black@0.7:shadowx=2:shadowy=2",
		s.fontSpec(font), text, b.FontSize, color, b.Opacity, x, y,
	)
}

// buildHookText renders each wrapped line as a separate drawtext filter.
// This avoids relying on \n escape inside drawtext (unreliable across FFmpeg versions).
func (s *Service) buildHookText(text string, duration float64, lang string) string {
	fontSpec := s.fontSpec(hookFontForLang(lang))
	text = strings.TrimSpace(text)
	if text == "" {
		return ""
	}

	const fontSize = 46
	const lineHeight = 64 // px between line baselines (fontSize + padding)
	const maxCharsPerLine = 28

	rawLines := strings.Split(wrapHookText(text, maxCharsPerLine), "\n")

	var filters []string
	for i, line := range rawLines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		escaped := escapeBrandText(line)
		// y = 20% from top + offset per line
		yExpr := fmt.Sprintf("(main_h*0.20)+%d", i*lineHeight)
		filters = append(filters, fmt.Sprintf(
			"drawtext=%s:text='%s':fontsize=%d:fontcolor=white@0.95:"+
				"x=(main_w-text_w)/2:y=%s:"+
				"shadowcolor=black@0.9:shadowx=2:shadowy=2:"+
				"box=1:boxcolor=black@0.6:boxborderw=14:"+
				"enable='between(t,0,%.1f)'",
			fontSpec, escaped, fontSize, yExpr, duration,
		))
	}
	return strings.Join(filters, ",")
}

// wrapHookText wraps text at maxChars runes per line at word boundaries, max 3 lines.
func wrapHookText(text string, maxChars int) string {
	words := strings.Fields(text)
	if len(words) == 0 {
		return text
	}
	var lines []string
	var cur string
	for _, word := range words {
		candidate := word
		if cur != "" {
			candidate = cur + " " + word
		}
		if len([]rune(candidate)) <= maxChars {
			cur = candidate
		} else {
			if cur != "" {
				lines = append(lines, cur)
			}
			cur = word
		}
	}
	if cur != "" {
		lines = append(lines, cur)
	}
	if len(lines) > 3 {
		lines = lines[:3]
	}
	return strings.Join(lines, "\n")
}

func brandPositionExpr(position, textW, textH string) (string, string) {
	m := "40"
	switch position {
	case "top_left":
		return m, m
	case "top_right":
		return fmt.Sprintf("main_w-%s-%s", textW, m), m
	case "bottom_left":
		return m, fmt.Sprintf("main_h-%s-%s", textH, m)
	case "top_center":
		return fmt.Sprintf("(main_w-%s)/2", textW), m
	case "bottom_center":
		return fmt.Sprintf("(main_w-%s)/2", textW), fmt.Sprintf("main_h-%s-%s", textH, m)
	default: // bottom_right
		return fmt.Sprintf("main_w-%s-%s", textW, m), fmt.Sprintf("main_h-%s-%s", textH, m)
	}
}

func escapeBrandText(text string) string {
	text = strings.ReplaceAll(text, `\`, `\\`)
	text = strings.ReplaceAll(text, "'", `\'`)
	text = strings.ReplaceAll(text, ":", `\:`)
	return text
}

// ConvertSRTtoASS converts SRT to ASS with custom subtitle styling.
func (s *Service) ConvertSRTtoASS(ctx context.Context, srtPath, assPath string, style SubtitleStyle, videoW, videoH int) error {
	s.logger.WithFields(logrus.Fields{"srt": srtPath, "ass": assPath}).Debug("ffmpeg: converting SRT→ASS")

	if err := s.run(ctx, []string{"-y", "-i", srtPath, assPath}); err != nil {
		return err
	}

	data, err := os.ReadFile(assPath)
	if err != nil {
		return err
	}

	content := string(data)
	content = patchLine(content, "PlayResX:", fmt.Sprintf("PlayResX: %d", videoW))
	content = patchLine(content, "PlayResY:", fmt.Sprintf("PlayResY: %d", videoH))
	// WrapStyle 1: fill first line completely before wrapping.
	// patchLine only replaces existing lines; insert if missing.
	if strings.Contains(content, "WrapStyle:") {
		content = patchLine(content, "WrapStyle:", "WrapStyle: 1")
	} else {
		// Insert after PlayResY line
		content = strings.Replace(content,
			fmt.Sprintf("PlayResY: %d", videoH),
			fmt.Sprintf("PlayResY: %d\nWrapStyle: 1", videoH),
			1)
	}

	alignment := subtitleAlignment(style.Position)
	primaryColor := hexToASSColor(style.Color)
	strokeColor := hexToASSColor(style.StrokeColor)
	marginV := style.MarginV
	if marginV <= 0 {
		marginV = 40
	}

	scaleX := style.ScaleX
	if scaleX <= 0 {
		scaleX = 100
	}

	newStyle := fmt.Sprintf(
		"Style: Default,%s,%d,%s,&H000000FF,%s,&H64000000,0,0,0,0,%d,100,0,0,1,%.1f,0,%d,10,10,%d,1",
		style.FontName, style.FontSize, primaryColor, strokeColor,
		scaleX, style.StrokeWidth, alignment, marginV,
	)
	content = patchLinePrefix(content, "Style: Default", newStyle)

	return os.WriteFile(assPath, []byte(content), 0644)
}

// ─── Helpers ──────────────────────────────────────────────────────────────────

func (s *Service) run(ctx context.Context, args []string) error {
	cmd := exec.CommandContext(ctx, s.ffmpegBin, args...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	s.logger.WithField("cmd", s.ffmpegBin+" "+strings.Join(args, " ")).Debug("ffmpeg: running command")
	if err := cmd.Run(); err != nil {
		s.logger.WithError(err).WithField("stderr", stderr.String()).Error("ffmpeg: command failed")
		return fmt.Errorf("ffmpeg: %w\n%s", err, stderr.String())
	}
	return nil
}

func hexToFFmpegColor(hex string, opacity float64) string {
	hex = strings.TrimPrefix(hex, "#")
	if hex == "000000" {
		return fmt.Sprintf("black@%.2f", opacity)
	}
	return fmt.Sprintf("0x%s@%.2f", hex, opacity)
}

func hexToASSColor(hex string) string {
	hex = strings.TrimPrefix(hex, "#")
	if len(hex) != 6 {
		return "&H00FFFFFF"
	}
	return fmt.Sprintf("&H00%s%s%s", hex[4:6], hex[2:4], hex[0:2])
}

func subtitleAlignment(position string) int {
	switch strings.ToLower(position) {
	case "top":
		return 8
	case "center", "middle":
		return 5
	default:
		return 2
	}
}

func patchLine(content, prefix, replacement string) string {
	lines := strings.Split(content, "\n")
	for i, line := range lines {
		if strings.HasPrefix(line, prefix) {
			lines[i] = replacement
		}
	}
	return strings.Join(lines, "\n")
}

func patchLinePrefix(content, prefix, replacement string) string {
	return patchLine(content, prefix, replacement)
}
