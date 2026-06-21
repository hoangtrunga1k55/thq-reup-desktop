package openai

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"mime/multipart"
	"net/http"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/sirupsen/logrus"
)

// ContentTone controls the style of AI-generated content.
type ContentTone string

const (
	ToneNatural      ContentTone = "natural"
	ToneViralTikTok  ContentTone = "viral_tiktok"
	ToneSales        ContentTone = "sales"
	ToneFunny        ContentTone = "funny"
	ToneProfessional ContentTone = "professional"
)

// AIContent holds all AI-generated metadata for a video.
type AIContent struct {
	Title    string   `json:"title"`
	Caption  string   `json:"caption"`
	Hashtags []string `json:"hashtags"`
}

// Client is the OpenAI service adapter.
type Client struct {
	httpClient *http.Client
	logger     *logrus.Logger
}

// NewClient creates a new OpenAI Client.
func NewClient(logger *logrus.Logger) *Client {
	return &Client{
		httpClient: &http.Client{Timeout: 120 * time.Second},
		logger:     logger,
	}
}

// ─── Transcribe ───────────────────────────────────────────────────────────────

// whisperWord is a single word with timing from Whisper verbose_json.
type whisperWord struct {
	Word  string  `json:"word"`
	Start float64 `json:"start"`
	End   float64 `json:"end"`
}

// whisperVerboseResponse is the verbose_json response from Whisper.
type whisperVerboseResponse struct {
	Words []whisperWord `json:"words"`
}

// TranscribeAudio sends audio bytes to OpenAI Whisper and returns SRT content.
// Uses word-level timestamps to produce well-grouped subtitles.
// audioFormat: "mp3", "mp4", "wav", "m4a", etc.
func (c *Client) TranscribeAudio(ctx context.Context, apiKey string, audioBytes []byte, audioFormat, language string) (string, error) {
	c.logger.WithFields(logrus.Fields{
		"format":   audioFormat,
		"language": language,
		"bytes":    len(audioBytes),
	}).Info("openai: transcribing audio with Whisper")

	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)

	filename := "audio." + audioFormat
	fw, err := mw.CreateFormFile("file", filename)
	if err != nil {
		return "", err
	}
	if _, err := io.Copy(fw, bytes.NewReader(audioBytes)); err != nil {
		return "", err
	}

	_ = mw.WriteField("model", "whisper-1")
	_ = mw.WriteField("response_format", "verbose_json")
	_ = mw.WriteField("timestamp_granularities[]", "word")
	if language != "" {
		if code := languageCode(language); code != "" {
			_ = mw.WriteField("language", code)
		}
	}
	mw.Close()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		"https://api.openai.com/v1/audio/transcriptions", &buf)
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+apiKey)
	req.Header.Set("Content-Type", mw.FormDataContentType())

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("openai: whisper request: %w", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("openai: whisper status %d: %s", resp.StatusCode, string(body))
	}

	var verboseResp whisperVerboseResponse
	if err := json.Unmarshal(body, &verboseResp); err != nil {
		return "", fmt.Errorf("openai: parse whisper response: %w", err)
	}

	srtContent := wordsToSRT(verboseResp.Words)
	c.logger.WithFields(logrus.Fields{
		"words":      len(verboseResp.Words),
		"srt_length": len(srtContent),
	}).Info("openai: transcription complete")
	return srtContent, nil
}

