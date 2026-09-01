.PHONY: test test-race vet run build

test:
	go test ./...

test-race:
	go test -race ./...

vet:
	go vet ./...

run:
	go run ./cmd/checknetwork-api

build:
	go build -trimpath -o bin/checknetwork-api ./cmd/checknetwork-api

