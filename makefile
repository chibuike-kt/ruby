.PHONY: up down build run test test-race lint vet tidy migrate-up migrate-down migrate-new fmt

up:
	docker compose up -d --build

down:
	docker compose down

build:
	go build -trimpath -o bin/api ./cmd/api

run:
	go run ./cmd/api

# -p 1: internal packages run integration tests against one shared
# Postgres instance (see internal/dbtest), not per-package databases, so
# package test binaries must not run concurrently or they'll truncate
# each other's fixtures mid-test.
test:
	go test -p 1 ./...

test-race:
	go test -race -shuffle=on -p 1 ./...

lint:
	golangci-lint run

vet:
	go vet ./...

tidy:
	go mod tidy

fmt:
	gofmt -l -w .

migrate-up:
	migrate -path ./migrations -database "$$DATABASE_URL" up

migrate-down:
	migrate -path ./migrations -database "$$DATABASE_URL" down 1

migrate-new:
	migrate create -ext sql -dir ./migrations -seq $(name)