// wordsToSRT groups word-level timestamps into subtitle blocks and returns SRT.
// Mirrors the n8n JS grouping logic: max 10 words, max 4.5s, split on gaps > 0.6s
// or sentence-ending punctuation.
func wordsToSRT(words []whisperWord) string {
	const (
		maxWordsPerSub = 10
		maxDuration    = 4.5
		maxGap         = 0.6
		minDuration    = 0.8
	)

	type block struct {
		start float64
		end   float64
		words []string
	}

	sentenceEnd := regexp.MustCompile(`[.!?。！？]$`)

	var blocks []block
	var cur *block

	for _, w := range words {
		word := strings.TrimSpace(w.Word)
		if word == "" {
			continue
		}

		if cur == nil {
			cur = &block{start: w.Start, end: w.End, words: []string{word}}
			continue
		}

		gap := w.Start - cur.end
		duration := w.End - cur.start
		text := strings.Join(cur.words, " ")

		shouldSplit := len(cur.words) >= maxWordsPerSub ||
			duration >= maxDuration ||
			gap > maxGap ||
			sentenceEnd.MatchString(text)

		if shouldSplit {
			blocks = append(blocks, *cur)
			cur = &block{start: w.Start, end: w.End, words: []string{word}}
		} else {
			cur.words = append(cur.words, word)
			cur.end = w.End
		}
	}
	if cur != nil {
		blocks = append(blocks, *cur)
	}

	var sb strings.Builder
	for i, b := range blocks {
		end := b.end
		if end-b.start < minDuration {
			end = b.start + minDuration
		}
		text := strings.Join(b.words, " ")
		text = strings.TrimSpace(regexp.MustCompile(`\s+`).ReplaceAllString(text, " "))

		if i > 0 {
			sb.WriteString("\n\n")
		}
		sb.WriteString(fmt.Sprintf("%d\n%s --> %s\n%s",
			i+1,
			srtTime(b.start),
			srtTime(end),
			text,
		))
	}
	return sb.String()
}

// srtTime formats seconds as SRT timestamp HH:MM:SS,mmm.
func srtTime(sec float64) string {
	h := int(sec) / 3600
	m := (int(sec) % 3600) / 60
	s := int(sec) % 60
	ms := int(math.Round((sec - math.Floor(sec)) * 1000))
	if ms >= 1000 {
		ms = 999
	}
	return fmt.Sprintf("%02d:%02d:%02d,%03d", h, m, s, ms)
}

// ─── Translate ────────────────────────────────────────────────────────────────

// msToSRT formats milliseconds as an SRT timestamp (HH:MM:SS,mmm).
func msToSRT(ms int) string {
	if ms < 0 {
		ms = 0
	}
	return srtTime(float64(ms) / 1000.0)
}

// sentenceEndRe matches text whose final non-space char ends a sentence.
var sentenceEndRe = regexp.MustCompile(`[.!?。！？…]['")\]]?\s*$`)

// mergeIntoSentences joins consecutive SRT blocks into whole sentences so the
// translator receives complete units instead of mid-sentence fragments. Blocks
// are merged until the running text ends a sentence, a pause (gap) separates
// them, or a max duration is reached. Timestamps merge into one span; sequence
// numbers are renumbered 1..N.
func mergeIntoSentences(blocks []srtBlock) []srtBlock {
	const maxMergedMs = 8000 // keep a merged subtitle readable (~8s on screen)
	const maxGapMs = 800     // a pause longer than this ends the sentence
	var out []srtBlock
	var cur *srtBlock
	flush := func() {
		if cur != nil {
			out = append(out, *cur)
			cur = nil
		}
	}
	for i := range blocks {
		b := blocks[i]
		if cur == nil {
			c := b
			cur = &c
			continue
		}
		gap := b.startMs - cur.endMs
		mergedDur := b.endMs - cur.startMs
		if sentenceEndRe.MatchString(strings.TrimSpace(cur.text)) || gap > maxGapMs || mergedDur > maxMergedMs {
			flush()
			c := b
			cur = &c
			continue
		}
		cur.text = strings.TrimSpace(cur.text + " " + b.text)
		cur.endMs = b.endMs
	}
	flush()
	for i := range out {
		out[i].seq = i + 1
		out[i].timestamp = msToSRT(out[i].startMs) + " --> " + msToSRT(out[i].endMs)
	}
	return out
}

