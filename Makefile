.PHONY: test integration lint build cover all

test:
	go test -race -count=1 ./...

integration:
	go test -tags=integration -count=1 ./...

lint:
	go vet ./...

build:
	go build ./...

cover:
	go test -coverprofile=cover.out -covermode=atomic ./...
	go tool cover -func=cover.out | tail -1

all: lint build test integration cover
