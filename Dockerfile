FROM golang:1.25.5-trixie AS builder

RUN apt-get update && apt-get install -y --no-install-recommends \
    librados-dev \
    && rm -rf /var/lib/apt/lists/*

WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY main.go ./
RUN CGO_ENABLED=1 go build -o ceph-mgr-endpoint-controller .

FROM debian:trixie-slim

RUN apt-get update && apt-get install -y --no-install-recommends \
    librados2 \
    && rm -rf /var/lib/apt/lists/*

COPY --from=builder /app/ceph-mgr-endpoint-controller /usr/local/bin/

ENTRYPOINT ["ceph-mgr-endpoint-controller"]
