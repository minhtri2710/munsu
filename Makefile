.PHONY: test integration lint build cover all

# Thin targets that delegate to scripts/test.sh. See that file for phase details.

test:
	./scripts/test.sh unit

integration:
	./scripts/test.sh integration

lint:
	./scripts/test.sh lint

build:
	./scripts/test.sh build

cover:
	./scripts/test.sh coverage

all:
	./scripts/test.sh --all
