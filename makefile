.PHONY: up down build run test test-race lint vet tidy migrate-up migrate-down migrate-new fmt

up:
	docker compose up -d --build

down:
	docker compose down

build:
	go build -trimpath -o bin/api ./cmd/api

run:
	go run ./cmd/api

test:
	go test ./...

test-race:
	go test -race -shuffle=on ./...

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