// TranslateSubtitle translates the text of each SRT block while preserving the
// EXACT original timestamps and block count. It never trusts the model to emit
// well-formed SRT (GPT tends to merge/drop/re-segment blocks, which silently
// compresses the timeline — e.g. a 2:13 source collapsing to 1:40). Instead it
// translates an indexed list of source lines and re-grafts the translations onto
// the original timestamps, guaranteeing the output timeline matches the input.
func (c *Client) TranslateSubtitle(ctx context.Context, apiKey, srtContent, targetLang string) (string, error) {
	c.logger.WithField("target_lang", targetLang).Info("openai: translating subtitle")

	blocks := parseSRTBlocks(srtContent)
	if len(blocks) == 0 {
		return "", fmt.Errorf("openai: translate subtitle: no valid SRT blocks to translate")
	}

	// Whisper fragments a sentence across several short blocks (max 10 words /
	// 4.5s). Translating those fragments one-by-one yields incomplete, mismatched
	// output. Merge fragments back into whole sentences (merging their timestamps
	// into one span) BEFORE translating, so each unit is a complete sentence.
	srcBlockCount := len(blocks)
	srcLastEndMs := blocks[len(blocks)-1].endMs
	blocks = mergeIntoSentences(blocks)
	c.logger.WithFields(logrus.Fields{
		"src_blocks":      srcBlockCount,
		"merged_blocks":   len(blocks),
		"src_last_end_ms": srcLastEndMs,
		"src_srt_chars":   len(srtContent),
	}).Info("openai: translate — block counts (diagnostic)")

	// Per-block spoken-time budget = the gap until the next subtitle starts (for
	// the last block, its own duration). Given to the model so it keeps each line
	// short enough to be spoken naturally within its slot — minimising overflow
	// that would otherwise get cut off during assembly.
	budgetSec := make(map[int]float64, len(blocks))
	for i, b := range blocks {
		slot := b.endMs - b.startMs
		if i < len(blocks)-1 {
			if gap := blocks[i+1].startMs - b.startMs; gap > 0 {
				slot = gap
			}
		}
		if slot < 300 {
			slot = 300
		}
		budgetSec[b.seq] = float64(slot) / 1000.0
	}

	targetIsChinese := strings.Contains(strings.ToLower(targetLang), "chin") ||
		strings.Contains(strings.ToLower(targetLang), "trung")

	systemPrompt := fmt.Sprintf(`You are a professional subtitle translator for video dubbing.
Auto-detect the source language of the input and TRANSLATE everything into %[1]s.

Each input line has the form:
[[n]] (Xs) source text
where X is the spoken-time budget in seconds available for that line.

STRICT RULES:
1. The OUTPUT language MUST be %[1]s for EVERY line — even when the source text is already in another language. Never leave a line in the source language; always render it in %[1]s. (If the source is already %[1]s, keep it in %[1]s.)
2. Output EXACTLY one line per input line, in the SAME order, keeping the EXACT [[n]] marker. NEVER merge, split, add, drop, or reorder lines — output line count MUST equal input line count.
3. Translate COMPLETELY and faithfully — never omit sentences or key information, and keep each line a full, grammatical sentence in %[1]s. Prefer concise, natural everyday wording to roughly fit the (Xs) spoken-time budget; but if a faithful translation runs a little long, that is FINE — the voice is sped up slightly to fit, so do NOT drop meaning, cut clauses, or truncate to save time.
4. Output ONLY "[[n]] translation". Do NOT echo the (Xs) budget, timestamps, notes, or markdown.

LANGUAGE-SPECIFIC GUIDANCE (apply only the bullet matching %[1]s):
- Vietnamese: everyday spoken words (e.g. "xe hơi" not "ô tô"). Avoid long compound noun phrases that are slow to pronounce.
- Thai/Korean/Japanese: keep syllable count close to the original.
- English: prefer contractions and short words; cut filler.

The example below illustrates ONLY the required FORMAT (one output line per input line, [[n]] markers preserved). Your actual output text MUST be written in %[1]s — NOT the language of the source:
Input:
[[1]] (1.2s) <source line 1, any language>
[[2]] (3.0s) <source line 2, any language>
Output:
[[1]] <line 1 translated into %[1]s>
[[2]] <line 2 translated into %[1]s>`, targetLang)

	// translateBatch translates a set of blocks in one GPT call and returns
	// seq → translated text. Reused to retry blocks the model skips or leaves
	// untranslated (GPT often drops the last lines of a long list).
	translateBatch := func(batch []srtBlock) map[int]string {
		var ub strings.Builder
		for _, b := range batch {
			fmt.Fprintf(&ub, "[[%d]] (%.1fs) %s\n", b.seq, budgetSec[b.seq], b.text)
		}
		result, err := c.chatCompletion(ctx, apiKey, systemPrompt, ub.String(), "gpt-4o", false)
		if err != nil {
			c.logger.WithError(err).Warn("openai: translate batch failed")
			return map[int]string{}
		}
		return parseIndexedTranslations(stripCodeFences(strings.TrimSpace(result)))
	}

	translations := map[int]string{}
	for seq, txt := range translateBatch(blocks) {
		translations[seq] = txt
	}
	c.logger.WithFields(logrus.Fields{
		"first_pass_translated": len(translations),
		"total_blocks":          len(blocks),
	}).Info("openai: translate — first pass (diagnostic)")

	// done reports whether a block has a usable translation: non-empty and, when
	// the target isn't Chinese, not still left in Han characters.
	done := func(seq int) bool {
		txt := strings.TrimSpace(translations[seq])
		if txt == "" {
			return false
		}
		if !targetIsChinese && containsHan(txt) {
			return false
		}
		return true
	}

	// Retry blocks left empty or still untranslated (up to 2 focused passes).
	for attempt := 0; attempt < 2; attempt++ {
		var missingBlocks []srtBlock
		for _, b := range blocks {
			if !done(b.seq) {
				missingBlocks = append(missingBlocks, b)
			}
		}
		if len(missingBlocks) == 0 {
			break
		}
		c.logger.WithField("missing", len(missingBlocks)).Info("openai: retrying untranslated subtitle blocks")
		for seq, txt := range translateBatch(missingBlocks) {
			if strings.TrimSpace(txt) != "" {
				translations[seq] = txt
			}
		}
	}

	// Reassemble using the ORIGINAL timestamps and block count. Any block the
	// model still failed to translate keeps its source text so the timeline is
	// never shortened or shifted.
	var sb strings.Builder
	missing := 0
	for i, b := range blocks {
		txt := strings.TrimSpace(translations[b.seq])
		if txt == "" {
			txt = b.text
			missing++
		}
		if i > 0 {
			sb.WriteString("\n\n")
		}
		fmt.Fprintf(&sb, "%d\n%s\n%s", b.seq, b.timestamp, txt)
	}
	if missing > 0 {
		c.logger.WithFields(logrus.Fields{"missing": missing, "total": len(blocks)}).
			Warn("openai: some subtitle blocks had no translation after retries, kept source text")
	}

	out := sb.String()
	if !isValidSRT(out) {
		return "", fmt.Errorf("openai: translated SRT has no valid subtitle entries")
	}
	c.logger.WithField("blocks", len(blocks)).Info("openai: subtitle translation complete")
	return out, nil
}

