package evidence

import (
	"bytes"
	"context"
	"io"
	"testing"
)

func TestFilesystemStoreWritesObjectWithHash(t *testing.T) {
	store := NewFilesystemStore(t.TempDir())
	obj, err := store.Put(context.Background(), PutRequest{
		TraceID:     "trace_123",
		ObjectType:  "request_body",
		ContentType: "application/json",
		Reader:      bytes.NewBufferString(`{"ok":true}`),
	})
	if err != nil {
		t.Fatalf("Put error: %v", err)
	}
	if obj.SizeBytes == 0 || obj.SHA256 == "" || obj.ObjectRef == "" {
		t.Fatalf("invalid object metadata %#v", obj)
	}

	reader, err := store.Get(context.Background(), obj.ObjectRef)
	if err != nil {
		t.Fatalf("Get error: %v", err)
	}
	defer reader.Close()
	body, _ := io.ReadAll(reader)
	if string(body) != `{"ok":true}` {
		t.Fatalf("body = %q", string(body))
	}
}
