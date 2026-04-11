.PHONY: build run test clean vet lint

build:
	go build -o opencode-proxy .

run:
	./opencode-proxy

test:
	go test -race -count=1 ./...

vet:
	go vet ./...

lint: vet test

clean:
	rm -f opencode-proxy
