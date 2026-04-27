# Development

## Local Services

Start PostgreSQL and Redis:

```bash
docker compose -f deploy/docker-compose.yml up -d
```

## Tests

```bash
make test
```

## Python Worker

The analysis worker uses uv for Python dependency management:

```bash
cd workers/analysis_worker
uv sync
uv run python main.py < contract_example.json
```

## Gateway Environment

Copy `.env.example` to `.env.local` and set `NEW_API_BASE_URL` to a running new-api instance.
Export those values into your shell before starting the Go binary; `make run` reads process environment variables, not `.env.local` directly:

```bash
set -a
source .env.local
set +a
make run
```

The gateway must never log or persist plaintext API keys. Tests should assert that API-key handling only stores HMAC fingerprints and token metadata.
