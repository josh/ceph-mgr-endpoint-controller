FROM golang:1.25.5-alpine3.23@sha256:ac09a5f469f307e5da71e766b0bd59c9c49ea460a528cc3e6686513d64a6f1fb AS builder

RUN apk add --no-cache build-base=0.5-r3 linux-headers=6.16.12-r0 ceph19-dev=19.2.3-r3

WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY main.go ./
RUN CGO_ENABLED=1 go build -trimpath -ldflags="-s -w" -o ceph-mgr-endpoint-controller .

FROM alpine:3.23@sha256:865b95f46d98cf867a156fe4a135ad3fe50d2056aa3f25ed31662dff6da4eb62

RUN apk add --no-cache librados19=19.2.3-r3

COPY --from=builder /app/ceph-mgr-endpoint-controller /usr/local/bin/
RUN ceph-mgr-endpoint-controller build-check

LABEL org.opencontainers.image.source="https://github.com/josh/ceph-mgr-endpoint-controller"
LABEL org.opencontainers.image.description="Ceph MGR Endpoint Controller"
LABEL org.opencontainers.image.licenses="MIT"

USER 65534

ENTRYPOINT ["ceph-mgr-endpoint-controller"]
