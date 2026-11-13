.PHONY: web build test test-db

web:
	cd web && npm ci && npm run build

build: web
	go build -o bin/agentd ./cmd/agentd

test:
	go test ./...
	cd web && npm test

test-db:
	AGENTHUB_TEST_DATABASE_URL='postgres://postgres:agenthub@127.0.0.1:55433/agenthub_test?sslmode=disable' \
		go test -p 1 ./internal/core ./internal/store -count=1
