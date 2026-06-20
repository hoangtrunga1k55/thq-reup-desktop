// Package store is the engine's local persistence layer (SQLite, pure-Go driver
// so the engine cross-compiles to Windows without CGO). It keeps the desktop
// app's job history independent of the backend (MVP — see PLAN.md §1).
package store

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"time"

	_ "modernc.org/sqlite"
)

// Job is a single reup job row.
type Job struct {
	ID          string    `json:"id"`
	SourceURL   string    `json:"source_url"`
	Status      string    `json:"status"` // queued | processing | waiting_subtitle | waiting_content | completed | failed
	CurrentStep string    `json:"current_step"`
	Progress    int       `json:"progress"`
	Title       string    `json:"title"`
	OutputPath  string    `json:"output_path"`
	Error       string    `json:"error"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

// Store wraps the SQLite database.
type Store struct {
	db *sql.DB
}

// Open creates/opens reup.db inside dataDir and applies the schema.
func Open(dataDir string) (*Store, error) {
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		return nil, fmt.Errorf("store: mkdir data dir: %w", err)
	}
	dbPath := filepath.Join(dataDir, "reup.db")
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return nil, fmt.Errorf("store: open sqlite: %w", err)
	}
	s := &Store{db: db}
	if err := s.migrate(); err != nil {
		_ = db.Close()
		return nil, err
	}
	return s, nil
}

func (s *Store) migrate() error {
	const schema = `
CREATE TABLE IF NOT EXISTS jobs (
	id           TEXT PRIMARY KEY,
	source_url   TEXT NOT NULL,
	status       TEXT NOT NULL DEFAULT 'queued',
	current_step TEXT NOT NULL DEFAULT '',
	progress     INTEGER NOT NULL DEFAULT 0,
	title        TEXT NOT NULL DEFAULT '',
	output_path  TEXT NOT NULL DEFAULT '',
	error        TEXT NOT NULL DEFAULT '',
	created_at   DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
	updated_at   DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);`
	if _, err := s.db.Exec(schema); err != nil {
		return fmt.Errorf("store: migrate: %w", err)
	}
	return nil
}

// Close releases the database handle.
func (s *Store) Close() error { return s.db.Close() }

// CreateJob inserts a new queued job.
func (s *Store) CreateJob(id, sourceURL string) error {
	_, err := s.db.Exec(
		`INSERT INTO jobs (id, source_url, status) VALUES (?, ?, 'queued')`,
		id, sourceURL)
	if err != nil {
		return fmt.Errorf("store: create job: %w", err)
	}
	return nil
}

// UpdateStatus updates the live status/step/progress of a job.
func (s *Store) UpdateStatus(id, status, step string, progress int) error {
	_, err := s.db.Exec(
		`UPDATE jobs SET status=?, current_step=?, progress=?, updated_at=CURRENT_TIMESTAMP WHERE id=?`,
		status, step, progress, id)
	return err
}

// Complete marks a job finished and records the output path/title.
func (s *Store) Complete(id, title, outputPath string) error {
	_, err := s.db.Exec(
		`UPDATE jobs SET status='completed', progress=100, title=?, output_path=?, updated_at=CURRENT_TIMESTAMP WHERE id=?`,
		title, outputPath, id)
	return err
}

// Fail marks a job failed with an error message.
func (s *Store) Fail(id, step, errMsg string) error {
	_, err := s.db.Exec(
		`UPDATE jobs SET status='failed', current_step=?, error=?, updated_at=CURRENT_TIMESTAMP WHERE id=?`,
		step, errMsg, id)
	return err
}

// ListJobs returns jobs newest-first, capped at limit.
func (s *Store) ListJobs(limit int) ([]Job, error) {
	if limit <= 0 {
		limit = 100
	}
	rows, err := s.db.Query(
		`SELECT id, source_url, status, current_step, progress, title, output_path, error, created_at, updated_at
		 FROM jobs ORDER BY created_at DESC LIMIT ?`, limit)
	if err != nil {
		return nil, fmt.Errorf("store: list jobs: %w", err)
	}
	defer rows.Close()

	var out []Job
	for rows.Next() {
		var j Job
		if err := rows.Scan(&j.ID, &j.SourceURL, &j.Status, &j.CurrentStep, &j.Progress,
			&j.Title, &j.OutputPath, &j.Error, &j.CreatedAt, &j.UpdatedAt); err != nil {
			return nil, fmt.Errorf("store: scan job: %w", err)
		}
		out = append(out, j)
	}
	return out, rows.Err()
}

// DeleteJob removes a job from the local SQLite history.
func (s *Store) DeleteJob(id string) error {
	_, err := s.db.Exec(`DELETE FROM jobs WHERE id=?`, id)
	if err != nil {
		return fmt.Errorf("store: delete job: %w", err)
	}
	return nil
}
