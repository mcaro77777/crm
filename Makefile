.PHONY: test build run check fmt

test:
	go test ./...

build:
	go build -o bin/crm-api ./cmd/api

run:
	go run ./cmd/api serve

check:
	go vet ./...
	go test ./...

fmt:
	gofmt -w cmd internal
