FROM golang:1.24-alpine AS builder

WORKDIR /src
# Cache dependencies
ENV GOTOOLCHAIN=auto
COPY go.mod go.sum ./
RUN go mod download

# Build
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /bin/cf-ai-proxy .

# ─────────────────────────────────────────────────────────
FROM alpine:3.20

RUN apk add --no-cache ca-certificates tzdata
WORKDIR /app

COPY --from=builder /bin/cf-ai-proxy ./cf-ai-proxy
# CSV files are mounted at runtime via docker-compose volumes
# accounts.csv và models.csv sẽ được mount từ host

EXPOSE 20127
ENV GIN_MODE=release

CMD ["./cf-ai-proxy"]
