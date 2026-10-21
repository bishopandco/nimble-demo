.PHONY: dev run test check build frontend frontend-test frontend-build

dev:
	go run github.com/air-verse/air@v1.62.0 -c .air.toml

run:
	go run ./cmd/api

test:
	go test -race -cover ./...

check:
	go vet ./...
	@test -z "$$(gofmt -l cmd internal)"

build:
	go build -o bin/api ./cmd/api

frontend:
	npm --prefix frontend run dev

frontend-test:
	npm --prefix frontend test

frontend-build:
	npm --prefix frontend run build
