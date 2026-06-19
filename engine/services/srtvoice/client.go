package srtvoice

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"os"
	"strings"
	"time"

	"github.com/sirupsen/logrus"
)

const (
	srtVoiceBaseURL   = "https://srt-to-voice.io.vn"
	svPollInterval    = 3 * time.Second
	svPollMaxDuration = 10 * time.Minute
	svRequestTimeout  = 60 * time.Second
	svMaxRetries      = 3
)

// ProgressFunc is called on each poll with the latest conversion progress.
// entryCount is 0 when the provider does not report per-entry progress (e.g. edge-tts).
type ProgressFunc func(status string, progress, currentEntry, entryCount int)

// ConvertParams holds all parameters for an SRT-to-audio conversion request.
type ConvertParams struct {
	SRTText         string // Raw SRT content (used if SRTFilePath is empty)
	SRTFilePath     string // Path to .srt file on disk (preferred over SRTText)
	Provider        string // "edge-tts" | "vbee" (defaults to edge-tts)
	Voice           string // edge: "vi-VN-HoaiMyNeural"; vbee: "hn_female_ngochuyen_full_48k-fhg"
	OutputFormat    string // "mp3" | "wav"
	Rate            string // e.g. "+0%"
	Volume          string // e.g. "+0%"
	Pitch           string // e.g. "+0Hz"
	Strategy        string // "pad" | "speed" | "trim"
	SpeedAdjustment int    // -20..+20: negative = slower, positive = faster (e.g. -15 = 15% slower)

	// OnProgress, if set, is invoked on each status poll. Optional.
	OnProgress ProgressFunc
}

// Voice represents an available TTS voice from the SRT To Voice API.
type Voice struct {
	ShortName    string `json:"ShortName"`
	FriendlyName string `json:"FriendlyName"`
	Locale       string `json:"Locale"`
	Gender       string `json:"Gender"`
}

// Client is the SRT To Voice API adapter.
type Client struct {
	baseURL    string
	httpClient *http.Client
	logger     *logrus.Logger
}

// NewClient returns a ready-to-use Client.
// baseURL defaults to the production service if empty.
func NewClient(logger *logrus.Logger, baseURL string) *Client {
	if baseURL == "" {
		baseURL = srtVoiceBaseURL
	}
	return &Client{
		baseURL: baseURL,
		httpClient: &http.Client{
			Timeout: svRequestTimeout,
		},
		logger: logger,
	}
}

// ConvertAndDownload converts SRT text to audio, polls until completed, then
// downloads and returns the raw audio bytes and content type (e.g. "audio/mpeg").
// ConvertAndDownload converts SRT text to audio, polls until completed, then
// downloads and returns the raw audio bytes and content type.
func (c *Client) ConvertAndDownload(ctx context.Context, apiKey string, params ConvertParams) ([]byte, string, error) {
	// Step 1: Submit conversion job
	taskID, err := c.submitConvert(ctx, apiKey, params)
	if err != nil {
		return nil, "", fmt.Errorf("srtvoice: submit convert: %w", err)
	}

	c.logger.WithField("task_id", taskID).Info("srtvoice: conversion task submitted, polling…")

	// Step 2: Poll until completed
	if err := c.pollStatus(ctx, apiKey, taskID, svPollMaxDuration, params.OnProgress); err != nil {
		return nil, "", fmt.Errorf("srtvoice: poll task %s: %w", taskID, err)
	}

	// Step 3: Download audio
	audioBytes, contentType, err := c.downloadAudio(ctx, apiKey, taskID)
	if err != nil {
		return nil, "", fmt.Errorf("srtvoice: download audio for task %s: %w", taskID, err)
	}

	c.logger.WithFields(logrus.Fields{
		"task_id":      taskID,
		"content_type": contentType,
		"bytes":        len(audioBytes),
	}).Info("srtvoice: audio downloaded successfully")

	return audioBytes, contentType, nil
}

