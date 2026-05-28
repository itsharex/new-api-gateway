# Trace Route Filter Design

## Problem

The gateway currently traces every request that reaches the proxy handler, including non-model requests (e.g. new-api management backend routes like `/api/home_page_content`, `/panel`). This creates noise in the trace data and inflates storage.

## Decision

Only requests matching a registered model API route in `routes.Registry` will be proxied and traced. All other requests receive a 404 response and are not forwarded upstream.

## Changes

### 1. `internal/gateway/proxy.go` — Reject unmatched routes

In `ServeHTTP()`, when `h.Registry.Match()` returns `ok == false`, return a 404 immediately instead of constructing an "unknown" entry and continuing to proxy.

Response format (OpenAI-compatible JSON):
```json
{
  "error": {
    "message": "unknown route: GET /panel",
    "type": "not_found",
    "code": 404
  }
}
```

### 2. `internal/routes/registry.go` — Route documentation comments

Add per-entry comments describing the protocol family and purpose of each route, serving as inline documentation for the ~32 registered routes.

### 3. `README.md` — Supported routes table

Add a "中转路由清单" section listing all supported model API routes with method, path, protocol family, and description.

## What Does NOT Change

- Ops/admin/admin UI dispatch logic in `buildHTTPHandler()` — already correctly isolated
- Route registry structure and matching algorithm
- Trace recording flow for matched routes
- Evidence capture and persistence

## Implications

- Access to new-api's own management backend (`/panel`, `/api/*`) will no longer work through the gateway. Users must access new-api directly.
- Any currently-traced "unknown" routes in production (browser probes, health checks from external LBs, etc.) will start receiving 404s. This is expected and desired.
