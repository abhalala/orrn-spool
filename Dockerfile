FROM golang:1.22-alpine AS builder

RUN apk add --no-cache git ca-certificates build-base

WORKDIR /app

COPY go.mod go.sum ./
RUN go mod download

COPY . .
ARG VERSION=dev

RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w -X main.Version=${VERSION}" -o /orrn-edge ./cmd/orrn-edge

FROM alpine:3.19

RUN apk add --no-cache ca-certificates tzdata sqlite && \
    adduser -D -H -u 1000 -h /var/lib/orrn-edge orrn-edge && \
    install -d -m 0700 -o orrn-edge -g orrn-edge /var/lib/orrn-edge

WORKDIR /var/lib/orrn-edge

COPY --from=builder /orrn-edge /usr/local/bin/orrn-edge

USER orrn-edge
EXPOSE 8080

HEALTHCHECK --interval=30s --timeout=5s --start-period=5s --retries=3 \
  CMD ["/usr/local/bin/orrn-edge", "healthcheck", "--url", "http://127.0.0.1:8080/healthz"]

ENTRYPOINT ["/usr/local/bin/orrn-edge"]
CMD ["run", "--state-dir", "/var/lib/orrn-edge", "--admin-addr", "127.0.0.1:8081", "--fallback-addr", ":8080"]