// submitConvert sends the multipart/form-data request to /api/convert.
// Returns the task_id from the response.
func (c *Client) submitConvert(ctx context.Context, apiKey string, params ConvertParams) (string, error) {
	// Apply defaults
	if params.Provider == "" {
		params.Provider = "edge-tts"
	}
	if params.Voice == "" {
		params.Voice = "vi-VN-HoaiMyNeural"
	}
	if params.OutputFormat == "" {
		params.OutputFormat = "mp3"
	}
	if params.Rate == "" {
		params.Rate = "+0%"
	}
	if params.Volume == "" {
		params.Volume = "+0%"
	}
	if params.Pitch == "" {
		params.Pitch = "+0Hz"
	}
	if params.Strategy == "" {
		params.Strategy = "pad"
	}

	var lastErr error
	for attempt := 1; attempt <= svMaxRetries; attempt++ {
		body := &bytes.Buffer{}
		writer := multipart.NewWriter(body)

		// Upload SRT as text/plain file
		h := make(textproto.MIMEHeader)
		h.Set("Content-Disposition", `form-data; name="file"; filename="subtitle.srt"`)
		h.Set("Content-Type", "text/plain; charset=utf-8")
		fw, err := writer.CreatePart(h)
		if err != nil {
			return "", fmt.Errorf("srtvoice: create form file: %w", err)
		}
		// Prefer reading from disk file — more reliable than in-memory string
		if params.SRTFilePath != "" {
			f, ferr := os.Open(params.SRTFilePath)
			if ferr != nil {
				return "", fmt.Errorf("srtvoice: open srt file %s: %w", params.SRTFilePath, ferr)
			}
			_, ferr = io.Copy(fw, f)
			f.Close()
			if ferr != nil {
				return "", fmt.Errorf("srtvoice: copy srt file: %w", ferr)
			}
		} else {
			if _, err = io.WriteString(fw, params.SRTText); err != nil {
				return "", fmt.Errorf("srtvoice: write srt content: %w", err)
			}
		}

		_ = writer.WriteField("provider", params.Provider)
		_ = writer.WriteField("voice", params.Voice)
		_ = writer.WriteField("output_format", params.OutputFormat)
		_ = writer.WriteField("rate", params.Rate)
		_ = writer.WriteField("volume", params.Volume)
		_ = writer.WriteField("pitch", params.Pitch)
		_ = writer.WriteField("strategy", params.Strategy)
		_ = writer.WriteField("speed_adjustment", fmt.Sprintf("%d", params.SpeedAdjustment))
		if apiKey != "" {
			_ = writer.WriteField("token", apiKey)
		}
		writer.Close()

		c.logger.WithFields(logrus.Fields{
			"provider":         params.Provider,
			"voice":            params.Voice,
			"strategy":         params.Strategy,
			"rate":             params.Rate,
			"speed_adjustment": params.SpeedAdjustment,
			"srt_file":         params.SRTFilePath,
		}).Info("srtvoice: submitting convert request")

		req, err := http.NewRequestWithContext(ctx, http.MethodPost,
			c.baseURL+"/api/convert", body)
		if err != nil {
			return "", err
		}
		req.Header.Set("Content-Type", writer.FormDataContentType())
		if apiKey != "" {
			req.Header.Set("Authorization", "Bearer "+apiKey)
		}

		resp, err := c.httpClient.Do(req)
		if err != nil {
			lastErr = err
			c.logger.WithError(err).Warnf("srtvoice: submit attempt %d/%d failed", attempt, svMaxRetries)
			time.Sleep(time.Duration(attempt) * 2 * time.Second)
			continue
		}
		respBody, _ := io.ReadAll(resp.Body)
		resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			lastErr = fmt.Errorf("status %d: %s", resp.StatusCode, string(respBody))
			srtPreview := params.SRTText
			if len(srtPreview) > 200 {
				srtPreview = srtPreview[:200]
			}
			c.logger.WithFields(logrus.Fields{
				"status":      resp.StatusCode,
				"srt_preview": srtPreview,
				"srt_len":     len(params.SRTText),
			}).Warnf("srtvoice: submit attempt %d got non-200", attempt)
			time.Sleep(time.Duration(attempt) * 2 * time.Second)
			continue
		}

		var res map[string]interface{}
		if err := json.Unmarshal(respBody, &res); err != nil {
			return "", fmt.Errorf("srtvoice: unmarshal submit response: %w", err)
		}

		taskID, _ := res["task_id"].(string)
		if taskID == "" {
			return "", fmt.Errorf("srtvoice: submit returned no task_id: %s", string(respBody))
		}
		return taskID, nil
	}
	return "", fmt.Errorf("srtvoice: all %d submit attempts failed: %w", svMaxRetries, lastErr)
}

