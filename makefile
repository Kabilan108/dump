.PHONY: build install deps clean test fmt check run

VERSION ?= $(shell git describe --tags --always --dirty)
LDFLAGS := -ldflags "-s -w -X main.version=$(VERSION)"

build/dump: $(shell find . -name '*.go')
	CGO_ENABLED=0 go build $(LDFLAGS) -o build/dump .

build: build/dump

install:
	go install

deps:
	go mod tidy

clean:
	rm -f build/dump

test:
	go test -v -cover ./...

fmt:
	go fmt ./...

check:
	go vet ./...

run: build
