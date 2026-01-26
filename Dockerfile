FROM golang:1.25.5-alpine3.21@sha256:b4dbd292a0852331c89dfd64e84d16811f3e3aae4c73c13d026c4d200715aff6 AS builder

RUN apk add --no-cache build-base linux-headers ceph19-dev

WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY main.go ./
RUN CGO_ENABLED=1 go build -trimpath -ldflags="-s -w" -o ceph-mgr-endpoint-controller .

FROM alpine:3.21@sha256:5405e8f36ce1878720f71217d664aa3dea32e5e5df11acbf07fc78ef5661465b

RUN apk add --no-cache librados19

COPY --from=builder /app/ceph-mgr-endpoint-controller /usr/local/bin/

LABEL org.opencontainers.image.source="https://github.com/josh/ceph-mgr-endpoint-controller"
LABEL org.opencontainers.image.description="Ceph MGR Endpoint Controller"
LABEL org.opencontainers.image.licenses="MIT"

ENTRYPOINT ["ceph-mgr-endpoint-controller"]
