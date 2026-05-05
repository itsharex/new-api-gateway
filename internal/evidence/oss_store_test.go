package evidence

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"strings"
	"testing"
)

type mockBucket struct {
	objects map[string][]byte
	putErr  error
	getErr  error
}

func newMockBucket() *mockBucket {
	return &mockBucket{objects: make(map[string][]byte)}
}

func (m *mockBucket) put(key string, data []byte) error {
	if m.putErr != nil {
		return m.putErr
	}
	m.objects[key] = data
	return nil
}

func (m *mockBucket) get(key string) ([]byte, error) {
	if m.getErr != nil {
		return nil, m.getErr
	}
	data, ok := m.objects[key]
	if !ok {
		return nil, fmt.Errorf("object not found: %s", key)
	}
	return data, nil
}

func TestOSSStorePutReturnsOSSRef(t *testing.T) {
	bucket := newMockBucket()
	store := NewOSSStoreWithBucket("test-bucket", bucket)
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
	if obj.StorageBackend != "oss" {
		t.Fatalf("StorageBackend = %q, want oss", obj.StorageBackend)
	}
	if !strings.HasPrefix(obj.ObjectRef, "oss://test-bucket/") {
		t.Fatalf("ObjectRef = %q, want oss://test-bucket/ prefix", obj.ObjectRef)
	}
	if !strings.HasSuffix(obj.ObjectRef, "/trace_123/request_body.bin") {
		t.Fatalf("ObjectRef = %q, want trace/object suffix", obj.ObjectRef)
	}
	hash := sha256.Sum256([]byte(body))
	if obj.SHA256 != hex.EncodeToString(hash[:]) {
		t.Fatalf("SHA256 = %q, want %q", obj.SHA256, hex.EncodeToString(hash[:]))
	}
	if obj.SizeBytes != int64(len(body)) {
		t.Fatalf("SizeBytes = %d, want %d", obj.SizeBytes, len(body))
	}
	if obj.CreatedAt.IsZero() {
		t.Fatal("CreatedAt is zero")
	}
}

func TestOSSStoreGetReadsObject(t *testing.T) {
	bucket := newMockBucket()
	data := []byte(`{"model":"gpt-4.1"}`)
	bucket.objects["raw/2026/05/05/trace_456/response_body.bin"] = data
	store := NewOSSStoreWithBucket("test-bucket", bucket)

	reader, err := store.Get(context.Background(), "oss://test-bucket/raw/2026/05/05/trace_456/response_body.bin")
	if err != nil {
		t.Fatalf("Get error: %v", err)
	}
	defer reader.Close()
	read, _ := io.ReadAll(reader)
	if string(read) != string(data) {
		t.Fatalf("body = %q, want %q", string(read), string(data))
	}
}

func TestOSSStoreGetRejectsInvalidRef(t *testing.T) {
	bucket := newMockBucket()
	store := NewOSSStoreWithBucket("test-bucket", bucket)

	tests := []string{
		"",
		"file:///raw/key.bin",
		"oss://wrong-bucket/raw/key.bin",
		"oss://test-bucket/",
		"oss://test-bucket",
	}
	for _, ref := range tests {
		t.Run(ref, func(t *testing.T) {
			_, err := store.Get(context.Background(), ref)
			if err == nil {
				t.Fatal("expected error for invalid ref")
			}
		})
	}
}

func TestOSSStorePutRejectsInvalidInput(t *testing.T) {
	bucket := newMockBucket()
	store := NewOSSStoreWithBucket("test-bucket", bucket)

	tests := []struct {
		name       string
		traceID    string
		objectType string
		reader     io.Reader
	}{
		{"empty trace id", "", "request_body", strings.NewReader("body")},
		{"empty object type", "trace_123", "", strings.NewReader("body")},
		{"nil reader", "trace_123", "request_body", nil},
		{"traversal trace id", "../outside", "request_body", strings.NewReader("body")},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := store.Put(context.Background(), PutRequest{
				TraceID: tt.traceID, ObjectType: tt.objectType, Reader: tt.reader,
			})
			if err == nil {
				t.Fatal("expected validation error")
			}
		})
	}
}

func TestOSSStorePutPropagatesError(t *testing.T) {
	bucket := newMockBucket()
	bucket.putErr = fmt.Errorf("oss unavailable")
	store := NewOSSStoreWithBucket("test-bucket", bucket)

	_, err := store.Put(context.Background(), PutRequest{
		TraceID: "trace_123", ObjectType: "request_body", Reader: strings.NewReader("body"),
	})
	if err == nil {
		t.Fatal("expected oss error")
	}
}

func TestOSSStoreGetPropagatesError(t *testing.T) {
	bucket := newMockBucket()
	bucket.getErr = fmt.Errorf("oss unavailable")
	store := NewOSSStoreWithBucket("test-bucket", bucket)

	_, err := store.Get(context.Background(), "oss://test-bucket/raw/key.bin")
	if err == nil {
		t.Fatal("expected oss error")
	}
}

func TestOSSStoreChecksContext(t *testing.T) {
	bucket := newMockBucket()
	store := NewOSSStoreWithBucket("test-bucket", bucket)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := store.Put(ctx, PutRequest{
		TraceID: "trace_123", ObjectType: "request_body", Reader: strings.NewReader("body"),
	})
	if !strings.Contains(err.Error(), "context") {
		t.Fatalf("expected context error, got %v", err)
	}
}
