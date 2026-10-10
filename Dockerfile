FROM golang:1.27.1-alpine3.24@sha256:8a5910f31396cd4d89662f56c68b3ae31d374308270a1c3bd96672ee5ed43414 AS builder

RUN apk add --no-cache build-base=0.5-r4 linux-headers=7.0.0-r1 ceph19-dev=19.2.4-r1

WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY main.go ./
RUN CGO_ENABLED=1 go build -trimpath -ldflags="-s -w" -o ceph-mgr-endpoint-controller .

FROM alpine:3.24.2@sha256:294b683cb724975bec92580e1e685676bd4b50bda910ddb8c51d4cabeaec77e6

RUN apk add --no-cache librados19=19.2.4-r1

COPY --from=builder /app/ceph-mgr-endpoint-controller /usr/local/bin/
RUN ceph-mgr-endpoint-controller version

LABEL org.opencontainers.image.source="https://github.com/josh/ceph-mgr-endpoint-controller"
LABEL org.opencontainers.image.description="Ceph MGR Endpoint Controller"
LABEL org.opencontainers.image.licenses="MIT"

USER 65534:65534

ENTRYPOINT ["ceph-mgr-endpoint-controller"]
