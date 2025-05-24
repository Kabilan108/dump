
build/dump: $(shell find . -name '*.go')
	GO111MODULE=on CGO_ENABLED=0 go build -ldflags="-s -w" -o build/dump .

build: build/dump

install:
	GO111MODULE=on go install

deps:
	GO111MODULE=on go mod tidy

clean:
	rm -f build/dump

run: build
	./build/dump
