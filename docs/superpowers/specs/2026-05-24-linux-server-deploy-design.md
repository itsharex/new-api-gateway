# Linux Server Deployment Design

## Overview

Deploy new-api + new-api-gateway to remote Linux server (47.113.144.13) as two independent Docker Compose stacks, connected via external Docker network.

## Architecture

```
┌────────────── new-api_new-api-network ───────────────┐
│                                                       │
│  ┌── new-api (unchanged) ──┐  ┌── gateway ──────────┐│
│  │ new-api (:3000)         │  │ audit-gateway (:8080)││
│  │ new-api-redis           │  │ analysis-worker      ││
│  │ new-api-postgres        │←─│ gateway-postgres     ││
│  │                         │  │ gateway-redis        ││
│  └─────────────────────────┘  └──────────────────────┘│
└───────────────────────────────────────────────────────┘
```

## Decisions

- **Database**: Independent PostgreSQL per project (new-api: PG15, gateway: PG16)
- **Networking**: Gateway joins new-api's existing Docker network as external — zero changes to new-api
- **new-api source**: Pre-built image `calciumion/new-api:latest`, clone docker-compose.yml from GitHub
- **Gateway source**: rsync from local worktree, multi-stage Docker build on server
- **Ports**: new-api 3000, gateway 8080; PostgreSQL/Redis not exposed to host

## Directory Layout

```
/root/
├── new-api/
│   ├── docker-compose.yml    # from GitHub
│   └── .env
└── new-api-gateway/
    ├── deploy/
    │   ├── docker-compose.yml  # modified to join external network
    │   └── Dockerfile
    ├── workers/
    ├── migrations/
    └── .env
```

## Environment Variables

### Gateway .env
```
NEW_API_BASE_URL=http://new-api:3000
NEW_API_POSTGRES_DSN=postgres://root:123456@new-api-postgres:5432/new-api?sslmode=disable
AUDIT_HMAC_SECRET=<generated 32-char random string>
AUDIT_GATEWAY_PORT=8080
EVIDENCE_HOST_DIR=./var/evidence
```

## Changes to This Project

Only `deploy/docker-compose.yml` needs modification:
- Add external network `new-api_new-api-network`
- Services that need cross-stack access (`audit-gateway`, `analysis-worker`) join this network

## Deployment Steps

1. SSH to server, install Docker if needed
2. Create directory structure
3. Deploy new-api: clone compose file, configure, `docker compose up -d`
4. Wait for new-api healthy
5. Deploy gateway: rsync code, configure .env, `docker compose up -d`
6. Run database migrations
7. Verify both services

## Rollback

- `docker compose down` in either directory to stop that stack
- Data persists in Docker named volumes
