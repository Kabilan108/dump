
build/dump: $(shell find . -name '*.go')
	 CGO_ENABLED=0 go build -ldflags="-s -w" -o build/dump .

build: build/dump

install:
	 go install

deps:
	 go mod tidy

clean:
	rm -f build/dump

run: build
	./build/dump
