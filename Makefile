.PHONY: test run tidy smoke

test:
	go test ./...

run:
	go run ./cmd/audit-gateway

tidy:
	go mod tidy

smoke:
	./scripts/smoke_proxy.sh
