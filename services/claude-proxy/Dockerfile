FROM golang:1.24-alpine AS builder

WORKDIR /src
ENV GOTOOLCHAIN=auto
COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /bin/claude-proxy ./cmd/proxy

# ─────────────────────────────────────────────────────────
FROM alpine:3.20

RUN apk add --no-cache ca-certificates tzdata
WORKDIR /app

COPY --from=builder /bin/claude-proxy ./claude-proxy
# config.json được mount từ host để dễ chỉnh sửa mà không cần rebuild
COPY config.json ./config.json

EXPOSE 20129

CMD ["./claude-proxy", "--config", "/app/config.json"]
