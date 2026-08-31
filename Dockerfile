FROM golang:1.27.0-alpine3.23@sha256:3747dcba41c8b0db3211fda4db61638b980e17ac5bb3c94460a975a9cfe19395 AS builder

RUN apk add --no-cache build-base=0.5-r3 linux-headers=6.16.12-r0 ceph19-dev=19.2.3-r3

WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY main.go ./
RUN CGO_ENABLED=1 go build -trimpath -ldflags="-s -w" -o ceph-mgr-endpoint-controller .

FROM alpine:3.24.1@sha256:28bd5fe8b56d1bd048e5babf5b10710ebe0bae67db86916198a6eec434943f8b

RUN apk add --no-cache librados19=19.2.4-r1

COPY --from=builder /app/ceph-mgr-endpoint-controller /usr/local/bin/
RUN ceph-mgr-endpoint-controller version

LABEL org.opencontainers.image.source="https://github.com/josh/ceph-mgr-endpoint-controller"
LABEL org.opencontainers.image.description="Ceph MGR Endpoint Controller"
LABEL org.opencontainers.image.licenses="MIT"

USER 65534:65534

ENTRYPOINT ["ceph-mgr-endpoint-controller"]
