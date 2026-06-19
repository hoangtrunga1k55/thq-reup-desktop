package thq

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/sirupsen/logrus"
)

const (
	defaultBaseURL  = "https://thq-solution-tools.io.vn"
	pollInterval    = 2 * time.Second
	pollMaxDuration = 5 * time.Minute
	requestTimeout  = 30 * time.Second
	maxRetries      = 3
)

// Platform constants for source video detection.
const (
	PlatformTikTok   = "tiktok"
	PlatformDouyin   = "douyin"
	PlatformFacebook = "facebook"
	PlatformUnknown  = "unknown"
)

// ErrNotSupported is returned when a platform is not supported for download.
var ErrNotSupported = errors.New("thq: platform not supported for download")

// VideoInfo contains parsed metadata and download URL for a video.
type VideoInfo struct {
	VideoID     string
	Title       string
	Author      string
	CoverURL    string
	DownloadURL string
	Duration    int    // seconds
	Platform    string // tiktok|douyin|facebook|unknown
	CreateTime  time.Time
}

// Client is the THQ Solution Tools API adapter.
// It detects the source platform from the URL and calls the correct endpoint.
type Client struct {
	baseURL    string
	httpClient *http.Client
	logger     *logrus.Logger
}

// NewClient returns a ready-to-use Client with sensible timeouts.
// baseURL defaults to the production service when empty.
func NewClient(logger *logrus.Logger, baseURL string) *Client {
	if strings.TrimSpace(baseURL) == "" {
		baseURL = defaultBaseURL
	}
	return &Client{
		baseURL: baseURL,
		httpClient: &http.Client{
			Timeout: requestTimeout,
		},
		logger: logger,
	}
}

// tiktokURLRegex matches TikTok video URLs.
var tiktokURLRegex = regexp.MustCompile(`(?i)^https?://(www\.)?tiktok\.com|^https?://(vm|vt)\.tiktok\.com`)

// douyinURLRegex matches Douyin video URLs.
var douyinURLRegex = regexp.MustCompile(`(?i)^https?://(www\.)?douyin\.com|^https?://v\.douyin\.com`)

// facebookURLRegex matches Facebook video URLs.
var facebookURLRegex = regexp.MustCompile(`(?i)^https?://(www\.)?facebook\.com|^https?://fb\.watch`)

// DetectPlatform examines a URL and returns the source platform name.
func DetectPlatform(rawURL string) string {
	switch {
	case tiktokURLRegex.MatchString(rawURL):
		return PlatformTikTok
	case douyinURLRegex.MatchString(rawURL):
		return PlatformDouyin
	case facebookURLRegex.MatchString(rawURL):
		return PlatformFacebook
	default:
		return PlatformUnknown
	}
}

// ParseAndDownload detects the platform, queues a download job via the THQ
// API, polls for completion, and returns the VideoInfo including DownloadURL.
//
// Facebook is NOT supported by THQ; ErrNotSupported is returned for that platform.
func (c *Client) ParseAndDownload(ctx context.Context, apiKey, rawURL string) (*VideoInfo, error) {
	platform := DetectPlatform(rawURL)
	c.logger.WithFields(logrus.Fields{"url": rawURL, "platform": platform}).Info("thq: detected platform")

	if platform == PlatformFacebook || platform == PlatformUnknown {
		return &VideoInfo{Platform: platform}, fmt.Errorf("%w: %s", ErrNotSupported, platform)
	}

	// Step 1: queue download job
	jobID, err := c.queueDownload(ctx, apiKey, rawURL, platform)
	if err != nil {
		return nil, fmt.Errorf("thq: queue download: %w", err)
	}

	c.logger.WithField("job_id", jobID).Info("thq: download job queued, polling…")

	// Step 2: poll until completed or failed
	result, err := c.pollJob(ctx, jobID, pollMaxDuration)
	if err != nil {
		return nil, fmt.Errorf("thq: poll job %s: %w", jobID, err)
	}

	// Step 3: map result to VideoInfo
	info := mapResultToVideoInfo(result, platform)
	c.logger.WithFields(logrus.Fields{"platform": platform, "title": info.Title}).Info("thq: download completed")
	return info, nil
}

