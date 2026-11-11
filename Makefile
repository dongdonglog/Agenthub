.PHONY: run build test vet fmt tidy docker

BIN := bin/agentd

run:
	go run ./cmd/agentd

build:
	CGO_ENABLED=0 go build -trimpath -ldflags "-s -w" -o $(BIN) ./cmd/agentd

test:
	go test ./...

vet:
	go vet ./...

fmt:
	gofmt -l -w .

tidy:
	go mod tidy

docker:
	docker build -t agenthub:dev .
