package facebook

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"time"

	"github.com/sirupsen/logrus"
)

const (
	fbGraphBase      = "https://graph.facebook.com/v18.0"
	fbVideoGraphBase = "https://graph-video.facebook.com/v18.0"
	fbTimeout        = 120 * time.Second
	fbMaxRetries     = 2
)

// PostResult contains the Facebook post ID and URL after successful posting.
type PostResult struct {
	PostID  string `json:"post_id"`
	PostURL string `json:"post_url"`
}

// Client is the Facebook Graph API adapter for posting videos.
type Client struct {
	httpClient *http.Client
	logger     *logrus.Logger
}

// NewClient creates a new Facebook Graph API Client.
func NewClient(logger *logrus.Logger) *Client {
	return &Client{
		httpClient: &http.Client{Timeout: fbTimeout},
		logger:     logger,
	}
}

// UploadVideo uploads a video file to a Facebook Page.
// Uses multipart upload to POST /{page_id}/videos on graph-video.facebook.com.
//
//   - pageID       – Facebook Page ID (numeric string)
//   - accessToken  – Page access token
//   - videoBytes   – Raw video file bytes (MP4)
//   - title        – Video title (shown in post)
//   - description  – Video description / caption
func (c *Client) UploadVideo(
	ctx context.Context,
	pageID, accessToken string,
	videoBytes []byte,
	title, description string,
) (*PostResult, error) {
	c.logger.WithFields(logrus.Fields{
		"page_id": pageID,
		"bytes":   len(videoBytes),
	}).Info("facebook: uploading video")

	uploadURL := fmt.Sprintf("%s/%s/videos", fbVideoGraphBase, pageID)

	var lastErr error
	for attempt := 1; attempt <= fbMaxRetries; attempt++ {
		var buf bytes.Buffer
		mw := multipart.NewWriter(&buf)

		_ = mw.WriteField("access_token", accessToken)
		_ = mw.WriteField("title", title)
		_ = mw.WriteField("description", description)

		// Add video file field
		fw, err := mw.CreateFormFile("source", "video.mp4")
		if err != nil {
			return nil, fmt.Errorf("facebook: create form file: %w", err)
		}
		if _, err := fw.Write(videoBytes); err != nil {
			return nil, fmt.Errorf("facebook: write video bytes: %w", err)
		}
		mw.Close()

		req, err := http.NewRequestWithContext(ctx, http.MethodPost, uploadURL, &buf)
		if err != nil {
			return nil, err
		}
		req.Header.Set("Content-Type", mw.FormDataContentType())

		resp, err := c.httpClient.Do(req)
		if err != nil {
			lastErr = err
			c.logger.WithError(err).Warnf("facebook: upload attempt %d/%d failed", attempt, fbMaxRetries)
			time.Sleep(time.Duration(attempt) * 5 * time.Second)
			continue
		}
		defer resp.Body.Close()

		body, _ := io.ReadAll(resp.Body)
		if resp.StatusCode != http.StatusOK {
			lastErr = fmt.Errorf("status %d: %s", resp.StatusCode, string(body))
			c.logger.WithError(lastErr).Warnf("facebook: upload attempt %d non-200", attempt)
			time.Sleep(time.Duration(attempt) * 5 * time.Second)
			continue
		}

		var res struct {
			ID    string `json:"id"`
			Error *struct {
				Message string `json:"message"`
				Code    int    `json:"code"`
			} `json:"error"`
		}
		if err := json.Unmarshal(body, &res); err != nil {
			return nil, fmt.Errorf("facebook: parse upload response: %w", err)
		}
		if res.Error != nil {
			return nil, fmt.Errorf("facebook: API error %d: %s", res.Error.Code, res.Error.Message)
		}

		postURL := fmt.Sprintf("https://www.facebook.com/%s/videos/%s", pageID, res.ID)
		c.logger.WithFields(logrus.Fields{"post_id": res.ID, "url": postURL}).Info("facebook: video uploaded")
		return &PostResult{PostID: res.ID, PostURL: postURL}, nil
	}
	return nil, fmt.Errorf("facebook: all upload attempts failed: %w", lastErr)
}

// PostFeed posts a text message to a Facebook Page feed.
// Used for posting captions separately or when video upload isn't needed.
func (c *Client) PostFeed(ctx context.Context, pageID, accessToken, message string) (*PostResult, error) {
	c.logger.WithField("page_id", pageID).Info("facebook: posting to feed")

	feedURL := fmt.Sprintf("%s/%s/feed", fbGraphBase, pageID)

	payload := map[string]string{
		"access_token": accessToken,
		"message":      message,
	}
	body, _ := json.Marshal(payload)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, feedURL, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("facebook: feed post request: %w", err)
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("facebook: feed post status %d: %s", resp.StatusCode, string(respBody))
	}

	var res struct {
		ID    string `json:"id"`
		Error *struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(respBody, &res); err != nil {
		return nil, fmt.Errorf("facebook: parse feed response: %w", err)
	}
	if res.Error != nil {
		return nil, fmt.Errorf("facebook: API error: %s", res.Error.Message)
	}

	postURL := fmt.Sprintf("https://www.facebook.com/%s/posts/%s", pageID, res.ID)
	c.logger.WithField("post_id", res.ID).Info("facebook: feed post created")
	return &PostResult{PostID: res.ID, PostURL: postURL}, nil
}

// PostComment posts a text comment on a Facebook post/video.
// Returns the comment ID or empty string if message is empty.
func (c *Client) PostComment(ctx context.Context, postID, accessToken, message string) (string, error) {
	if message == "" {
		return "", nil
	}
	c.logger.WithField("post_id", postID).Info("facebook: posting comment")

	url := fmt.Sprintf("%s/%s/comments", fbGraphBase, postID)
	payload, _ := json.Marshal(map[string]string{
		"message":      message,
		"access_token": accessToken,
	})

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(payload))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("facebook: post comment: %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("facebook: comment status %d: %s", resp.StatusCode, string(body))
	}

	var res struct {
		ID    string `json:"id"`
		Error *struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	_ = json.Unmarshal(body, &res)
	if res.Error != nil {
		return "", fmt.Errorf("facebook: comment API error: %s", res.Error.Message)
	}
	c.logger.WithField("comment_id", res.ID).Info("facebook: comment posted")
	return res.ID, nil
}
