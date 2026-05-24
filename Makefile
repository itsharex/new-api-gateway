PLATFORM := $(shell uname -m)

ifeq ($(PLATFORM),arm64)
DEV_COMPOSE = -f deploy/docker-compose.yml -f deploy/docker-compose.arm.yml
else
DEV_COMPOSE = -f deploy/docker-compose.yml
endif

.PHONY: test run tidy smoke dev

test:
	go test ./...

run:
	go run ./cmd/audit-gateway

tidy:
	go mod tidy

smoke:
	./scripts/smoke_proxy.sh

dev:
	docker compose $(DEV_COMPOSE) --env-file .env.local up
