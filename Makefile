.PHONY: test test-race vet web-test web-test-syntax run build

test:
	go test ./...
	$(MAKE) web-test

web-test:
	npm --prefix frontend test

web-test-syntax:
	npm --prefix frontend run test:syntax

test-race:
	go test -race ./...

vet:
	go vet ./...

run:
	go run ./cmd/checknetwork-api

build:
	go build -trimpath -o bin/checknetwork-api ./cmd/checknetwork-api
