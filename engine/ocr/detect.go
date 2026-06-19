package ocr

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"time"

	"github.com/sirupsen/logrus"
)

// SubtitleRegion defines a rectangular area of the video frame.
type SubtitleRegion struct {
	X          int     `json:"x"`
	Y          int     `json:"y"`
	Width      int     `json:"width"`
	Height     int     `json:"height"`
	Confidence float64 `json:"confidence"`
}

// DefaultSubtitleRegion returns the fallback subtitle area when OCR finds nothing.
// Placed at 70% from top — the most common subtitle position for TikTok/Douyin 9:16 videos.
func DefaultSubtitleRegion(videoWidth, videoHeight int) SubtitleRegion {
	padding := videoWidth * 5 / 100
	y := videoHeight * 70 / 100
	h := videoHeight * 10 / 100
	return SubtitleRegion{
		X:          padding,
		Y:          y,
		Width:      videoWidth - (2 * padding),
		Height:     h,
		Confidence: 0,
	}
}

// Service detects subtitle regions by calling the PaddleOCR microservice.
type Service struct {
	logger     *logrus.Logger
	serviceURL string
	httpClient *http.Client
}

// NewService creates an OCR Service. serviceURL is the base URL of the PaddleOCR
// detection service (e.g. http://ocr:8000).
func NewService(logger *logrus.Logger, serviceURL string) *Service {
	if serviceURL == "" {
		serviceURL = "http://ocr:8000"
	}
	return &Service{
		logger:     logger,
		serviceURL: serviceURL,
		httpClient: &http.Client{Timeout: 30 * time.Second},
	}
}

// ocrLine is one text line returned by the OCR service.
type ocrLine struct {
	Text string  `json:"text"`
	Conf float64 `json:"conf"`
	X    int     `json:"x"`
	Y    int     `json:"y"`
	W    int     `json:"w"`
	H    int     `json:"h"`
}

type ocrResponse struct {
	Width  int       `json:"width"`
	Height int       `json:"height"`
	Lines  []ocrLine `json:"lines"`
}

// DetectSubtitleArea sends the frame to the PaddleOCR service and picks the most
// likely subtitle line: a multi-character text line in the lower part of the
// frame. Scoring rewards character count and a bottom position while penalising
// the watermark zone (>92%). Returns the line's bounding box in frame coords.
func (s *Service) DetectSubtitleArea(ctx context.Context, framePath string, videoWidth, videoHeight int) (*SubtitleRegion, bool) {
	resp, err := s.detect(ctx, framePath)
	if err != nil {
		s.logger.WithError(err).Warn("ocr: detect request failed")
		return nil, false
	}
	if len(resp.Lines) == 0 {
		return nil, false
	}

	// Prefer the OCR service's own reported frame size for fractions.
	fh := resp.Height
	if fh <= 0 {
		fh = videoHeight
	}
	fw := resp.Width
	if fw <= 0 {
		fw = videoWidth
	}

	best := -1
	bestScore := 0.0
	for i, ln := range resp.Lines {
		runes := len([]rune(ln.Text))
		if runes < 2 || ln.Conf < 0.5 {
			continue // skip single-char noise / low-confidence reads
		}
		cy := float64(ln.Y) + float64(ln.H)/2
		yFrac := cy / float64(fh)
		if yFrac < 0.45 {
			continue // subtitles are in the lower part of the frame
		}
		posMul := yFrac
		if yFrac > 0.92 {
			posMul = 0.92 - (yFrac-0.92)*10 // steep penalty for the watermark zone
			if posMul < 0 {
				posMul = 0
			}
		}
		score := float64(runes) * (0.3 + posMul)
		if score > bestScore {
			bestScore = score
			best = i
		}
	}
	if best < 0 {
		return nil, false
	}

	ln := resp.Lines[best]
	// Hard reject: bottom 5% is almost certainly a watermark/handle.
	if (float64(ln.Y)+float64(ln.H)/2)/float64(fh) > 0.95 {
		return nil, false
	}

	// Scale box from the OCR service's frame size to the requested video size
	// (they should match, but guard against any mismatch).
	sx := float64(videoWidth) / float64(fw)
	sy := float64(videoHeight) / float64(fh)

	wPad := videoWidth * 2 / 100
	hPad := 6
	x := int(float64(ln.X)*sx) - wPad
	y := int(float64(ln.Y)*sy) - hPad
	w := int(float64(ln.W)*sx) + 2*wPad
	h := int(float64(ln.H)*sy) + 2*hPad
	if x < 0 {
		x = 0
	}
	if y < 0 {
		y = 0
	}
	if x+w > videoWidth {
		w = videoWidth - x
	}
	if y+h > videoHeight {
		h = videoHeight - y
	}
	if w <= 0 || h <= 0 {
		return nil, false
	}

	s.logger.WithFields(logrus.Fields{
		"text": ln.Text, "x": x, "y": y, "w": w, "h": h, "conf": ln.Conf,
	}).Debug("ocr: subtitle line picked")

	return &SubtitleRegion{X: x, Y: y, Width: w, Height: h, Confidence: ln.Conf}, true
}

// detect uploads the frame to the OCR service /detect endpoint.
func (s *Service) detect(ctx context.Context, framePath string) (*ocrResponse, error) {
	f, err := os.Open(framePath)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	fw, err := mw.CreateFormFile("file", "frame.jpg")
	if err != nil {
		return nil, err
	}
	if _, err := io.Copy(fw, f); err != nil {
		return nil, err
	}
	mw.Close()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.serviceURL+"/detect", &body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", mw.FormDataContentType())

	resp, err := s.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("ocr: service status %d", resp.StatusCode)
	}

	var out ocrResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("ocr: decode response: %w", err)
	}
	return &out, nil
}
