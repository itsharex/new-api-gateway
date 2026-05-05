//go:build integration

package evidence

import (
	"bytes"
	"context"
	"io"
	"os"
	"strings"
	"testing"
)

func TestOSSStoreIntegrationPutAndGet(t *testing.T) {
	endpoint := os.Getenv("OSS_ENDPOINT")
	bucketName := os.Getenv("OSS_BUCKET")
	accessKeyID := os.Getenv("OSS_ACCESS_KEY_ID")
	accessKeySecret := os.Getenv("OSS_ACCESS_KEY_SECRET")
	for _, v := range []string{endpoint, bucketName, accessKeyID, accessKeySecret} {
		if v == "" {
			t.Skip("OSS environment variables not set")
		}
	}

	store, err := NewOSSStore(endpoint, bucketName, accessKeyID, accessKeySecret)
	if err != nil {
		t.Fatalf("NewOSSStore: %v", err)
	}

	body := `{"integration":"test"}`
	obj, err := store.Put(context.Background(), PutRequest{
		TraceID:     "trace_integration_test",
		ObjectType:  "request_body",
		ContentType: "application/json",
		Reader:      bytes.NewBufferString(body),
	})
	if err != nil {
		t.Fatalf("Put: %v", err)
	}
	if obj.StorageBackend != "oss" {
		t.Fatalf("StorageBackend = %q, want oss", obj.StorageBackend)
	}
	if !strings.HasPrefix(obj.ObjectRef, "oss://"+bucketName+"/") {
		t.Fatalf("ObjectRef = %q, want oss://%s/ prefix", obj.ObjectRef, bucketName)
	}
	t.Logf("ObjectRef: %s", obj.ObjectRef)

	reader, err := store.Get(context.Background(), obj.ObjectRef)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	defer reader.Close()
	read, _ := io.ReadAll(reader)
	if string(read) != body {
		t.Fatalf("body = %q, want %q", string(read), body)
	}
}
