package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"time"
)

var ErrSpoolDirRequired = errors.New("gateway spool dir is empty")

type Spool interface {
	Write(ctx context.Context, envelope SpoolEnvelope) error
}

type SpoolEnvelope struct {
	TraceID     string    `json:"trace_id"`
	Method      string    `json:"method"`
	Path        string    `json:"path"`
	Reason      string    `json:"reason"`
	ErrorType   string    `json:"error_type"`
	CapturedAt  time.Time `json:"captured_at"`
	RequestSize int64     `json:"request_size"`
}

type FilesystemSpool struct {
	dir string
	now func() time.Time
}

func NewFilesystemSpool(dir string) FilesystemSpool {
	return FilesystemSpool{dir: dir}
}

func (s FilesystemSpool) Write(ctx context.Context, envelope SpoolEnvelope) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if s.dir == "" {
		return ErrSpoolDirRequired
	}
	if envelope.CapturedAt.IsZero() {
		if s.now != nil {
			envelope.CapturedAt = s.now().UTC()
		} else {
			envelope.CapturedAt = time.Now().UTC()
		}
	}
	if err := os.MkdirAll(s.dir, 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(envelope, "", "  ")
	if err != nil {
		return err
	}
	path := filepath.Join(s.dir, envelope.TraceID+".json")
	return os.WriteFile(path, data, 0o600)
}
