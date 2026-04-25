.PHONY: test run tidy

test:
	go test ./...

run:
	go run ./cmd/audit-gateway

tidy:
	go mod tidy
