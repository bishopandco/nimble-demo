.PHONY: dev dev-build run test check build sidecar frontend frontend-test frontend-build

dev: sidecar
	go run github.com/air-verse/air@v1.62.0 -c .air.toml

run: sidecar
	go run ./cmd/api

test:
	go test -race -cover ./...

check:
	go vet ./...
	@test -z "$$(gofmt -l cmd internal)"

sidecar:
	go build -o bin/bot-sidecar ./cmd/bot-sidecar

dev-build: sidecar
	go build -o ./.air/api ./cmd/api

build: sidecar
	go build -o bin/api ./cmd/api

frontend:
	npm --prefix frontend run dev

frontend-test:
	npm --prefix frontend test

frontend-build:
	npm --prefix frontend run build
