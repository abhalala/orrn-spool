FROM golang:1.22-alpine AS builder

RUN apk add --no-cache git ca-certificates build-base

WORKDIR /app

COPY go.mod ./
RUN go mod download || true

COPY . .

RUN CGO_ENABLED=1 GOOS=linux go build -ldflags="-w -s" -o /spool ./cmd/spool

FROM alpine:3.19

RUN apk add --no-cache ca-certificates age tzdata sqlite

WORKDIR /app

COPY --from=builder /spool /app/spool

COPY web/ /app/web/

RUN mkdir -p /app/data

EXPOSE 8080

HEALTHCHECK --interval=30s --timeout=10s --start-period=5s --retries=3 \
  CMD wget --no-verbose --tries=1 --spider http://localhost:8080/health || exit 1

ENTRYPOINT ["/app/spool"]
CMD ["--config", "/app/config.yaml"]