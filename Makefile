.PHONY: build run test clean docker-build docker-run

BINARY=claude-code-proxy

build:
	go build -o $(BINARY) ./cmd/claude-code-proxy

run: build
	./$(BINARY)

test:
	go test ./... -v

clean:
	rm -f $(BINARY)

docker-build:
	docker build -t claude-code-proxy .

docker-run: docker-build
	docker run -p 42069:42069 \
		-v ~/.claude:/root/.claude \
		-v ~/.claude-code-proxy:/root/.claude-code-proxy \
		claude-code-proxy
