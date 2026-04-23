.PHONY: build health-ui run test clean vet lint

build: health-ui
	go build -o opencode-proxy .

health-ui:
	bun run --cwd health-ui build
	rm -rf internal/proxy/health_assets/*
	cp -R health-ui/dist/. internal/proxy/health_assets/

run:
	./opencode-proxy

test:
	go test -race -count=1 ./...

vet:
	go vet ./...

lint: vet test

clean:
	rm -f opencode-proxy