// srtBlock is a parsed SRT entry: sequence number, timestamp line, and text.
type srtBlock struct {
	seq       int
	timestamp string
	text      string
	startMs   int
	endMs     int
}

var srtTimeRangeRe = regexp.MustCompile(`(\d{2}):(\d{2}):(\d{2})[,.](\d{3})\s*-->\s*(\d{2}):(\d{2}):(\d{2})[,.](\d{3})`)

// parseSRTTimeRange extracts start/end milliseconds from an SRT timestamp line.
func parseSRTTimeRange(ts string) (startMs, endMs int) {
	m := srtTimeRangeRe.FindStringSubmatch(ts)
	if m == nil {
		return 0, 0
	}
	atoi := func(s string) int { n, _ := strconv.Atoi(s); return n }
	startMs = ((atoi(m[1])*60+atoi(m[2]))*60+atoi(m[3]))*1000 + atoi(m[4])
	endMs = ((atoi(m[5])*60+atoi(m[6]))*60+atoi(m[7]))*1000 + atoi(m[8])
	return startMs, endMs
}

// containsHan reports whether s contains Han (Chinese) characters — used to
// detect blocks the model left untranslated.
func containsHan(s string) bool {
	for _, r := range s {
		if unicode.Is(unicode.Han, r) {
			return true
		}
	}
	return false
}

// parseSRTBlocks parses SRT content into ordered blocks, joining multi-line
// text with a single space. Sequence numbers are normalised to 1..N for stable
// indexing regardless of the source numbering.
func parseSRTBlocks(srt string) []srtBlock {
	lines := strings.Split(strings.ReplaceAll(srt, "\r\n", "\n"), "\n")
	var blocks []srtBlock
	i := 0
	for i < len(lines) {
		if strings.TrimSpace(lines[i]) == "" {
			i++
			continue
		}
		// A block is an optional number line followed by a timestamp line, or a
		// bare timestamp line; then one or more text lines until a blank line.
		tsIdx := -1
		if strings.Contains(lines[i], " --> ") {
			tsIdx = i
		} else if i+1 < len(lines) && strings.Contains(lines[i+1], " --> ") {
			tsIdx = i + 1
		}
		if tsIdx == -1 {
			i++
			continue
		}
		ts := strings.TrimSpace(lines[tsIdx])
		i = tsIdx + 1
		var textParts []string
		for i < len(lines) && strings.TrimSpace(lines[i]) != "" {
			textParts = append(textParts, strings.TrimSpace(lines[i]))
			i++
		}
		startMs, endMs := parseSRTTimeRange(ts)
		blocks = append(blocks, srtBlock{
			seq:       len(blocks) + 1,
			timestamp: ts,
			text:      strings.Join(textParts, " "),
			startMs:   startMs,
			endMs:     endMs,
		})
	}
	return blocks
}

