BINDIR ?= $(if $(XDG_BIN_HOME),$(XDG_BIN_HOME),$(HOME)/.local/bin)

.PHONY: install uninstall test integration lint build cover all

install:
	@mkdir -p "$(BINDIR)"
	GOBIN="$(BINDIR)" go install ./cmd/munsu
	@echo "installed munsu to $(BINDIR)/munsu"

uninstall:
	rm -f "$(BINDIR)/munsu"

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
