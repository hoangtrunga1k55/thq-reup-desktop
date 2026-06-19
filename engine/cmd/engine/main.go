// Command engine is the local pipeline engine sidecar for the Auto ReUp Studio
// desktop app. It is spawned by the Tauri shell and communicates over
// stdin/stdout using the newline-delimited JSON protocol in package ipc.
//
// Phase 0: wires the IPC loop, the SQLite store, and the orchestrator skeleton.
// The full 3-stage pipeline is filled in during Phase 1 (see package pipeline).
package main

import (
	"context"
	"errors"
	"flag"
	"io"
	"os"

	"github.com/sirupsen/logrus"

	"github.com/thq-solution/auto-reup-studio-desktop/engine/ipc"
	"github.com/thq-solution/auto-reup-studio-desktop/engine/pipeline"
	"github.com/thq-solution/auto-reup-studio-desktop/engine/store"
)

// version is overridden at build time via -ldflags "-X main.version=...".
var version = "0.0.0-dev"

func main() {
	dataDir := flag.String("data-dir", ".", "directory for the local SQLite db and temp job files")
	ocrURL := flag.String("ocr-url", "http://127.0.0.1:8000", "base URL of the local OCR sidecar")
	ffmpegPath := flag.String("ffmpeg", "ffmpeg", "path to the bundled ffmpeg binary")
	ffprobePath := flag.String("ffprobe", "ffprobe", "path to the bundled ffprobe binary")
	fontsDir := flag.String("fonts-dir", "", "dir with bundled fonts for drawtext overlays")
	flag.Parse()

	// Logs go to stderr so they never corrupt the stdout JSON event stream.
	logger := logrus.New()
	logger.SetOutput(os.Stderr)
	logger.SetLevel(logrus.InfoLevel)

	st, err := store.Open(*dataDir)
	if err != nil {
		logger.WithError(err).Fatal("engine: open store")
	}
	defer st.Close()

	out := ipc.NewWriter(os.Stdout)
	orch := pipeline.NewOrchestrator(pipeline.Deps{
		Logger:      logger,
		Store:       st,
		OCRURL:      *ocrURL,
		FFmpegPath:  *ffmpegPath,
		FFprobePath: *ffprobePath,
		FontsDir:    *fontsDir,
		DataDir:     *dataDir,
	})

	logger.WithField("version", version).Info("engine: started")
	run(context.Background(), logger, ipc.NewReader(os.Stdin), out, orch)
}

// run is the main command loop. Each command is dispatched to the orchestrator.
func run(ctx context.Context, logger *logrus.Logger, r *ipc.Reader, out *ipc.Writer, orch *pipeline.Orchestrator) {
	emit := func(e ipc.Event) { out.Emit(e) }

	for {
		cmd, err := r.Next()
		if errors.Is(err, io.EOF) {
			logger.Info("engine: stdin closed, shutting down")
			return
		}
		if err != nil {
			logger.WithError(err).Warn("engine: failed to decode command")
			continue
		}

		switch cmd.Type {
		case ipc.CmdPing:
			emit(ipc.Event{ID: cmd.ID, Type: ipc.EvtAck, Payload: map[string]string{"pong": "ok"}})

		case ipc.CmdVersion:
			emit(ipc.Event{ID: cmd.ID, Type: ipc.EvtResult, Payload: map[string]string{"version": version}})

		case ipc.CmdStartJob:
			// Each job runs in its own goroutine so the loop stays responsive
			// (cancel, status, future concurrent jobs).
			go orch.StartJob(ctx, cmd.ID, cmd.Payload, emit)

		case ipc.CmdCancelJob:
			orch.Cancel(cmd.ID, cmd.Payload)

		case ipc.CmdConfirmSubtitle:
			orch.ConfirmSubtitle(cmd.ID, cmd.Payload)

		case ipc.CmdConfirmContent:
			orch.ConfirmContent(cmd.ID, cmd.Payload)

		case ipc.CmdListJobs:
			orch.ListJobs(cmd.ID, emit)

		default:
			emit(ipc.Event{ID: cmd.ID, Type: ipc.EvtError, Payload: map[string]string{"error": "unknown command type: " + cmd.Type}})
		}
	}
}