// parseIndexedTranslations parses lines of the form "[[n]] text" into a map of
// index → text. A non-empty line without a marker is treated as a continuation
// of the previous indexed line.
func parseIndexedTranslations(s string) map[int]string {
	re := regexp.MustCompile(`^\s*\[\[(\d+)\]\]\s*(.*)$`)
	out := map[int]string{}
	lastIdx := -1
	for _, line := range strings.Split(strings.ReplaceAll(s, "\r\n", "\n"), "\n") {
		if m := re.FindStringSubmatch(line); m != nil {
			idx, _ := strconv.Atoi(m[1])
			out[idx] = strings.TrimSpace(m[2])
			lastIdx = idx
		} else if trimmed := strings.TrimSpace(line); trimmed != "" && lastIdx != -1 {
			out[lastIdx] = strings.TrimSpace(out[lastIdx] + " " + trimmed)
		}
	}
	return out
}

// isValidSRT checks that the SRT contains at least one valid timestamp block.
func isValidSRT(srt string) bool {
	for _, line := range strings.Split(srt, "\n") {
		if strings.Contains(line, " --> ") {
			return true
		}
	}
	return false
}

// ─── AI Content Generation ────────────────────────────────────────────────────

// GenerateAIContent generates title, description, caption, and hashtags from
// the translated SRT content. Content is strictly derived from the video.
func (c *Client) GenerateAIContent(ctx context.Context, apiKey, translatedSRT, targetLang string, tone ContentTone) (*AIContent, error) {
	c.logger.WithFields(logrus.Fields{"tone": tone, "lang": targetLang}).Info("openai: generating AI content")

	toneDesc := toneDescription(tone)
	if targetLang == "" {
		targetLang = "Vietnamese"
	}

	systemPrompt := fmt.Sprintf(`You are a social media content writer specializing in viral video content.
Generate metadata for a video based ONLY on its subtitle content. Do NOT fabricate anything not in the subtitles.

IMPORTANT: All text fields (title, caption, hashtags) MUST be written in %s.

Tone style: %s

Return a JSON object with exactly these fields:
{
  "title": "main video title in %s (max 100 chars)",
  "caption": "engaging social media caption with emojis in %s (max 500 chars)",
  "hashtags": ["hashtag1", "hashtag2", ...]  // 8-12 hashtags without # prefix, in %s
}`, targetLang, toneDesc, targetLang, targetLang, targetLang)

	userPrompt := fmt.Sprintf("Generate content from this video subtitle:\n\n%s", translatedSRT)

	result, err := c.chatCompletion(ctx, apiKey, systemPrompt, userPrompt, "gpt-4o", true)
	if err != nil {
		return nil, fmt.Errorf("openai: generate content: %w", err)
	}

	// Parse JSON response
	result = stripCodeFences(result)
	var content AIContent
	if err := json.Unmarshal([]byte(result), &content); err != nil {
		return nil, fmt.Errorf("openai: parse content JSON: %w\nraw: %s", err, result)
	}

	c.logger.WithField("title", content.Title).Info("openai: AI content generation complete")
	return &content, nil
}

// ─── Internal helpers ─────────────────────────────────────────────────────────

