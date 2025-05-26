build/dump: $(shell find . -name '*.go')
	CGO_ENABLED=0 go build -ldflags="-s -w" -o build/dump .

build/dump-linux-amd64: $(shell find . -name '*.go')
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -ldflags="-s -w" -o build/dump-linux-amd64 .

build: build/dump

install:
	go install

deps:
	go mod tidy

clean:
	rm -f build/dump
	rm -rf dump-linux-amd64
	rm -f dump-linux-amd64.tar.gz

run: build
	./build/dump

release: build/dump-linux-amd64
	cp build/dump-linux-amd64 dump
	tar czf dump-linux-amd64.tar.gz -C build dump
	rm -rf dump
