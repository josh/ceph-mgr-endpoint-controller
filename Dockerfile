FROM golang:1.27.4-alpine3.24@sha256:70bd7ddea8b6cbe5d6943cde279d6b67feb10cc924fb9100d1d8757c1575b280 AS builder

RUN apk add --no-cache build-base=0.5-r4 linux-headers=7.0.0-r1 ceph19-dev=19.2.4-r1

WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY main.go ./
RUN CGO_ENABLED=1 go build -trimpath -ldflags="-s -w" -o ceph-mgr-endpoint-controller .

FROM alpine:3.24.4@sha256:f59abe656b692d901e184be3445e084786602108599d2a51d229c0f5a48c71bf

RUN apk add --no-cache librados19=19.2.4-r1

COPY --from=builder /app/ceph-mgr-endpoint-controller /usr/local/bin/
RUN ceph-mgr-endpoint-controller version

LABEL org.opencontainers.image.source="https://github.com/josh/ceph-mgr-endpoint-controller"
LABEL org.opencontainers.image.description="Ceph MGR Endpoint Controller"
LABEL org.opencontainers.image.licenses="MIT"

USER 65534:65534

ENTRYPOINT ["ceph-mgr-endpoint-controller"]
