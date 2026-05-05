# OSS Evidence Storage Backend Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add Alibaba Cloud OSS as an alternative evidence storage backend, selectable via `EVIDENCE_STORAGE_BACKEND` env var, with unified `file:///` and `oss://` scheme-prefixed object refs.

**Architecture:** Go and Python each get a new OSS store implementation conforming to their existing interfaces (`evidence.Store` and `EvidenceStore` Protocol). A factory function selects the backend at startup based on env vars. Object refs gain scheme prefixes (`file:///` for filesystem, `oss://bucket/` for OSS). Existing proxy error handling already does not block user requests on Put failures.

**Tech Stack:** Go aliyun-oss-go-sdk, Python oss2, PostgreSQL migration for ref prefix update.

---

### Task 1: Add DB migration for object_ref scheme prefix

**Files:**
- Create: `migrations/0013_object_ref_scheme_prefix.sql`

- [ ] **Step 1: Create migration file**

```sql
-- 0013_object_ref_scheme_prefix.sql
-- Add file:/// scheme prefix to all existing filesystem object_ref values.
-- Idempotent: NOT LIKE guard prevents double-prefixing.

UPDATE raw_evidence_objects
SET object_ref = 'file:///' || object_ref
WHERE storage_backend = 'filesystem'
  AND object_ref NOT LIKE 'file:///%';
```

- [ ] **Step 2: Verify SQL syntax**

Run: `head -20 migrations/0013_object_ref_scheme_prefix.sql`
Expected: File contains the UPDATE statement with idempotent guard.

- [ ] **Step 3: Commit**

```bash
git add migrations/0013_object_ref_scheme_prefix.sql
git commit -m "feat(migration): add object_ref scheme prefix migration"
```

---

### Task 2: Go — Add OSS config fields to config package

**Files:**
- Modify: `internal/config/config.go:18-34` (Config struct)
- Modify: `internal/config/config.go:107-158` (LoadFromEnv function)

- [ ] **Step 1: Add config fields**

Add to the `Config` struct after `EvidenceStorageDir`:

```go
EvidenceStorageBackend string
OSSEndpoint            string
OSSBucket              string
OSSAccessKeyID         string
OSSAccessKeySecret     string
```

- [ ] **Step 2: Replace `requiredEnv("EVIDENCE_STORAGE_DIR")` block with backend-aware loading**

Replace the current `evidenceStorageDir` block (lines 107-110) and the config struct initialization (lines 141-157) with:

```go
evidenceStorageBackend, err := requiredEnv("EVIDENCE_STORAGE_BACKEND")
if err != nil {
    return Config{}, err
}
if evidenceStorageBackend != "filesystem" && evidenceStorageBackend != "oss" {
    return Config{}, fmt.Errorf("EVIDENCE_STORAGE_BACKEND must be filesystem or oss, got %q", evidenceStorageBackend)
}

var evidenceStorageDir string
var ossEndpoint, ossBucket, ossAccessKeyID, ossAccessKeySecret string
switch evidenceStorageBackend {
case "filesystem":
    evidenceStorageDir, err = requiredEnv("EVIDENCE_STORAGE_DIR")
    if err != nil {
        return Config{}, err
    }
case "oss":
    ossEndpoint, err = requiredEnv("OSS_ENDPOINT")
    if err != nil {
        return Config{}, err
    }
    ossBucket, err = requiredEnv("OSS_BUCKET")
    if err != nil {
        return Config{}, err
    }
    ossAccessKeyID, err = requiredEnv("OSS_ACCESS_KEY_ID")
    if err != nil {
        return Config{}, err
    }
    ossAccessKeySecret, err = requiredEnv("OSS_ACCESS_KEY_SECRET")
    if err != nil {
        return Config{}, err
    }
}
```

Add to the returned `cfg` struct:

```go
EvidenceStorageBackend: evidenceStorageBackend,
EvidenceStorageDir:     evidenceStorageDir,
OSSEndpoint:            ossEndpoint,
OSSBucket:              ossBucket,
OSSAccessKeyID:         ossAccessKeyID,
OSSAccessKeySecret:     ossAccessKeySecret,
```

- [ ] **Step 3: Run existing tests**