// pollStatus polls GET /api/status/:task_id until status is "completed" or "failed".
// onProgress, when non-nil, is invoked on each poll where progress advanced.
func (c *Client) pollStatus(ctx context.Context, apiKey, taskID string, maxWait time.Duration, onProgress ProgressFunc) error {
	deadline := time.Now().Add(maxWait)
	pollURL := fmt.Sprintf("%s/api/status/%s", c.baseURL, taskID)
	lastProgress := -1

	for time.Now().Before(deadline) {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		req, err := http.NewRequestWithContext(ctx, http.MethodGet, pollURL, nil)
		if err != nil {
			return err
		}
		if apiKey != "" {
			req.Header.Set("Authorization", "Bearer "+apiKey)
			q := req.URL.Query()
			q.Set("token", apiKey)
			req.URL.RawQuery = q.Encode()
		}

		resp, err := c.httpClient.Do(req)
		if err != nil {
			c.logger.WithError(err).Warn("srtvoice: poll error, retrying…")
			time.Sleep(svPollInterval)
			continue
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()

		var res map[string]interface{}
		if err := json.Unmarshal(body, &res); err != nil {
			time.Sleep(svPollInterval)
			continue
		}

		status, _ := res["status"].(string)
		progress := jsonInt(res["progress"])
		currentEntry := jsonInt(res["current_entry"])
		entryCount := jsonInt(res["entry_count"])
		c.logger.WithFields(logrus.Fields{
			"task_id": taskID, "status": status, "progress": progress,
			"current_entry": currentEntry, "entry_count": entryCount,
		}).Debug("srtvoice: poll status")

		// Emit progress only when it advances, to avoid spamming the SSE hub.
		if onProgress != nil && progress > lastProgress {
			lastProgress = progress
			onProgress(status, progress, currentEntry, entryCount)
		}

		switch strings.ToLower(status) {
		case "completed":
			return nil
		case "failed", "error":
			msg, _ := res["message"].(string)
			return fmt.Errorf("srtvoice: task %s failed: %s", taskID, msg)
		default:
			time.Sleep(svPollInterval)
		}
	}
	return fmt.Errorf("srtvoice: poll timeout for task %s", taskID)
}

// downloadAudio fetches the converted audio from GET /api/download/:task_id.
// Returns raw bytes and the Content-Type header value.
func (c *Client) downloadAudio(ctx context.Context, apiKey, taskID string) ([]byte, string, error) {
	dlURL := fmt.Sprintf("%s/api/download/%s", c.baseURL, taskID)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, dlURL, nil)
	if err != nil {
		return nil, "", err
	}
	if apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+apiKey)
		q := req.URL.Query()
		q.Set("token", apiKey)
		req.URL.RawQuery = q.Encode()
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		return nil, "", fmt.Errorf("srtvoice: download status %d: %s", resp.StatusCode, string(b))
	}

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, "", err
	}
	return data, resp.Header.Get("Content-Type"), nil
}

// GetVoices returns the available TTS voices for a provider, filtered by locale (optional).
// provider defaults to "edge-tts" when empty.
func (c *Client) GetVoices(ctx context.Context, apiKey, provider, locale string) ([]Voice, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/api/voices", nil)
	if err != nil {
		return nil, err
	}
	q := req.URL.Query()
	if provider == "" {
		provider = "edge-tts"
	}
	q.Set("provider", provider)
	if locale != "" {
		q.Set("locale", locale)
	}
	if apiKey != "" {
		q.Set("token", apiKey)
		req.Header.Set("Authorization", "Bearer "+apiKey)
	}
	req.URL.RawQuery = q.Encode()

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)

	// Try direct JSON array (success case)
	var voices []Voice
	if err := json.Unmarshal(body, &voices); err == nil {
		return voices, nil
	}

	// API returned a JSON object — check for error detail or wrapped array
	var obj map[string]json.RawMessage
	if json.Unmarshal(body, &obj) == nil {
		// Try common wrapper keys
		for _, key := range []string{"data", "voices", "items", "results"} {
			if raw, ok := obj[key]; ok {
				var vs []Voice
				if json.Unmarshal(raw, &vs) == nil {
					return vs, nil
				}
			}
		}
		// Extract error detail for a cleaner error message
		if detail, ok := obj["detail"]; ok {
			var msg string
			_ = json.Unmarshal(detail, &msg)
			return nil, fmt.Errorf("srtvoice: voices API error: %s", msg)
		}
	}

	c.logger.WithField("status", resp.StatusCode).WithField("body", string(body[:min(len(body), 200)])).Warn("srtvoice: unexpected voices response")
	return nil, fmt.Errorf("srtvoice: parse voices: unexpected response format (status %d)", resp.StatusCode)
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// jsonInt coerces a decoded JSON value (float64 / int / nil) to an int, defaulting to 0.
func jsonInt(v interface{}) int {
	switch n := v.(type) {
	case float64:
		return int(n)
	case int:
		return n
	default:
		return 0
	}
}
