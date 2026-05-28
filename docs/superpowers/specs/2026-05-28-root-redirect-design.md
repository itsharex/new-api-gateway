# Root Path Redirect to Admin UI

## Problem

Accessing `http://localhost:8080/` returns a 404 JSON error from the gateway proxy, because `/` does not match any model route in the registry. Users must manually navigate to `/admin` to reach the management dashboard.

## Solution

Redirect `GET /` to `/admin` with HTTP 302 (Found).

## Design

### Where

`cmd/audit-gateway/main.go` — the `buildHTTPHandler` function contains two dispatch closures:

1. **Degraded path** (pool == nil, line ~135): add root redirect before `isOpsPath` check
2. **Normal path** (line ~166): add root redirect before `isOpsPath` check

### What changes

Both closures gain the same 3-line block at the top:

```go
if r.URL.Path == "/" {
    http.Redirect(w, r, "/admin", http.StatusFound)
    return
}
```

### What does NOT change

- `internal/routes/registry.go` — root path is not a model route
- `internal/gateway/proxy.go` — root path is intercepted before reaching the proxy
- `internal/adminui/static.go` — admin UI already works at `/admin`

### Why 302, not 301

302 (Found) is temporary — if the root path later serves a landing page or different content, clients won't have cached a permanent redirect. 302 also avoids preflight CORS issues that some browsers exhibit with 301.

## Testing

- `GET /` → 302 redirect to `/admin`
- `GET /admin` → unchanged (admin UI)
- `GET /healthz` → unchanged (ops handler)
- `GET /v1/chat/completions` → unchanged (gateway proxy)

## Scope

Minimal: two identical 3-line additions in `main.go`. No new files, no new dependencies.