Run: `go test ./internal/config/...`
Expected: PASS (existing config tests should still pass since they're not evidence-specific)

- [ ] **Step 4: Commit**

```bash
git add internal/config/config.go
git commit -m "feat(config): add EVIDENCE_STORAGE_BACKEND and OSS config fields"
```

---

### Task 3: Go — Update FilesystemStore ref format to `file:///`

**Files:**
- Modify: `internal/evidence/store.go:108-110` (Put return value)
- Modify: `internal/evidence/store.go:126-130` (Get ref parsing)
- Modify: `internal/evidence/store_test.go` (all ObjectRef assertions)

- [ ] **Step 1: Update FilesystemStore.Put to return `file:///` prefixed ref**

In `store.go`, replace the ObjectRef construction (line 109):

```go
ObjectRef:      "file:///" + filepath.ToSlash(ref),
```

- [ ] **Step 2: Update FilesystemStore.Get to parse `file:///` prefix**

Replace the `validateObjectRef` call in `Get` (line 126) with:

```go
if !strings.HasPrefix(objectRef, "file:///") {
    return nil, fmt.Errorf("invalid object ref %q: must start with file:///", objectRef)
}
refPath, err := validateObjectRef(strings.TrimPrefix(objectRef, "file:///"))
if err != nil {
    return nil, err
}
```

- [ ] **Step 3: Update tests in `store_test.go`**

In `TestFilesystemStoreWritesObjectWithHash`, update the ObjectRef assertion (line 48):

```go
if !strings.HasPrefix(obj.ObjectRef, "file:///raw/") || !strings.HasSuffix(obj.ObjectRef, "/trace_123/request_body.bin") {
    t.Fatalf("ObjectRef = %q, want file:///raw date prefix and trace/object suffix", obj.ObjectRef)
}
```

In `TestFilesystemStoreRejectsMaliciousObjectRefs`, all test refs without `file:///` prefix will be rejected by the new prefix check. Update the test refs to include `file:///` prefix where they test path traversal:

```go
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
```

In `TestFilesystemStoreRejectsEmptyRoot`, update the Get ref (line 169):

```go
reader, err := store.Get(context.Background(), "file:///raw/2026/01/01/trace_123/request_body.bin")
```

In `TestFilesystemStoreChecksContextBeforePutAndGet`, update the Get ref (line 200):

```go
reader, err := store.Get(ctx, "file:///raw/2026/01/01/trace_123/request_body.bin")
```

In `TestFilesystemStoreWritesObjectWithHash`, update the Get call (line 55) — already uses `obj.ObjectRef` so no change needed.

In `TestFilesystemStorePutCleansUpPartialWriteOnReaderError`, update the `finalPath` (line 144):

```go
finalPath := filepath.Join(root, "raw")
```
No change needed — this walks the directory, not the ref.

- [ ] **Step 4: Run tests**

Run: `go test ./internal/evidence/...`
Expected: All tests pass with `file:///` prefixed refs.

- [ ] **Step 5: Commit**

```bash
git add internal/evidence/store.go internal/evidence/store_test.go
git commit -m "feat(evidence): update FilesystemStore ref format to file:/// scheme"
```

---

### Task 4: Go — Add `go.aliyun.com/oss` dependency

**Files:**
- Modify: `go.mod` / `go.sum`

- [ ] **Step 1: Install aliyun-oss-go-sdk**

Run: `go get github.com/aliyun/aliyun-oss-go-sdk/oss`

- [ ] **Step 2: Verify dependency**

Run: `grep aliyun-oss-go-sdk go.mod`
Expected: Shows the dependency line.

- [ ] **Step 3: Commit**

```bash
git add go.mod go.sum
git commit -m "chore: add aliyun-oss-go-sdk dependency"
```

---

### Task 5: Go — Implement OSSStore

**Files:**
- Create: `internal/evidence/oss_store.go`
- Create: `internal/evidence/oss_store_test.go`

- [ ] **Step 1: Write failing tests for OSSStore**

Create `internal/evidence/oss_store_test.go`:

```go
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
    "time"
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
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/evidence/... -run TestOSSStore`
Expected: Compilation fails — `NewOSSStoreWithBucket` and `OSSStore` don't exist yet.

- [ ] **Step 3: Implement OSSStore**

Create `internal/evidence/oss_store.go`:

```go
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
```

- [ ] **Step 4: Run tests**

Run: `go test ./internal/evidence/... -run TestOSSStore -v`
Expected: All OSSStore tests pass.

- [ ] **Step 5: Commit**

```bash
git add internal/evidence/oss_store.go internal/evidence/oss_store_test.go
git commit -m "feat(evidence): add OSSStore with mock-based tests"
```

---

### Task 6: Go — Add real OSS client adapter and factory function

**Files:**
- Modify: `internal/evidence/oss_store.go` (add real OSS adapter and factory)
- Modify: `internal/evidence/store.go` (add factory function)

- [ ] **Step 1: Add real OSS client adapter**

Add to `internal/evidence/oss_store.go`:

```go
import (
    "github.com/aliyun/aliyun-oss-go-sdk/oss"
)

type realOSSBucket struct {
    bucket *oss.Bucket
}

func newRealOSSBucket(endpoint, bucketName, accessKeyID, accessKeySecret string) (*realOSSBucket, error) {
    client, err := oss.New(endpoint, accessKeyID, accessKeySecret)
    if err != nil {
        return nil, fmt.Errorf("oss client: %w", err)
    }
    bucket, err := client.Bucket(bucketName)
    if err != nil {
        return nil, fmt.Errorf("oss bucket %s: %w", bucketName, err)
    }
    return &realOSSBucket{bucket: bucket}, nil
}

func (r *realOSSBucket) put(key string, data []byte) error {
    return r.bucket.PutObject(key, bytes.NewReader(data))
}

func (r *realOSSBucket) get(key string) ([]byte, error) {
    reader, err := r.bucket.GetObject(key)
    if err != nil {
        return nil, err
    }
    defer reader.Close()
    return io.ReadAll(reader)
}
```

- [ ] **Step 2: Add constructor and factory function**

Add to `internal/evidence/oss_store.go`:

```go
func NewOSSStore(endpoint, bucketName, accessKeyID, accessKeySecret string) (OSSStore, error) {
    client, err := newRealOSSBucket(endpoint, bucketName, accessKeyID, accessKeySecret)
    if err != nil {
        return OSSStore{}, err
    }
    return NewOSSStoreWithBucket(bucketName, client), nil
}
```

Add to `internal/evidence/store.go`:

```go
// StoreConfig holds configuration for creating the appropriate evidence Store.
type StoreConfig struct {
    Backend          string
    FilesystemRoot   string
    OSSEndpoint      string
    OSSBucket        string
    OSSAccessKeyID   string
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
```

- [ ] **Step 3: Run tests**

Run: `go test ./internal/evidence/...`
Expected: All tests pass (mock tests unchanged, factory uses compile-time type check).

- [ ] **Step 4: Commit**

```bash
git add internal/evidence/oss_store.go internal/evidence/store.go
git commit -m "feat(evidence): add real OSS client and NewStore factory function"
```

---

### Task 7: Go — Wire factory in main.go

**Files:**
- Modify: `cmd/audit-gateway/main.go:108-127` (buildHandler)
- Modify: `cmd/audit-gateway/main.go:159-163` (buildHTTPHandler admin store)
- Modify: `cmd/audit-gateway/main.go:202` (buildOpsHandler evidence check)

- [ ] **Step 1: Create store once in `buildHTTPHandler` and pass it down**

Add a helper at package level to build store config:

```go
func evidenceStoreConfig(cfg config.Config) evidence.StoreConfig {
    return evidence.StoreConfig{
        Backend:         cfg.EvidenceStorageBackend,
        FilesystemRoot:  cfg.EvidenceStorageDir,
        OSSEndpoint:     cfg.OSSEndpoint,
        OSSBucket:       cfg.OSSBucket,
        OSSAccessKeyID:     cfg.OSSAccessKeyID,
        OSSAccessKeySecret: cfg.OSSAccessKeySecret,
    }
}
```

In `buildHandler`, replace:
```go
EvidenceStore: evidence.NewFilesystemStore(cfg.EvidenceStorageDir),
```
with:
```go
EvidenceStore: evidenceStoreFromConfig(cfg),
```

In `buildHTTPHandler`, replace:
```go
EvidenceStore: evidence.NewFilesystemStore(cfg.EvidenceStorageDir),
```
with:
```go
EvidenceStore: evidenceStoreFromConfig(cfg),
```

In `buildOpsHandler`, replace:
```go
store := evidence.NewFilesystemStore(cfg.EvidenceStorageDir)
```
with:
```go
store, err := evidence.NewStore(evidenceStoreConfig(cfg))
if err != nil {
    panic(fmt.Sprintf("evidence store init: %v", err))
}
```

Add a helper function:
```go
func evidenceStoreFromConfig(cfg config.Config) evidence.Store {
    store, err := evidence.NewStore(evidenceStoreConfig(cfg))
    if err != nil {
        panic(fmt.Sprintf("evidence store init: %v", err))
    }
    return store
}
```

Note: The ops handler already handles the case where store.Put/Get fail (returns error from EvidenceCheck). The proxy handler already does not propagate Put errors to users (uses reportAuditError + spool). No error handling changes needed in the gateway layer.

- [ ] **Step 2: Run full test suite**

Run: `go test ./...`
Expected: All tests pass.

- [ ] **Step 3: Commit**

```bash
git add cmd/audit-gateway/main.go
git commit -m "feat(gateway): wire evidence store factory in main.go"
```

---

### Task 8: Go — Add Prometheus metrics for evidence store ops

**Files:**
- Create: `internal/evidence/metrics.go`
- Modify: `internal/evidence/oss_store.go` (instrument Put/Get)
- Modify: `internal/evidence/store.go` (instrument FilesystemStore Put/Get)

- [ ] **Step 1: Create metrics file**

Create `internal/evidence/metrics.go`:

```go
package evidence

import "github.com/prometheus/client_golang/prometheus"

var storeOpsTotal = prometheus.NewCounterVec(
    prometheus.CounterOpts{
        Name: "evidence_store_ops_total",
        Help: "Total evidence store operations by backend, operation, and status.",
    },
    []string{"backend", "operation", "status"},
)

func init() {
    prometheus.MustRegister(storeOpsTotal)
}
```

- [ ] **Step 2: Instrument FilesystemStore**

In `store.go` `FilesystemStore.Put`, add before the return on success:

```go
storeOpsTotal.WithLabelValues("filesystem", "put", "success").Inc()
```

And in error returns before each `return Object{}, err`:

```go
storeOpsTotal.WithLabelValues("filesystem", "put", "error").Inc()
```

Similarly for `Get`: add success/error increments.

- [ ] **Step 3: Instrument OSSStore**

In `oss_store.go` `OSSStore.Put`, add before the success return:

```go
storeOpsTotal.WithLabelValues("oss", "put", "success").Inc()
```

And before error returns:

```go
storeOpsTotal.WithLabelValues("oss", "put", "error").Inc()
```

Similarly for `Get`.

- [ ] **Step 4: Run tests**

Run: `go test ./internal/evidence/...`
Expected: All tests pass (metrics registration is tested via side effect).

- [ ] **Step 5: Commit**

```bash
git add internal/evidence/metrics.go internal/evidence/store.go internal/evidence/oss_store.go
git commit -m "feat(evidence): add Prometheus metrics for store operations"
```

---

### Task 9: Python — Update Protocol and FilesystemEvidenceStore ref format

**Files:**
- Modify: `workers/analysis_worker/evidence.py`
- Modify: `workers/analysis_worker/tests/test_evidence.py`

- [ ] **Step 1: Update EvidenceStore Protocol return types**

In `evidence.py`, change the Protocol:

```python
@runtime_checkable
class EvidenceStore(Protocol):
    def read_text(self, object_ref: str) -> str: ...
    def read_bytes(self, object_ref: str) -> bytes: ...
    def write_text(self, object_ref: str, data: str) -> str: ...
    def write_bytes(self, object_ref: str, data: bytes) -> str: ...
```

- [ ] **Step 2: Update FilesystemEvidenceStore to use `file:///` prefix**

Rewrite `FilesystemEvidenceStore`:

```python
from pathlib import Path
from typing import Protocol, runtime_checkable

_FILE_SCHEME = "file:///"


@runtime_checkable
class EvidenceStore(Protocol):
    def read_text(self, object_ref: str) -> str: ...
    def read_bytes(self, object_ref: str) -> bytes: ...
    def write_text(self, object_ref: str, data: str) -> str: ...
    def write_bytes(self, object_ref: str, data: bytes) -> str: ...


class FilesystemEvidenceStore:
    def __init__(self, root: str | Path):
        self.root = Path(root).resolve()

    def read_text(self, object_ref: str) -> str:
        path = self._path_for_ref(object_ref)
        return path.read_text(encoding="utf-8")

    def read_bytes(self, object_ref: str) -> bytes:
        path = self._path_for_ref(object_ref)
        return path.read_bytes()

    def write_text(self, object_ref: str, data: str) -> str:
        path = self._path_for_ref(object_ref)
        path.parent.mkdir(parents=True, exist_ok=True)
        tmp = path.with_suffix(path.suffix + ".tmp")
        try:
            tmp.write_text(data, encoding="utf-8")
            tmp.replace(path)
        except BaseException:
            tmp.unlink(missing_ok=True)
            raise
        return object_ref

    def write_bytes(self, object_ref: str, data: bytes) -> str:
        path = self._path_for_ref(object_ref)
        path.parent.mkdir(parents=True, exist_ok=True)
        path.write_bytes(data)
        return object_ref

    def _path_for_ref(self, object_ref: str) -> Path:
        if not object_ref.startswith(_FILE_SCHEME):
            raise ValueError(f"invalid object ref {object_ref!r}: must start with {_FILE_SCHEME}")
        relative = object_ref[len(_FILE_SCHEME):]
        if not relative:
            raise ValueError(f"invalid object ref {object_ref!r}: empty path")
        if "\\" in relative or "//" in relative or ".." in relative:
            raise ValueError(f"invalid object ref {object_ref!r}")
        ref_path = Path(relative)
        if ref_path.is_absolute():
            raise ValueError(f"invalid object ref {object_ref!r}")
        candidate = (self.root / ref_path).resolve()
        if candidate != self.root and self.root not in candidate.parents:
            raise ValueError(f"object ref escapes evidence root {object_ref!r}")
        return candidate
```

- [ ] **Step 3: Update tests**

Update `tests/test_evidence.py`:

```python
from pathlib import Path

import pytest

from evidence import EvidenceStore, FilesystemEvidenceStore


def _ref(path: str) -> str:
    return f"file:///{path}"


def test_file_evidence_store_reads_ref_under_root(tmp_path: Path):
    evidence_path = tmp_path / "raw" / "2026" / "04" / "28" / "trace_1"
    evidence_path.mkdir(parents=True)
    (evidence_path / "request_body.bin").write_text('{"model":"gpt-4.1"}', encoding="utf-8")

    store = FilesystemEvidenceStore(tmp_path)

    assert store.read_text(_ref("raw/2026/04/28/trace_1/request_body.bin")) == '{"model":"gpt-4.1"}'


def test_file_evidence_store_rejects_path_escape(tmp_path: Path):
    store = FilesystemEvidenceStore(tmp_path)

    with pytest.raises(ValueError, match="invalid object ref"):
        store.read_text(_ref("../secrets.env"))


def test_file_evidence_store_rejects_non_file_scheme(tmp_path: Path):
    store = FilesystemEvidenceStore(tmp_path)
    with pytest.raises(ValueError, match="must start with file:///"):
        store.read_text("oss://bucket/raw/key.bin")


def test_filesystem_evidence_store_write_text_creates_file(tmp_path: Path):
    store = FilesystemEvidenceStore(tmp_path)
    ref = _ref("raw/2026/05/05/trace_1/request_body.bin")
    result = store.write_text(ref, '{"model":"gpt-4.1"}')

    assert result == ref
    assert store.read_text(ref) == '{"model":"gpt-4.1"}'


def test_filesystem_evidence_store_write_text_overwrites_existing(tmp_path: Path):
    ref = _ref("raw/2026/05/05/trace_1/request_body.bin")
    store = FilesystemEvidenceStore(tmp_path)
    store.write_text(ref, "original")
    store.write_text(ref, "updated")

    assert store.read_text(ref) == "updated"


def test_filesystem_evidence_store_write_text_rejects_path_escape(tmp_path: Path):
    store = FilesystemEvidenceStore(tmp_path)
    with pytest.raises(ValueError, match="invalid object ref"):
        store.write_text(_ref("../../etc/passwd"), "data")


def test_filesystem_evidence_store_write_bytes_creates_file(tmp_path: Path):
    store = FilesystemEvidenceStore(tmp_path)
    binary = b"\x89PNG\r\n\x1a\n"
    ref = _ref("raw/2026/05/05/trace_1/media_asset_000001.bin")
    result = store.write_bytes(ref, binary)

    assert result == ref
    path = tmp_path / "raw" / "2026" / "05" / "05" / "trace_1" / "media_asset_000001.bin"
    assert path.read_bytes() == binary


def test_filesystem_evidence_store_write_bytes_rejects_path_escape(tmp_path: Path):
    store = FilesystemEvidenceStore(tmp_path)
    with pytest.raises(ValueError, match="invalid object ref"):
        store.write_bytes(_ref("../../../etc/shadow"), b"secret")


def test_evidence_store_is_protocol():
    assert issubclass(FilesystemEvidenceStore, EvidenceStore)
```

- [ ] **Step 4: Run tests**

Run: `cd workers/analysis_worker && uv run pytest tests/test_evidence.py -v`
Expected: All tests pass.

- [ ] **Step 5: Commit**

```bash
git add workers/analysis_worker/evidence.py workers/analysis_worker/tests/test_evidence.py
git commit -m "feat(worker): update EvidenceStore Protocol and FilesystemStore ref format to file:/// scheme"
```

---

### Task 10: Python — Update media_extraction.py to use scheme-prefixed refs

**Files:**
- Modify: `workers/analysis_worker/media_extraction.py`
- Modify: `workers/analysis_worker/tests/test_media_extraction.py`

- [ ] **Step 1: Update MediaExtractionContext to use `file:///` refs**

In `media_extraction.py`, the `evidence_dir` needs to produce `file:///` prefixed refs. Update `_decode_and_store`:

Change line 69:
```python
object_ref = f"file:///{self.evidence_dir}/{object_type}.bin"
```

And in `apply_replacements`, the `object_ref` parameter is already expected to be scheme-prefixed, so no change needed there.

- [ ] **Step 2: Update tests**

In `tests/test_media_extraction.py`, `evidence_dir` passed to `MediaExtractionContext` remains the plain path (no scheme) — the scheme is added inside `_decode_and_store`. But all refs used directly with the store must have `file:///` prefix.

Update all tests:

```python
import base64
from pathlib import Path

import pytest

from evidence import FilesystemEvidenceStore
from media_extraction import MediaExtractionContext, MediaAsset


def _data_url(media_type: str, payload: bytes) -> str:
    encoded = base64.b64encode(payload).decode("ascii")
    return f"data:{media_type};base64,{encoded}"


def _ref(path: str) -> str:
    return f"file:///{path}"


def test_extract_data_url_writes_binary_asset(tmp_path: Path):
    store = FilesystemEvidenceStore(tmp_path)
    ctx = MediaExtractionContext(store, "raw/2026/05/05/trace_1", "trace_1")
    png_data = b"\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR"
    data_url = _data_url("image/png", png_data)

    asset = ctx.extract_data_url(data_url, "image")

    assert asset is not None
    assert asset.object_type == "media_asset_000001"
    assert asset.media_type == "image/png"
    assert asset.size_bytes == len(png_data)
    written = store.read_bytes(_ref("raw/2026/05/05/trace_1/media_asset_000001.bin"))
    assert written == png_data


def test_extract_data_url_returns_replacement_mapping(tmp_path: Path):
    store = FilesystemEvidenceStore(tmp_path)
    ctx = MediaExtractionContext(store, "raw/2026/05/05/trace_1", "trace_1")
    png_data = b"small image data"
    data_url = _data_url("image/png", png_data)

    asset = ctx.extract_data_url(data_url, "image")

    assert asset is not None
    assert len(ctx.replacements) == 1
    assert ctx.replacements[0] == (data_url, f"audit-media:{asset.object_type}")


def test_extract_data_url_sequential_numbering(tmp_path: Path):
    store = FilesystemEvidenceStore(tmp_path)
    ctx = MediaExtractionContext(store, "raw/2026/05/05/trace_1", "trace_1")

    asset1 = ctx.extract_data_url(_data_url("image/png", b"img1"), "image")
    asset2 = ctx.extract_data_url("data:audio/wav;base64," + base64.b64encode(b"aud1").decode(), "audio")

    assert asset1.object_type == "media_asset_000001"
    assert asset2.object_type == "media_asset_000002"
    assert len(ctx.replacements) == 2


def test_extract_data_url_skips_oversized(tmp_path: Path):
    store = FilesystemEvidenceStore(tmp_path)
    ctx = MediaExtractionContext(store, "raw/2026/05/05/trace_1", "trace_1", max_bytes=10)
    big_payload = b"x" * 100
    data_url = _data_url("image/png", big_payload)

    asset = ctx.extract_data_url(data_url, "image")

    assert asset is None
    assert ctx.replacements == []


def test_extract_data_url_skips_invalid_base64(tmp_path: Path):
    store = FilesystemEvidenceStore(tmp_path)
    ctx = MediaExtractionContext(store, "raw/2026/05/05/trace_1", "trace_1")

    asset = ctx.extract_data_url("data:image/png;base64,!!!invalid!!!", "image")

    assert asset is None
    assert ctx.replacements == []


def test_extract_raw_base64_writes_binary_asset(tmp_path: Path):
    store = FilesystemEvidenceStore(tmp_path)
    ctx = MediaExtractionContext(store, "raw/2026/05/05/trace_1", "trace_1")
    raw_b64 = base64.b64encode(b"audio data").decode("ascii")

    asset = ctx.extract_raw_base64(raw_b64, "audio/wav", "audio")

    assert asset is not None
    assert asset.media_type == "audio/wav"
    assert asset.size_bytes == len(b"audio data")
    assert ctx.replacements[0] == (raw_b64, f"audit-media:{asset.object_type}")


def test_apply_replacements_modifies_json(tmp_path: Path):
    store = FilesystemEvidenceStore(tmp_path)
    evidence_dir = "raw/2026/05/05/trace_1"
    ctx = MediaExtractionContext(store, evidence_dir, "trace_1")
    data_url = _data_url("image/png", b"img")
    ctx.extract_data_url(data_url, "image")

    original_json = '{"url":"' + data_url + '"}'
    ref = _ref(f"{evidence_dir}/request_body.bin")
    store.write_text(ref, original_json)
    ctx.apply_replacements(ref)

    modified = store.read_text(ref)
    assert "audit-media:media_asset_000001" in modified
    assert data_url not in modified


def test_apply_replacements_noop_when_empty(tmp_path: Path):
    store = FilesystemEvidenceStore(tmp_path)
    ctx = MediaExtractionContext(store, "raw/2026/05/05/trace_1", "trace_1")
    ref = _ref("raw/2026/05/05/trace_1/request_body.bin")
    original = '{"model":"gpt-4.1"}'
    store.write_text(ref, original)

    ctx.apply_replacements(ref)

    assert store.read_text(ref) == original
```

- [ ] **Step 3: Run tests**

Run: `cd workers/analysis_worker && uv run pytest tests/test_media_extraction.py -v`
Expected: All tests pass.

- [ ] **Step 4: Commit**

```bash
git add workers/analysis_worker/media_extraction.py workers/analysis_worker/tests/test_media_extraction.py
git commit -m "feat(worker): update media extraction to use file:/// scheme refs"
```

---

### Task 11: Python — Update repository.py storage_backend parameterization

**Files:**
- Modify: `workers/analysis_worker/repository.py:335-355`

- [ ] **Step 1: Add storage_backend parameter to save_media_assets**

Change the method signature and remove hardcoded `'filesystem'`:

```python
def save_media_assets(self, trace_id: str, assets: list, storage_backend: str = "filesystem") -> None:
    if not assets:
        return
    cursor = self.connection.cursor()
    for asset in assets:
        cursor.execute(
            """
            INSERT INTO raw_evidence_objects (
                trace_id, object_type, object_ref, storage_backend,
                content_type, size_bytes
            ) VALUES (%s, %s, %s, %s, %s, %s)
            """,
            (
                trace_id,
                asset.object_type,
                asset.object_ref,
                storage_backend,
                asset.media_type,
                asset.size_bytes,
            ),
        )
    self.connection.commit()
```

- [ ] **Step 2: Run tests**

Run: `cd workers/analysis_worker && uv run pytest tests/test_repository.py -v`
Expected: All tests pass (existing tests use default "filesystem").

- [ ] **Step 3: Commit**

```bash
git add workers/analysis_worker/repository.py
git commit -m "feat(worker): parameterize storage_backend in save_media_assets"
```

---

### Task 12: Python — Add OSS store implementation

**Files:**
- Create: `workers/analysis_worker/oss_evidence.py`
- Create: `workers/analysis_worker/tests/test_oss_evidence.py`
- Modify: `workers/analysis_worker/pyproject.toml` (add oss2 dependency)

- [ ] **Step 1: Add oss2 dependency**

Run: `cd workers/analysis_worker && uv add oss2`

- [ ] **Step 2: Write failing tests**

Create `workers/analysis_worker/tests/test_oss_evidence.py`:

```python
from unittest.mock import MagicMock

import pytest

from oss_evidence import OSSEvidenceStore


class FakeBucket:
    def __init__(self):
        self.objects: dict[str, bytes] = {}

    def get_object(self, key: str) -> MagicMock:
        if key not in self.objects:
            raise FileNotFoundError(f"object not found: {key}")
        resp = MagicMock()
        resp.read.return_value = self.objects[key]
        return resp

    def put_object(self, key: str, data: bytes) -> None:
        self.objects[key] = data


def _oss_ref(bucket: str, key: str) -> str:
    return f"oss://{bucket}/{key}"


def test_oss_store_read_text(tmp_path):
    bucket = FakeBucket()
    bucket.objects["raw/2026/05/05/trace_1/request_body.bin"] = b'{"model":"gpt-4.1"}'
    store = OSSEvidenceStore("test-bucket", bucket)

    result = store.read_text(_oss_ref("test-bucket", "raw/2026/05/05/trace_1/request_body.bin"))
    assert result == '{"model":"gpt-4.1"}'


def test_oss_store_read_bytes(tmp_path):
    bucket = FakeBucket()
    binary = b"\x89PNG\r\n\x1a\n"
    bucket.objects["raw/2026/05/05/trace_1/media.bin"] = binary
    store = OSSEvidenceStore("test-bucket", bucket)

    result = store.read_bytes(_oss_ref("test-bucket", "raw/2026/05/05/trace_1/media.bin"))
    assert result == binary


def test_oss_store_write_text(tmp_path):
    bucket = FakeBucket()
    store = OSSEvidenceStore("test-bucket", bucket)
    ref = _oss_ref("test-bucket", "raw/2026/05/05/trace_1/request_body.bin")

    result = store.write_text(ref, '{"model":"gpt-4.1"}')

    assert result == ref
    assert bucket.objects["raw/2026/05/05/trace_1/request_body.bin"] == b'{"model":"gpt-4.1"}'


def test_oss_store_write_bytes(tmp_path):
    bucket = FakeBucket()
    store = OSSEvidenceStore("test-bucket", bucket)
    binary = b"\x89PNG\r\n\x1a\n"
    ref = _oss_ref("test-bucket", "raw/2026/05/05/trace_1/media.bin")

    result = store.write_bytes(ref, binary)

    assert result == ref
    assert bucket.objects["raw/2026/05/05/trace_1/media.bin"] == binary


def test_oss_store_rejects_wrong_scheme(tmp_path):
    bucket = FakeBucket()
    store = OSSEvidenceStore("test-bucket", bucket)

    with pytest.raises(ValueError, match="must start with oss://"):
        store.read_text("file:///raw/key.bin")


def test_oss_store_rejects_wrong_bucket(tmp_path):
    bucket = FakeBucket()
    store = OSSEvidenceStore("test-bucket", bucket)

    with pytest.raises(ValueError, match="must start with oss://test-bucket/"):
        store.read_text("oss://other-bucket/raw/key.bin")


def test_oss_store_rejects_empty_key(tmp_path):
    bucket = FakeBucket()
    store = OSSEvidenceStore("test-bucket", bucket)

    with pytest.raises(ValueError, match="empty key"):
        store.read_text("oss://test-bucket/")


def test_oss_store_is_evidence_store():
    from evidence import EvidenceStore
    bucket = FakeBucket()
    store = OSSEvidenceStore("test-bucket", bucket)
    assert isinstance(store, EvidenceStore)
```

- [ ] **Step 3: Run tests to verify they fail**

Run: `cd workers/analysis_worker && uv run pytest tests/test_oss_evidence.py -v`
Expected: FAIL — `oss_evidence` module doesn't exist.

- [ ] **Step 4: Implement OSSEvidenceStore**

Create `workers/analysis_worker/oss_evidence.py`:

```python
import oss2

from evidence import EvidenceStore


class _BucketWrapper:
    """Wraps an oss2.Bucket for testability."""

    def __init__(self, bucket: oss2.Bucket):
        self._bucket = bucket

    def get_object(self, key: str):
        return self._bucket.get_object(key)

    def put_object(self, key: str, data: bytes):
        self._bucket.put_object(key, data)


class OSSEvidenceStore:
    def __init__(self, bucket_name: str, bucket_client):
        self._bucket_name = bucket_name
        self._client = bucket_client

    @classmethod
    def from_env(cls, endpoint: str, bucket_name: str, access_key_id: str, access_key_secret: str) -> "OSSEvidenceStore":
        auth = oss2.Auth(access_key_id, access_key_secret)
        bucket = oss2.Bucket(auth, endpoint, bucket_name)
        return cls(bucket_name, _BucketWrapper(bucket))

    def read_text(self, object_ref: str) -> str:
        key = self._parse_ref(object_ref)
        result = self._client.get_object(key)
        return result.read().decode("utf-8")

    def read_bytes(self, object_ref: str) -> bytes:
        key = self._parse_ref(object_ref)
        result = self._client.get_object(key)
        return result.read()

    def write_text(self, object_ref: str, data: str) -> str:
        key = self._parse_ref(object_ref)
        self._client.put_object(key, data.encode("utf-8"))
        return object_ref

    def write_bytes(self, object_ref: str, data: bytes) -> str:
        key = self._parse_ref(object_ref)
        self._client.put_object(key, data)
        return object_ref

    def _parse_ref(self, object_ref: str) -> str:
        prefix = f"oss://{self._bucket_name}/"
        if not object_ref.startswith(prefix):
            raise ValueError(f"invalid object ref {object_ref!r}: must start with {prefix}")
        key = object_ref[len(prefix):]
        if not key:
            raise ValueError(f"invalid object ref {object_ref!r}: empty key")
        return key
```

- [ ] **Step 5: Run tests**

Run: `cd workers/analysis_worker && uv run pytest tests/test_oss_evidence.py -v`
Expected: All tests pass.

- [ ] **Step 6: Commit**

```bash
git add workers/analysis_worker/oss_evidence.py workers/analysis_worker/tests/test_oss_evidence.py workers/analysis_worker/pyproject.toml workers/analysis_worker/uv.lock
git commit -m "feat(worker): add OSSEvidenceStore with tests"
```

---

### Task 13: Python — Refactor main.py: remove CLI args, add factory, pass storage_backend

**Files:**
- Modify: `workers/analysis_worker/main.py`

- [ ] **Step 1: Add factory function and update imports**

Replace the `from evidence import FilesystemEvidenceStore` import with:

```python
import os

from evidence import EvidenceStore, FilesystemEvidenceStore
```

Add factory function after imports:

```python
def create_evidence_store() -> EvidenceStore:
    backend = os.environ.get("EVIDENCE_STORAGE_BACKEND", "").strip()
    if backend == "oss":
        from oss_evidence import OSSEvidenceStore
        endpoint = os.environ.get("OSS_ENDPOINT", "").strip()
        bucket = os.environ.get("OSS_BUCKET", "").strip()
        access_key_id = os.environ.get("OSS_ACCESS_KEY_ID", "").strip()
        access_key_secret = os.environ.get("OSS_ACCESS_KEY_SECRET", "").strip()
        missing = [k for k, v in [
            ("OSS_ENDPOINT", endpoint),
            ("OSS_BUCKET", bucket),
            ("OSS_ACCESS_KEY_ID", access_key_id),
            ("OSS_ACCESS_KEY_SECRET", access_key_secret),
        ] if not v]
        if missing:
            raise SystemExit(f"EVIDENCE_STORAGE_BACKEND=oss requires {', '.join(missing)}")
        return OSSEvidenceStore.from_env(endpoint, bucket, access_key_id, access_key_secret)
    if backend == "filesystem":
        evidence_dir = os.environ.get("EVIDENCE_STORAGE_DIR", "").strip()
        if not evidence_dir:
            raise SystemExit("EVIDENCE_STORAGE_DIR is required when EVIDENCE_STORAGE_BACKEND=filesystem")
        if not Path(evidence_dir).is_absolute():
            evidence_dir = str((Path(__file__).resolve().parent.parent.parent / evidence_dir).resolve())
        return FilesystemEvidenceStore(evidence_dir)
    raise SystemExit(f"EVIDENCE_STORAGE_BACKEND must be 'filesystem' or 'oss', got {backend!r}")
```

- [ ] **Step 2: Update function signatures**

Change `process_job_line` signature — replace `evidence_store: FilesystemEvidenceStore` with `evidence_store: EvidenceStore`.

Change `process_trace` signature — replace `evidence_store: FilesystemEvidenceStore | None` with `evidence_store: EvidenceStore | None`.

- [ ] **Step 3: Remove `--evidence-root` CLI arg and update main()**

Remove `parser.add_argument("--evidence-root", ...)` from `main()`.

Replace the `main()` function body:

```python
def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("--redis-once", action="store_true")
    parser.add_argument("--redis-url", default=os.environ.get("REDIS_URL", "redis://localhost:6379/0"))
    parser.add_argument("--redis-list", default=os.environ.get("ANALYSIS_REDIS_LIST", "analysis_jobs"))
    parser.add_argument("--redis-timeout-seconds", type=int, default=5)
    parser.add_argument("--postgres-dsn", default=os.environ.get("POSTGRES_DSN", ""))
    args = parser.parse_args()

    if not args.redis_once and "EVIDENCE_STORAGE_BACKEND" not in os.environ and not args.postgres_dsn:
        return process_contract_stdin()

    evidence_store = create_evidence_store()
    storage_backend = os.environ.get("EVIDENCE_STORAGE_BACKEND", "")

    if not args.postgres_dsn:
        raise SystemExit("POSTGRES_DSN is required")
    if args.redis_once:
        return process_redis_once(
            args.redis_url,
            args.redis_list,
            evidence_store,
            args.postgres_dsn,
            args.redis_timeout_seconds,
            storage_backend=storage_backend,
        )
    return process_redis_loop(
        args.redis_url,
        args.redis_list,
        evidence_store,
        args.postgres_dsn,
        args.redis_timeout_seconds,
        storage_backend=storage_backend,
    )
```

- [ ] **Step 4: Update process_stdin, process_redis_once, process_redis_loop**

Update `process_stdin` to use factory:

```python
def process_stdin(evidence_store: EvidenceStore, postgres_dsn: str) -> int:
    payload = sys.stdin.read().strip()
    if not payload:
        return 0
    with psycopg.connect(postgres_dsn) as connection:
        result = process_job_line(
            payload,
            evidence_store,
            PostgresAnalysisRepository(connection),
            PostgresContextRepository(connection),
        )
    print(json.dumps(result, sort_keys=True))
    return 0
```

Update `process_redis_once` — change `evidence_root: str` to `evidence_store: EvidenceStore` and add `storage_backend: str = "filesystem"`:

```python
def process_redis_once(
    redis_url: str,
    list_name: str,
    evidence_store: EvidenceStore,
    postgres_dsn: str,
    timeout_seconds: int,
    connection_factory=psycopg.connect,
    storage_backend: str = "filesystem",
) -> int:
    client = redis.Redis.from_url(redis_url, decode_responses=True)
    item = client.blpop(list_name, timeout=timeout_seconds)
    with connection_factory(postgres_dsn) as connection:
        heartbeat = HeartbeatRepository(connection)
        if item is None:
            heartbeat.record(
                worker_id=worker_id(),
                worker_kind="analysis",
                status="idle",
                queue_name=list_name,
                processed_count=0,
                error_count=0,
                metadata={"poll_result": "idle"},
            )
            print(json.dumps({"worker_status": "idle", "list": list_name}, sort_keys=True))
            return 0
        _, payload = item
        try:
            result = process_job_line(
                payload,
                evidence_store,
                PostgresAnalysisRepository(connection),
                PostgresContextRepository(connection),
            )
        except Exception as exc:
            record_heartbeat_safely(
                heartbeat,
                connection,
                worker_id=worker_id(),
                worker_kind="analysis",
                status="error",
                queue_name=list_name,
                processed_count=0,
                error_count=1,
                metadata={"error_type": exc.__class__.__name__},
            )
            raise
        heartbeat.record(
            worker_id=worker_id(),
            worker_kind="analysis",
            status="processed",
            queue_name=list_name,
            processed_count=1,
            error_count=0,
            metadata={"trace_id": result.get("accepted_trace_id", "")},
        )
    print(json.dumps(result, sort_keys=True))
    return 0
```

Update `process_redis_loop` similarly — accept `evidence_store: EvidenceStore` and `storage_backend: str`:

```python
def process_redis_loop(
    redis_url: str,
    list_name: str,
    evidence_store: EvidenceStore,
    postgres_dsn: str,
    timeout_seconds: int,
    storage_backend: str = "filesystem",
) -> int:
    client = redis.Redis.from_url(redis_url, decode_responses=True)
    wid = worker_id()
    running = True

    def _stop(signum, _frame):
        nonlocal running
        running = False

    signal.signal(signal.SIGINT, _stop)
    signal.signal(signal.SIGTERM, _stop)
    print(json.dumps({"worker_status": "starting", "worker_id": wid, "list": list_name}), flush=True)

    while running:
        item = client.blpop(list_name, timeout=timeout_seconds)
        if not running:
            break
        with psycopg.connect(postgres_dsn) as connection:
            heartbeat = HeartbeatRepository(connection)
            if item is None:
                heartbeat.record(
                    worker_id=wid,
                    worker_kind="analysis",
                    status="idle",
                    queue_name=list_name,
                    processed_count=0,
                    error_count=0,
                    metadata={"poll_result": "idle"},
                )
                continue
            _, payload = item
            try:
                result = process_job_line(
                    payload,
                    evidence_store,
                    PostgresAnalysisRepository(connection),
                    PostgresContextRepository(connection),
                )
            except Exception as exc:
                record_heartbeat_safely(
                    heartbeat,
                    connection,
                    worker_id=wid,
                    worker_kind="analysis",
                    status="error",
                    queue_name=list_name,
                    processed_count=0,
                    error_count=1,
                    metadata={"error_type": exc.__class__.__name__},
                )
                print(json.dumps({"worker_status": "error", "error": str(exc)}), flush=True)
                continue
            heartbeat.record(
                worker_id=wid,
                worker_kind="analysis",
                status="processed",
                queue_name=list_name,
                processed_count=1,
                error_count=0,
                metadata={"trace_id": result.get("accepted_trace_id", "")},
            )
            print(json.dumps(result, sort_keys=True), flush=True)

    print(json.dumps({"worker_status": "stopped", "worker_id": wid}), flush=True)
    return 0
```

- [ ] **Step 5: Update process_trace to pass storage_backend to save_media_assets**

In `process_trace`, update the `save_media_assets` call:

```python
if hasattr(repository, "save_media_assets"):
    repository.save_media_assets(job.trace_id, extraction_context.assets, storage_backend=storage_backend)
```

This requires `process_trace` to accept a `storage_backend` parameter, which gets passed from callers.

- [ ] **Step 6: Run all Python tests**

Run: `cd workers/analysis_worker && uv run pytest -q`
Expected: All tests pass.

- [ ] **Step 7: Commit**

```bash
git add workers/analysis_worker/main.py
git commit -m "feat(worker): refactor main.py to use evidence store factory and env-only config"
```

---

### Task 14: Update docker-compose.yml with new env vars

**Files:**
- Modify: `deploy/docker-compose.yml`

- [ ] **Step 1: Add EVIDENCE_STORAGE_BACKEND to services**

Add `EVIDENCE_STORAGE_BACKEND: filesystem` to both the gateway and analysis-worker services.

- [ ] **Step 2: Verify compose file syntax**

Run: `docker compose -f deploy/docker-compose.yml config --quiet`
Expected: No errors.

- [ ] **Step 3: Commit**

```bash
git add deploy/docker-compose.yml
git commit -m "feat(deploy): add EVIDENCE_STORAGE_BACKEND to docker-compose services"
```

---

### Task 15: Go — Add OSS integration test

**Files:**
- Create: `internal/evidence/oss_integration_test.go`

- [ ] **Step 1: Write integration test**

Create `internal/evidence/oss_integration_test.go`:

```go
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
```

- [ ] **Step 2: Run integration test (requires OSS credentials)**

Run: `OSS_ENDPOINT=... OSS_BUCKET=... OSS_ACCESS_KEY_ID=... OSS_ACCESS_KEY_SECRET=... go test ./internal/evidence/... -tags=integration -run TestOSSStoreIntegration -v`
Expected: PASS (put and get round-trip against real OSS).

- [ ] **Step 3: Commit**

```bash
git add internal/evidence/oss_integration_test.go
git commit -m "test(evidence): add OSS integration test with real bucket"
```

---

### Task 16: Python — Add OSS integration test

**Files:**
- Create: `workers/analysis_worker/tests/test_oss_integration.py`

- [ ] **Step 1: Write integration test**

Create `workers/analysis_worker/tests/test_oss_integration.py`:

```python
import os

import pytest

from oss_evidence import OSSEvidenceStore


@pytest.fixture
def oss_store():
    endpoint = os.environ.get("OSS_ENDPOINT", "")
    bucket = os.environ.get("OSS_BUCKET", "")
    access_key_id = os.environ.get("OSS_ACCESS_KEY_ID", "")
    access_key_secret = os.environ.get("OSS_ACCESS_KEY_SECRET", "")
    if not all([endpoint, bucket, access_key_id, access_key_secret]):
        pytest.skip("OSS environment variables not set")
    return OSSEvidenceStore.from_env(endpoint, bucket, access_key_id, access_key_secret)


@pytest.mark.integration
def test_oss_round_trip_text(oss_store):
    ref = f"oss://{oss_store._bucket_name}/raw/test/integration/text.txt"
    content = '{"integration":"test"}'

    result = oss_store.write_text(ref, content)
    assert result == ref

    read = oss_store.read_text(ref)
    assert read == content


@pytest.mark.integration
def test_oss_round_trip_bytes(oss_store):
    ref = f"oss://{oss_store._bucket_name}/raw/test/integration/binary.bin"
    data = b"\x89PNG\r\n\x1a\n"

    result = oss_store.write_bytes(ref, data)
    assert result == ref

    read = oss_store.read_bytes(ref)
    assert read == data
```

- [ ] **Step 2: Run integration test (requires OSS credentials)**

Run: `cd workers/analysis_worker && OSS_ENDPOINT=... OSS_BUCKET=... OSS_ACCESS_KEY_ID=... OSS_ACCESS_KEY_SECRET=... uv run pytest tests/test_oss_integration.py -v`
Expected: PASS (round-trip read/write against real OSS).

- [ ] **Step 3: Commit**

```bash
git add workers/analysis_worker/tests/test_oss_integration.py
git commit -m "test(worker): add OSS integration test with real bucket"
```

---

### Task 17: Add migration idempotency test

**Files:**
- Create: `scripts/test_migration_idempotency.sh`

- [ ] **Step 1: Write idempotency test script**

Create `scripts/test_migration_idempotency.sh`:

```bash
#!/usr/bin/env bash
set -euo pipefail

# Test that migration 0013 is idempotent — running it twice produces the same result.
# Requires a running Postgres with the schema loaded.

DSN="${POSTGRES_DSN:-postgres://localhost:5432/audit_gateway}"

# Insert test data without prefix
psql "$DSN" -c "
INSERT INTO raw_evidence_objects (trace_id, object_type, object_ref, storage_backend, content_type, size_bytes)
VALUES ('test-idempotency-1', 'request_body', 'raw/2026/01/01/trace_1/request_body.bin', 'filesystem', 'application/json', 0)
ON CONFLICT DO NOTHING;
"

# Run migration once
psql "$DSN" -f migrations/0013_object_ref_scheme_prefix.sql

# Verify prefix added
count=$(psql "$DSN" -t -c "
SELECT COUNT(*) FROM raw_evidence_objects
WHERE trace_id = 'test-idempotency-1' AND object_ref = 'file:///raw/2026/01/01/trace_1/request_body.bin';
")
echo "After first run: $count rows with prefix (expect 1)"

# Run migration again (should be no-op)
psql "$DSN" -f migrations/0013_object_ref_scheme_prefix.sql

# Verify no double-prefix
double=$(psql "$DSN" -t -c "
SELECT COUNT(*) FROM raw_evidence_objects
WHERE object_ref LIKE 'file:///file:///%';
")
echo "After second run: $double double-prefixed rows (expect 0)"

# Cleanup
psql "$DSN" -c "DELETE FROM raw_evidence_objects WHERE trace_id = 'test-idempotency-1';"
```

- [ ] **Step 2: Commit**

```bash
chmod +x scripts/test_migration_idempotency.sh
git add scripts/test_migration_idempotency.sh
git commit -m "test(migration): add idempotency test for 0013 scheme prefix"
```

---

### Task 18: Run full test suites

- [ ] **Step 1: Run Go tests**

Run: `go test ./...`
Expected: All tests pass.

- [ ] **Step 2: Run Python tests**

Run: `cd workers/analysis_worker && uv run pytest -q`
Expected: All tests pass.

- [ ] **Step 3: Run Go vet**

Run: `go vet ./...`
Expected: No issues.