// chatCompletion sends a chat completion request to GPT-4o.
// If jsonMode is true, enables JSON response format.
func (c *Client) chatCompletion(ctx context.Context, apiKey, systemPrompt, userPrompt, model string, jsonMode bool) (string, error) {
	type message struct {
		Role    string `json:"role"`
		Content string `json:"content"`
	}
	type responseFormat struct {
		Type string `json:"type"`
	}
	type request struct {
		Model          string          `json:"model"`
		Messages       []message       `json:"messages"`
		ResponseFormat *responseFormat `json:"response_format,omitempty"`
		Temperature    float64         `json:"temperature"`
	}

	reqBody := request{
		Model: model,
		Messages: []message{
			{Role: "system", Content: systemPrompt},
			{Role: "user", Content: userPrompt},
		},
		Temperature: 0.7,
	}
	if jsonMode {
		reqBody.ResponseFormat = &responseFormat{Type: "json_object"}
	}

	payload, err := json.Marshal(reqBody)
	if err != nil {
		return "", err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		"https://api.openai.com/v1/chat/completions",
		bytes.NewReader(payload))
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+apiKey)
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("openai: chat request: %w", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("openai: chat status %d: %s", resp.StatusCode, string(body))
	}

	var res struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
		Error *struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(body, &res); err != nil {
		return "", fmt.Errorf("openai: parse response: %w", err)
	}
	if res.Error != nil {
		return "", fmt.Errorf("openai: API error: %s", res.Error.Message)
	}
	if len(res.Choices) == 0 {
		return "", fmt.Errorf("openai: no choices returned")
	}
	return res.Choices[0].Message.Content, nil
}

// stripCodeFences removes ```json ... ``` or ``` ... ``` wrapping.
var codeFenceRegex = regexp.MustCompile("(?s)^```[a-zA-Z]*\\n?(.*?)```$")

func stripCodeFences(s string) string {
	s = strings.TrimSpace(s)
	if m := codeFenceRegex.FindStringSubmatch(s); len(m) == 2 {
		return strings.TrimSpace(m[1])
	}
	return s
}

// languageCode converts common language names to ISO-639-1 codes for Whisper.
func languageCode(lang string) string {
	m := map[string]string{
		"vietnamese": "vi",
		"english":    "en",
		"chinese":    "zh",
		"japanese":   "ja",
		"korean":     "ko",
		"french":     "fr",
		"german":     "de",
		"spanish":    "es",
		"thai":       "th",
	}
	return m[strings.ToLower(lang)]
}

// toneDescription returns a descriptive instruction for the given tone.
func toneDescription(tone ContentTone) string {
	switch tone {
	case ToneViralTikTok:
		return "Viral TikTok style: energetic, trendy, uses emojis, hooks readers instantly"
	case ToneSales:
		return "Sales-oriented: persuasive, highlights benefits, clear call-to-action"
	case ToneFunny:
		return "Humorous and entertaining: light-hearted, witty, relatable"
	case ToneProfessional:
		return "Professional and informative: clear, concise, authoritative tone"
	default: // natural
		return "Natural and conversational: authentic, genuine, easy to read"
	}
}

// KeepFileExtension is a helper to get the audio format from a file path.
func KeepFileExtension(path string) string {
	ext := filepath.Ext(path)
	if ext != "" {
		return strings.TrimPrefix(ext, ".")
	}
	return "mp3"
}

// GenerateHookText creates a short, curiosity-driven hook text shown at the
// start of the video. Returns plain text (1-2 lines, max 120 chars total).
func (c *Client) GenerateHookText(ctx context.Context, apiKey, translatedSRT, targetLang string) (string, error) {
	c.logger.Info("openai: generating hook text")

	// Take first ~800 chars of subtitle for context
	srtExcerpt := translatedSRT
	if len(srtExcerpt) > 800 {
		srtExcerpt = srtExcerpt[:800]
	}

	systemPrompt := fmt.Sprintf(`You are an expert at writing viral video hooks.
Generate ONE short hook text in %s that appears at the start of a video to grab attention.
Rules:
- Maximum 2 lines, 60 characters per line
- Create curiosity, surprise, or suspense
- Use "..." or "?" for cliffhanger effect  
- Do NOT reveal the content, just tease it
- Return ONLY the hook text, no quotes, no explanation`, targetLang)

	userPrompt := fmt.Sprintf("Video subtitle excerpt:\n%s", srtExcerpt)

	result, err := c.chatCompletion(ctx, apiKey, systemPrompt, userPrompt, "gpt-4o-mini", false)
	if err != nil {
		return "", fmt.Errorf("openai: generate hook text: %w", err)
	}

	// Clean up result
	result = strings.TrimSpace(result)
	result = strings.Trim(result, `"`)
	if len(result) > 150 {
		result = result[:150]
	}

	c.logger.WithField("hook", result).Info("openai: hook text generated")
	return result, nil
}