// queueDownload sends the download request and returns the jobId.
func (c *Client) queueDownload(ctx context.Context, apiKey, rawURL, platform string) (string, error) {
	endpoint := c.downloadEndpoint(platform)
	payload := fmt.Sprintf(`{"url":%q}`, rawURL)

	var lastErr error

	for attempt := 1; attempt <= maxRetries; attempt++ {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(payload))
		if err != nil {
			return "", err
		}
		req.Header.Set("Authorization", "Bearer "+apiKey)
		req.Header.Set("Content-Type", "application/json")

		resp, err := c.httpClient.Do(req)
		if err != nil {
			lastErr = err
			c.logger.WithError(err).Warnf("thq: queue attempt %d/%d failed", attempt, maxRetries)
			time.Sleep(time.Duration(attempt) * time.Second) // simple linear backoff
			continue
		}
		defer resp.Body.Close()

		body, _ := io.ReadAll(resp.Body)
		if resp.StatusCode != http.StatusOK {
			lastErr = fmt.Errorf("status %d: %s", resp.StatusCode, string(body))
			c.logger.WithError(lastErr).Warnf("thq: queue attempt %d/%d got non-200", attempt, maxRetries)
			time.Sleep(time.Duration(attempt) * time.Second)
			continue
		}

		var res struct {
			Success bool `json:"success"`
			Data    struct {
				JobID   string `json:"jobId"`
				Message string `json:"message"`
			} `json:"data"`
			Error string `json:"error"`
		}
		if err := json.Unmarshal(body, &res); err != nil {
			return "", fmt.Errorf("thq: unmarshal queue response: %w", err)
		}
		if !res.Success || res.Data.JobID == "" {
			return "", fmt.Errorf("thq: queue returned no jobId: %s", res.Error)
		}
		return res.Data.JobID, nil
	}
	return "", fmt.Errorf("thq: all %d queue attempts failed: %w", maxRetries, lastErr)
}

// downloadEndpoint returns the correct download endpoint for a platform.
func (c *Client) downloadEndpoint(platform string) string {
	if platform == PlatformTikTok {
		return c.baseURL + "/api/tiktok/video/download"
	}
	return c.baseURL + "/api/video/download" // Douyin
}

// pollJob polls GET /api/job/:jobId until state is completed or failed,
// or until maxWait elapses.
func (c *Client) pollJob(ctx context.Context, jobID string, maxWait time.Duration) (map[string]interface{}, error) {
	deadline := time.Now().Add(maxWait)
	pollURL := c.baseURL + "/api/job/" + jobID

	for time.Now().Before(deadline) {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
		}

		req, err := http.NewRequestWithContext(ctx, http.MethodGet, pollURL, nil)
		if err != nil {
			return nil, err
		}

		resp, err := c.httpClient.Do(req)
		if err != nil {
			c.logger.WithError(err).Warn("thq: poll request error, retrying…")
			time.Sleep(pollInterval)
			continue
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()

		var res struct {
			Success bool `json:"success"`
			Data    struct {
				State  string                 `json:"state"`
				Error  string                 `json:"error"`
				Result map[string]interface{} `json:"result"`
			} `json:"data"`
		}
		if err := json.Unmarshal(body, &res); err != nil {
			c.logger.WithError(err).Warn("thq: poll unmarshal error, retrying…")
			time.Sleep(pollInterval)
			continue
		}

		switch res.Data.State {
		case "completed":
			return res.Data.Result, nil
		case "failed":
			return nil, fmt.Errorf("thq: job %s failed: %s", jobID, res.Data.Error)
		default:
			// waiting | active — keep polling
			c.logger.WithFields(logrus.Fields{"job_id": jobID, "state": res.Data.State}).Debug("thq: job still processing")
			time.Sleep(pollInterval)
		}
	}
	return nil, fmt.Errorf("thq: poll timeout after %s for job %s", maxWait, jobID)
}

// mapResultToVideoInfo converts the raw API result map to a VideoInfo struct.
func mapResultToVideoInfo(result map[string]interface{}, platform string) *VideoInfo {
	info := &VideoInfo{Platform: platform}

	if v, ok := result["video_id"].(string); ok {
		info.VideoID = v
	}
	if v, ok := result["title"].(string); ok {
		info.Title = v
	}
	if author, ok := result["author"].(map[string]interface{}); ok {
		if nick, ok := author["nickname"].(string); ok {
			info.Author = nick
		}
	}
	if v, ok := result["cover_url"].(string); ok {
		info.CoverURL = v
	}
	if v, ok := result["download_url"].(string); ok {
		info.DownloadURL = v
	}
	if v, ok := result["duration"].(float64); ok {
		info.Duration = int(v)
	}
	if v, ok := result["create_time"].(string); ok {
		if t, err := time.Parse(time.RFC3339, v); err == nil {
			info.CreateTime = t
		}
	}
	return info
}
