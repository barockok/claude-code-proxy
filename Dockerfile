FROM golang:1.25-alpine AS builder

WORKDIR /build
COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o /claude-code-proxy ./cmd/claude-code-proxy

FROM alpine:3.19
RUN apk --no-cache add ca-certificates

COPY --from=builder /claude-code-proxy /usr/local/bin/claude-code-proxy

ENTRYPOINT ["claude-code-proxy"]
