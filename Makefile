.PHONY: verify test build dev web fmt

verify: fmt
	go vet ./...
	go test ./...
	go build ./...
	cd web && npm ci && npm run typecheck && npm test && npm run build

fmt:
	gofmt -w .

test:
	go test ./...

build:
	cd web && npm ci && npm run build
	go build -o bin/gateway ./cmd/gateway
	go build -o bin/workspace ./cmd/workspace
	go build -o bin/mock-server ./cmd/mock-server
	go build -o bin/data-server ./cmd/data-server
	go build -o bin/github-server ./cmd/github-server
	go build -o bin/sampledb-server ./cmd/sampledb-server
	go build -o bin/files-server ./cmd/files-server

web:
	cd web && npm run dev

dev:
	./scripts/dev.sh
