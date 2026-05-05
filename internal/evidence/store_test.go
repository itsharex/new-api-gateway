package evidence

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestFilesystemStoreWritesObjectWithHash(t *testing.T) {
	store := NewFilesystemStore(t.TempDir())
	body := `{"ok":true}`
	obj, err := store.Put(context.Background(), PutRequest{
		TraceID:     "trace_123",
		ObjectType:  "request_body",
		ContentType: "application/json",
		Reader:      bytes.NewBufferString(body),
	})
	if err != nil {
		t.Fatalf("Put error: %v", err)
	}
	hash := sha256.Sum256([]byte(body))
	if obj.SizeBytes != int64(len(body)) {
		t.Fatalf("SizeBytes = %d, want %d", obj.SizeBytes, len(body))
	}
	if obj.SHA256 != hex.EncodeToString(hash[:]) {
		t.Fatalf("SHA256 = %q, want %q", obj.SHA256, hex.EncodeToString(hash[:]))
	}
	if obj.StorageBackend != "filesystem" {
		t.Fatalf("StorageBackend = %q, want filesystem", obj.StorageBackend)
	}
	if obj.ContentType != "application/json" {
		t.Fatalf("ContentType = %q, want application/json", obj.ContentType)
	}
	if obj.CreatedAt.IsZero() {
		t.Fatal("CreatedAt is zero")
	}
	if obj.CreatedAt.Location() != time.UTC {
		t.Fatalf("CreatedAt location = %v, want UTC", obj.CreatedAt.Location())
	}
	if !strings.HasPrefix(obj.ObjectRef, "file:///raw/") || !strings.HasSuffix(obj.ObjectRef, "/trace_123/request_body.bin") {
		t.Fatalf("ObjectRef = %q, want file:///raw date prefix and trace/object suffix", obj.ObjectRef)
	}
	if filepath.IsAbs(obj.ObjectRef) || strings.Contains(obj.ObjectRef, "..") || strings.Contains(obj.ObjectRef, "\\") {
		t.Fatalf("ObjectRef is not a safe slash-relative ref: %q", obj.ObjectRef)
	}

	reader, err := store.Get(context.Background(), obj.ObjectRef)
	if err != nil {
		t.Fatalf("Get error: %v", err)
	}
	defer reader.Close()
	readBody, _ := io.ReadAll(reader)
	if string(readBody) != `{"ok":true}` {
		t.Fatalf("body = %q", string(readBody))
	}
}

func TestFilesystemStoreRejectsMaliciousPutPathParts(t *testing.T) {
	store := NewFilesystemStore(t.TempDir())
	tests := []struct {
		name       string
		traceID    string
		objectType string
	}{
		{name: "empty trace id", traceID: "", objectType: "request_body"},
		{name: "absolute trace id", traceID: "/tmp/trace", objectType: "request_body"},
		{name: "trace id slash", traceID: "trace/123", objectType: "request_body"},
		{name: "trace id backslash", traceID: `trace\123`, objectType: "request_body"},
		{name: "trace id dot", traceID: ".", objectType: "request_body"},
		{name: "trace id dot dot", traceID: "..", objectType: "request_body"},
		{name: "trace id traversal", traceID: "../outside", objectType: "request_body"},
		{name: "empty object type", traceID: "trace_123", objectType: ""},
		{name: "absolute object type", traceID: "trace_123", objectType: "/tmp/request_body"},
		{name: "object type slash", traceID: "trace_123", objectType: "request/body"},
		{name: "object type backslash", traceID: "trace_123", objectType: `request\body`},
		{name: "object type dot", traceID: "trace_123", objectType: "."},
		{name: "object type dot dot", traceID: "trace_123", objectType: ".."},
		{name: "object type traversal", traceID: "trace_123", objectType: "../outside"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := store.Put(context.Background(), PutRequest{
				TraceID:     tt.traceID,
				ObjectType:  tt.objectType,
				ContentType: "application/json",
				Reader:      bytes.NewBufferString(`{"ok":true}`),
			})
			if err == nil {
				t.Fatal("Put error = nil, want validation error")
			}
		})
	}
}

func TestFilesystemStoreRejectsMaliciousObjectRefs(t *testing.T) {
	store := NewFilesystemStore(t.TempDir())
	tests := []string{
		"",
		"file:///raw/2026/01/01/trace/request.bin",
		"file:///../outside",
		"file:///raw/../../outside",
		"file:///raw/2026/01/01/trace/../../outside",
		"file:///.",
		"file:///..",
		"file:///raw//2026/object.bin",
		"file:///" + `raw\2026\01\01\trace\request.bin`,
		"oss://bucket/raw/key.bin",
	}

	for _, ref := range tests {
		t.Run(ref, func(t *testing.T) {
			reader, err := store.Get(context.Background(), ref)
			if err == nil {
				reader.Close()
				t.Fatal("Get error = nil, want validation error")
			}
		})
	}
}

func TestFilesystemStorePutCleansUpPartialWriteOnReaderError(t *testing.T) {
	root := t.TempDir()
	store := NewFilesystemStore(root)
	writeErr := errors.New("reader failed")

	_, err := store.Put(context.Background(), PutRequest{
		TraceID:     "trace_123",
		ObjectType:  "request_body",
		ContentType: "application/json",
		Reader:      &errorAfterReader{data: []byte(`{"partial":`), err: writeErr},
	})
	if !errors.Is(err, writeErr) {
		t.Fatalf("Put error = %v, want %v", err, writeErr)
	}

	finalPath := filepath.Join(root, "raw")
	err = filepath.WalkDir(finalPath, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if !d.IsDir() {
			t.Fatalf("object file remains after failed Put: %s", path)
		}
		return nil
	})
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("WalkDir error: %v", err)
	}
}

func TestFilesystemStoreRejectsEmptyRoot(t *testing.T) {
	store := NewFilesystemStore("")
	_, err := store.Put(context.Background(), PutRequest{
		TraceID:    "trace_123",
		ObjectType: "request_body",
		Reader:     bytes.NewBufferString("body"),
	})
	if err == nil {
		t.Fatal("Put error = nil, want empty root error")
	}
	reader, err := store.Get(context.Background(), "file:///raw/2026/01/01/trace_123/request_body.bin")
	if err == nil {
		reader.Close()
		t.Fatal("Get error = nil, want empty root error")
	}
}

func TestFilesystemStoreRejectsNilReader(t *testing.T) {
	store := NewFilesystemStore(t.TempDir())
	_, err := store.Put(context.Background(), PutRequest{
		TraceID:    "trace_123",
		ObjectType: "request_body",
	})
	if err == nil {
		t.Fatal("Put error = nil, want nil reader error")
	}
}

func TestFilesystemStoreChecksContextBeforePutAndGet(t *testing.T) {
	store := NewFilesystemStore(t.TempDir())
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := store.Put(ctx, PutRequest{
		TraceID:    "trace_123",
		ObjectType: "request_body",
		Reader:     bytes.NewBufferString("body"),
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Put error = %v, want context.Canceled", err)
	}
	reader, err := store.Get(ctx, "file:///raw/2026/01/01/trace_123/request_body.bin")
	if err == nil {
		reader.Close()
		t.Fatal("Get error = nil, want context.Canceled")
	}
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Get error = %v, want context.Canceled", err)
	}
}

type errorAfterReader struct {
	data []byte
	err  error
	done bool
}

func (r *errorAfterReader) Read(p []byte) (int, error) {
	if r.done {
		return 0, r.err
	}
	r.done = true
	return copy(p, r.data), r.err
}
