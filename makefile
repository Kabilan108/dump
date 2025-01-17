
build:
	GO111MODULE=on go build -o dump .

install:
	GO111MODULE=on go install

clean:
	rm -f dump
