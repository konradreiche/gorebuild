GO ?= go

.PHONY: build install test fmt clean

build:
	$(GO) build -o bin/gorebuild ./cmd/gorebuild

install:
	$(GO) install ./cmd/gorebuild

test:
	$(GO) test ./...

fmt:
	$(GO) fmt ./...

clean:
	rm -rf bin
