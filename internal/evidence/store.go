package evidence

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

var (
	errFilesystemRootRequired = errors.New("evidence filesystem root is empty")
	errEvidenceReaderRequired = errors.New("evidence reader is nil")
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
	if err := ctx.Err(); err != nil {
		storeOpsTotal.WithLabelValues("filesystem", "put", "error").Inc()
		return Object{}, err
	}
	root, err := absoluteRoot(s.root)
	if err != nil {
		storeOpsTotal.WithLabelValues("filesystem", "put", "error").Inc()
		return Object{}, err
	}
	if err := validatePathPart("trace id", req.TraceID); err != nil {
		storeOpsTotal.WithLabelValues("filesystem", "put", "error").Inc()
		return Object{}, err
	}
	if err := validatePathPart("object type", req.ObjectType); err != nil {
		storeOpsTotal.WithLabelValues("filesystem", "put", "error").Inc()
		return Object{}, err
	}
	if req.Reader == nil {
		storeOpsTotal.WithLabelValues("filesystem", "put", "error").Inc()
		return Object{}, errEvidenceReaderRequired
	}

	now := time.Now().UTC()
	dir := filepath.Join(root, "raw", now.Format("2006"), now.Format("01"), now.Format("02"), req.TraceID)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		storeOpsTotal.WithLabelValues("filesystem", "put", "error").Inc()
		return Object{}, err
	}
	name := fmt.Sprintf("%s.bin", req.ObjectType)
	path := filepath.Join(dir, name)
	if err := ensureWithinRoot(root, path); err != nil {
		storeOpsTotal.WithLabelValues("filesystem", "put", "error").Inc()
		return Object{}, err
	}
	file, err := os.CreateTemp(dir, "."+name+".tmp-*")
	if err != nil {
		storeOpsTotal.WithLabelValues("filesystem", "put", "error").Inc()
		return Object{}, err
	}
	tempPath := file.Name()
	removeTemp := true
	defer func() {
		if removeTemp {
			_ = os.Remove(tempPath)
		}
	}()

	hash := sha256.New()
	written, err := io.Copy(io.MultiWriter(file, hash), req.Reader)
	if err != nil {
		_ = file.Close()
		storeOpsTotal.WithLabelValues("filesystem", "put", "error").Inc()
		return Object{}, err
	}
	if err := file.Close(); err != nil {
		storeOpsTotal.WithLabelValues("filesystem", "put", "error").Inc()
		return Object{}, err
	}
	if err := os.Rename(tempPath, path); err != nil {
		storeOpsTotal.WithLabelValues("filesystem", "put", "error").Inc()
		return Object{}, err
	}
	removeTemp = false

	ref, err := filepath.Rel(root, path)
	if err != nil {
		storeOpsTotal.WithLabelValues("filesystem", "put", "error").Inc()
		return Object{}, err
	}
	storeOpsTotal.WithLabelValues("filesystem", "put", "success").Inc()
	return Object{
		ObjectRef:      "file:///" + filepath.ToSlash(ref),
		StorageBackend: "filesystem",
		ContentType:    req.ContentType,
		SizeBytes:      written,
		SHA256:         hex.EncodeToString(hash.Sum(nil)),
		CreatedAt:      now,
	}, nil
}

func (s FilesystemStore) Get(ctx context.Context, objectRef string) (io.ReadCloser, error) {
	if err := ctx.Err(); err != nil {
		storeOpsTotal.WithLabelValues("filesystem", "get", "error").Inc()
		return nil, err
	}
	root, err := absoluteRoot(s.root)
	if err != nil {
		storeOpsTotal.WithLabelValues("filesystem", "get", "error").Inc()
		return nil, err
	}
	if !strings.HasPrefix(objectRef, "file:///") {
		storeOpsTotal.WithLabelValues("filesystem", "get", "error").Inc()
		return nil, fmt.Errorf("invalid object ref %q: must start with file:///", objectRef)
	}
	refPath, err := validateObjectRef(strings.TrimPrefix(objectRef, "file:///"))
	if err != nil {
		storeOpsTotal.WithLabelValues("filesystem", "get", "error").Inc()
		return nil, err
	}
	path := filepath.Join(root, refPath)
	if err := ensureWithinRoot(root, path); err != nil {
		storeOpsTotal.WithLabelValues("filesystem", "get", "error").Inc()
		return nil, err
	}
	f, err := os.Open(path)
	if err != nil {
		storeOpsTotal.WithLabelValues("filesystem", "get", "error").Inc()
		return nil, err
	}
	storeOpsTotal.WithLabelValues("filesystem", "get", "success").Inc()
	return f, nil
}

func absoluteRoot(root string) (string, error) {
	if root == "" {
		return "", errFilesystemRootRequired
	}
	abs, err := filepath.Abs(root)
	if err != nil {
		return "", err
	}
	return filepath.Clean(abs), nil
}

func validatePathPart(name, value string) error {
	if value == "" {
		return fmt.Errorf("%s is empty", name)
	}
	if filepath.IsAbs(value) || value == "." || value == ".." {
		return fmt.Errorf("invalid %s %q", name, value)
	}
	if strings.Contains(value, "/") || strings.Contains(value, `\`) || strings.Contains(value, "..") {
		return fmt.Errorf("invalid %s %q", name, value)
	}
	return nil
}

func validateObjectRef(objectRef string) (string, error) {
	if objectRef == "" {
		return "", fmt.Errorf("object ref is empty")
	}
	if strings.Contains(objectRef, `\`) || strings.Contains(objectRef, "//") || strings.Contains(objectRef, "..") {
		return "", fmt.Errorf("invalid object ref %q", objectRef)
	}
	if strings.HasPrefix(objectRef, "/") || filepath.IsAbs(objectRef) {
		return "", fmt.Errorf("invalid object ref %q", objectRef)
	}
	parts := strings.Split(objectRef, "/")
	for _, part := range parts {
		if part == "" || part == "." || part == ".." {
			return "", fmt.Errorf("invalid object ref %q", objectRef)
		}
	}
	clean := filepath.Clean(filepath.FromSlash(objectRef))
	if clean == "." || filepath.IsAbs(clean) {
		return "", fmt.Errorf("invalid object ref %q", objectRef)
	}
	return clean, nil
}

func ensureWithinRoot(root, path string) error {
	absPath, err := filepath.Abs(path)
	if err != nil {
		return err
	}
	rel, err := filepath.Rel(root, filepath.Clean(absPath))
	if err != nil {
		return err
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
		return fmt.Errorf("path escapes evidence root")
	}
	return nil
}

// StoreConfig holds configuration for creating the appropriate evidence Store.
type StoreConfig struct {
	Backend            string
	FilesystemRoot     string
	OSSEndpoint        string
	OSSBucket          string
	OSSAccessKeyID     string
	OSSAccessKeySecret string
}

// NewStore creates the appropriate Store implementation based on Backend.
func NewStore(cfg StoreConfig) (Store, error) {
	switch cfg.Backend {
	case "filesystem":
		return NewFilesystemStore(cfg.FilesystemRoot), nil
	case "oss":
		return NewOSSStore(cfg.OSSEndpoint, cfg.OSSBucket, cfg.OSSAccessKeyID, cfg.OSSAccessKeySecret)
	default:
		return nil, fmt.Errorf("unsupported evidence storage backend: %q", cfg.Backend)
	}
}
