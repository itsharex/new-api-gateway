package evidence

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"
)

type PutRequest struct {
	TraceID     string
	ObjectType  string
	ContentType string
	Reader      io.Reader
}

type Object struct {
	ObjectRef      string
	StorageBackend string
	ContentType    string
	SizeBytes      int64
	SHA256         string
	CreatedAt      time.Time
}

type Store interface {
	Put(ctx context.Context, req PutRequest) (Object, error)
	Get(ctx context.Context, objectRef string) (io.ReadCloser, error)
}

type FilesystemStore struct {
	root string
}

func NewFilesystemStore(root string) FilesystemStore {
	return FilesystemStore{root: root}
}

func (s FilesystemStore) Put(ctx context.Context, req PutRequest) (Object, error) {
	now := time.Now().UTC()
	dir := filepath.Join(s.root, "raw", now.Format("2006"), now.Format("01"), now.Format("02"), req.TraceID)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return Object{}, err
	}
	name := fmt.Sprintf("%s.bin", req.ObjectType)
	path := filepath.Join(dir, name)
	file, err := os.Create(path)
	if err != nil {
		return Object{}, err
	}
	defer file.Close()

	hash := sha256.New()
	written, err := io.Copy(io.MultiWriter(file, hash), req.Reader)
	if err != nil {
		return Object{}, err
	}
	ref, err := filepath.Rel(s.root, path)
	if err != nil {
		return Object{}, err
	}
	return Object{
		ObjectRef:      filepath.ToSlash(ref),
		StorageBackend: "filesystem",
		ContentType:    req.ContentType,
		SizeBytes:      written,
		SHA256:         hex.EncodeToString(hash.Sum(nil)),
		CreatedAt:      now,
	}, nil
}

func (s FilesystemStore) Get(ctx context.Context, objectRef string) (io.ReadCloser, error) {
	return os.Open(filepath.Join(s.root, filepath.FromSlash(objectRef)))
}
