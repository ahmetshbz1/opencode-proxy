.PHONY: build run clean

build:
	go build -o opencode-proxy .

run:
	./opencode-proxy

clean:
	rm -f opencode-proxy