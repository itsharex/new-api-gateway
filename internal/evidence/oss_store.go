package evidence

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"strings"
	"time"
)

type ossBucketClient interface {
	put(key string, data []byte) error
	get(key string) ([]byte, error)
}

type OSSStore struct {
	bucketName string
	client     ossBucketClient
}

func NewOSSStoreWithBucket(bucketName string, client ossBucketClient) OSSStore {
	return OSSStore{bucketName: bucketName, client: client}
}

func (s OSSStore) Put(ctx context.Context, req PutRequest) (Object, error) {
	if err := ctx.Err(); err != nil {
		return Object{}, fmt.Errorf("oss put: %w", err)
	}
	if err := validatePathPart("trace id", req.TraceID); err != nil {
		return Object{}, err
	}
	if err := validatePathPart("object type", req.ObjectType); err != nil {
		return Object{}, err
	}
	if req.Reader == nil {
		return Object{}, errEvidenceReaderRequired
	}

	now := time.Now().UTC()
	key := fmt.Sprintf("raw/%s/%s/%s/%s/%s.bin",
		now.Format("2006"), now.Format("01"), now.Format("02"),
		req.TraceID, req.ObjectType)

	var buf bytes.Buffer
	hash := sha256.New()
	written, err := io.Copy(io.MultiWriter(&buf, hash), req.Reader)
	if err != nil {
		return Object{}, fmt.Errorf("oss put %s: read failed: %w", key, err)
	}

	if err := s.client.put(key, buf.Bytes()); err != nil {
		return Object{}, fmt.Errorf("oss put %s: %w", key, err)
	}

	return Object{
		ObjectRef:      "oss://" + s.bucketName + "/" + key,
		StorageBackend: "oss",
		ContentType:    req.ContentType,
		SizeBytes:      written,
		SHA256:         hex.EncodeToString(hash.Sum(nil)),
		CreatedAt:      now,
	}, nil
}

func (s OSSStore) Get(ctx context.Context, objectRef string) (io.ReadCloser, error) {
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("oss get: %w", err)
	}
	prefix := "oss://" + s.bucketName + "/"
	if !strings.HasPrefix(objectRef, prefix) {
		return nil, fmt.Errorf("invalid object ref %q: must start with %s", objectRef, prefix)
	}
	key := strings.TrimPrefix(objectRef, prefix)
	if key == "" {
		return nil, fmt.Errorf("invalid object ref %q: empty key", objectRef)
	}

	data, err := s.client.get(key)
	if err != nil {
		return nil, fmt.Errorf("oss get %s: %w", key, err)
	}
	return io.NopCloser(bytes.NewReader(data)), nil
}
