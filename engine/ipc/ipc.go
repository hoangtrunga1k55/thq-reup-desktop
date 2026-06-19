// Package ipc defines the newline-delimited JSON protocol the Go engine uses to
// talk to the Tauri shell. The shell writes Command lines to the engine's stdin;
// the engine writes Event lines to stdout. One JSON object per line.
package ipc

import (
	"bufio"
	"encoding/json"
	"io"
	"sync"
)

// Command is a request from the Tauri shell to the engine.
type Command struct {
	ID      string          `json:"id"`      // correlation id, echoed back on events
	Type    string          `json:"type"`    // ping | version | start_job | cancel_job | confirm_subtitle | confirm_content
	Payload json.RawMessage `json:"payload"` // type-specific body
}

// Command type constants.
const (
	CmdPing            = "ping"
	CmdVersion         = "version"
	CmdStartJob        = "start_job"
	CmdCancelJob       = "cancel_job"
	CmdConfirmSubtitle = "confirm_subtitle"
	CmdConfirmContent  = "confirm_content"
	CmdListJobs        = "list_jobs"
)

// Event is a message from the engine to the Tauri shell.
type Event struct {
	ID      string      `json:"id"`      // correlation id of the originating command
	Type    string      `json:"type"`    // ack | progress | step | log | waiting_subtitle | waiting_content | completed | failed | result | error
	Payload interface{} `json:"payload"` // type-specific body
}

// Event type constants.
const (
	EvtAck             = "ack"
	EvtProgress        = "progress"
	EvtStep            = "step"
	EvtLog             = "log"
	EvtWaitingSubtitle = "waiting_subtitle"
	EvtWaitingContent  = "waiting_content"
	EvtCompleted       = "completed"
	EvtFailed          = "failed"
	EvtResult          = "result"
	EvtError           = "error"
)

// Reader decodes Command lines from a stream (engine stdin).
type Reader struct {
	sc *bufio.Scanner
}

func NewReader(r io.Reader) *Reader {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 8*1024*1024) // allow large command payloads
	return &Reader{sc: sc}
}

// Next blocks for the next Command. Returns io.EOF when the stream closes.
func (r *Reader) Next() (*Command, error) {
	if !r.sc.Scan() {
		if err := r.sc.Err(); err != nil {
			return nil, err
		}
		return nil, io.EOF
	}
	var cmd Command
	if err := json.Unmarshal(r.sc.Bytes(), &cmd); err != nil {
		return nil, err
	}
	return &cmd, nil
}

// Writer encodes Event lines to a stream (engine stdout). Safe for concurrent use.
type Writer struct {
	mu  sync.Mutex
	enc *json.Encoder
}

func NewWriter(w io.Writer) *Writer {
	return &Writer{enc: json.NewEncoder(w)}
}

// Emit writes one Event as a single JSON line.
func (w *Writer) Emit(e Event) {
	w.mu.Lock()
	defer w.mu.Unlock()
	_ = w.enc.Encode(e) // json.Encoder appends '\n'
}

// EmitFunc is the callback the pipeline uses to report progress without
// depending on the concrete Writer.
type EmitFunc func(e Event)
