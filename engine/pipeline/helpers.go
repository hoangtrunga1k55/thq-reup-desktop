package pipeline

import (
	"context"
	"encoding/base64"
	"fmt"
	"image"
	_ "image/jpeg" // register JPEG decoder for imageSize
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

const maxSourceVideoBytes = 512 << 20 // 512 MiB safety cap per source video.

// downloadURLToFile streams a URL to disk without buffering the full response.
func downloadURLToFile(ctx context.Context, url, dst string, maxBytes int64) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	client := &http.Client{Timeout: 5 * time.Minute}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("download HTTP %d", resp.StatusCode)
	}
	if resp.ContentLength > maxBytes {
		return fmt.Errorf("download too large: %d bytes exceeds %d", resp.ContentLength, maxBytes)
	}

	tmp := dst + ".download"
	out, err := os.Create(tmp)
	if err != nil {
		return err
	}
	written, copyErr := io.Copy(out, io.LimitReader(resp.Body, maxBytes+1))
	closeErr := out.Close()
	if copyErr != nil {
		_ = os.Remove(tmp)
		return copyErr
	}
	if closeErr != nil {
		_ = os.Remove(tmp)
		return closeErr
	}
	if written > maxBytes {
		_ = os.Remove(tmp)
		return fmt.Errorf("download too large: exceeded %d bytes", maxBytes)
	}
	return os.Rename(tmp, dst)
}

// downloadBrandImage fetches a brand image from a URL, or decodes it from an
// inline "data:...;base64,..." URL (how the backend ships the user's local logo
// to the desktop, which can't reach the server-side file).
func downloadBrandImage(ctx context.Context, url string) ([]byte, error) {
	if strings.HasPrefix(url, "data:") {
		i := strings.Index(url, ",")
		if i < 0 {
			return nil, fmt.Errorf("invalid data URL for brand image")
		}
		meta, payload := url[:i], url[i+1:]
		if strings.Contains(meta, ";base64") {
			return base64.StdEncoding.DecodeString(payload)
		}
		return []byte(payload), nil
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("download brand image HTTP %d", resp.StatusCode)
	}
	return io.ReadAll(resp.Body)
}

// subtitleFontForLanguage returns the appropriate font family for the target
// language, falling back to defaultFont for Latin-script languages.
func subtitleFontForLanguage(lang, defaultFont string) string {
	switch lang {
	case "Korean":
		return "Noto Sans CJK KR"
	case "Chinese":
		return "Noto Sans CJK SC"
	case "Japanese":
		return "Noto Sans CJK JP"
	case "Thai":
		return "Noto Sans Thai"
	case "Arabic":
		return "Noto Sans Arabic"
	default:
		if defaultFont != "" {
			return defaultFont
		}
		return "Noto Sans"
	}
}

// imageSize returns the pixel dimensions of an image without fully decoding it.
func imageSize(path string) (w, h int, err error) {
	f, err := os.Open(path)
	if err != nil {
		return 0, 0, err
	}
	defer f.Close()
	cfg, _, err := image.DecodeConfig(f)
	if err != nil {
		return 0, 0, err
	}
	return cfg.Width, cfg.Height, nil
}
