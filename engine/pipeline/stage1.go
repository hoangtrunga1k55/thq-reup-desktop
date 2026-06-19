package pipeline

import (
	"context"
	"fmt"
	"hash/fnv"
	"math/rand"
	"os"
	"path/filepath"

	"github.com/thq-solution/auto-reup-studio-desktop/engine/ocr"
)

const maxOCRFrames = 10

// stage1 downloads the source video, normalizes it to 1080×1920, then detects
// the original subtitle band by OCR-ing up to 10 frames and voting on the
// vertical band that recurs most. Ported from process_job.go ProcessTask.
func (o *Orchestrator) stage1(ctx context.Context, pr *progress, st *jobState) error {
	if st.params.Keys.THQ == "" {
		return fmt.Errorf("THQ Solution API key chưa được cấu hình. Vào mục API Keys để thêm")
	}
	if err := os.MkdirAll(st.jobDir, 0o755); err != nil {
		return fmt.Errorf("create job dir: %w", err)
	}

	// ── Download ─────────────────────────────────────────────────────────────
	pr.step("download", 5, "Bắt đầu tải video…")
	info, err := o.thq.ParseAndDownload(ctx, st.params.Keys.THQ, st.params.SourceURL)
	if err != nil {
		return fmt.Errorf("download video: %w", err)
	}
	st.videoInfo = info

	if err := downloadURLToFile(ctx, info.DownloadURL, st.videoPath, maxSourceVideoBytes); err != nil {
		return fmt.Errorf("download video file: %w", err)
	}
	// Normalize to 1080×1920 so OCR + render work on consistent dimensions.
	if err := o.ffmpeg.NormalizeToFullHD(ctx, st.videoPath); err != nil {
		return fmt.Errorf("normalize video: %w", err)
	}
	pr.progress("download", 15, "Video đã tải xong")

	// ── OCR subtitle detection ────────────────────────────────────────────────
	pr.step("detect_subtitle", 20, "Đang phát hiện vùng subtitle…")

	dur := float64(info.Duration)
	validDur := dur * 0.90 // sample 5%–95% to skip intros/outros
	startOffset := dur * 0.05

	rng := rand.New(rand.NewSource(seedFromID(st.params.JobID)))
	vwActual, vhActual := st.vw, st.vh
	totalFramesScanned := 0

	type ocrCandidate struct {
		region    *ocr.SubtitleRegion
		frameName string
	}
	var candidates []ocrCandidate

	for i := 0; i < maxOCRFrames; i++ {
		ts := startOffset + rng.Float64()*validDur
		frameName := fmt.Sprintf("frame_%d.jpg", i+1)
		framePath := filepath.Join(st.jobDir, frameName)
		if err := o.ffmpeg.ExtractFrame(ctx, st.videoPath, ts, framePath); err != nil {
			continue
		}
		totalFramesScanned++

		// Read actual frame dimensions from the first frame.
		if totalFramesScanned == 1 {
			if fw, fh, ferr := imageSize(framePath); ferr == nil && fw > 0 && fh > 0 {
				vwActual, vhActual = fw, fh
			}
		}

		region, ok := o.ocr.DetectSubtitleArea(ctx, framePath, vwActual, vhActual)
		if ok {
			candidates = append(candidates, ocrCandidate{region: region, frameName: frameName})
		}
	}
	st.vw, st.vh = vwActual, vhActual

	// Cluster candidates by vertical centre; pick the band with the most votes
	// (ties → the lower/bottom-most band). The representative detection (centre
	// closest to the cluster mean) gives the region + preview frame.
	var foundRegion *ocr.SubtitleRegion
	var foundFrameName string
	if len(candidates) > 0 {
		bandTol := vhActual * 6 / 100
		used := make([]bool, len(candidates))
		bestCluster := []int{}
		bestCY := 0
		for i := range candidates {
			if used[i] {
				continue
			}
			ci := candidates[i].region.Y + candidates[i].region.Height/2
			cluster := []int{i}
			used[i] = true
			for j := i + 1; j < len(candidates); j++ {
				if used[j] {
					continue
				}
				cj := candidates[j].region.Y + candidates[j].region.Height/2
				if cj-ci <= bandTol && ci-cj <= bandTol {
					cluster = append(cluster, j)
					used[j] = true
				}
			}
			sumCY := 0
			for _, k := range cluster {
				sumCY += candidates[k].region.Y + candidates[k].region.Height/2
			}
			meanCY := sumCY / len(cluster)
			if len(cluster) > len(bestCluster) || (len(cluster) == len(bestCluster) && meanCY > bestCY) {
				bestCluster = cluster
				bestCY = meanCY
			}
		}
		repIdx := bestCluster[0]
		bestDist := 1 << 30
		for _, k := range bestCluster {
			cy := candidates[k].region.Y + candidates[k].region.Height/2
			d := cy - bestCY
			if d < 0 {
				d = -d
			}
			if d < bestDist {
				bestDist = d
				repIdx = k
			}
		}
		foundRegion = candidates[repIdx].region
		foundFrameName = candidates[repIdx].frameName
	}

	// Preview frame: the subtitle frame, or 30% into the video as fallback.
	bestFrameName := "frame_1.jpg"
	if foundFrameName != "" {
		bestFrameName = foundFrameName
	} else if dur > 0 {
		safePath := filepath.Join(st.jobDir, "frame_preview.jpg")
		if err := o.ffmpeg.ExtractFrame(ctx, st.videoPath, dur*0.30, safePath); err == nil {
			bestFrameName = "frame_preview.jpg"
		}
	}
	st.previewFrame = filepath.Join(st.jobDir, bestFrameName)

	// Region: detected, or default at 70% from top.
	st.region = foundRegion
	if st.region == nil {
		def := ocr.DefaultSubtitleRegion(vwActual, vhActual)
		st.region = &def
	}

	if foundRegion != nil {
		pr.progress("detect_subtitle", 25, "Đã xác định vùng subtitle")
	} else {
		pr.progress("detect_subtitle", 25,
			fmt.Sprintf("Không phát hiện subtitle trong %d frame, dùng vị trí mặc định", totalFramesScanned))
	}
	return nil
}

// seedFromID derives a deterministic rand seed from the (string) job id, mirroring
// the server's per-job seeding so frame sampling is reproducible per job.
func seedFromID(id string) int64 {
	h := fnv.New64a()
	_, _ = h.Write([]byte(id))
	return int64(h.Sum64())
}
