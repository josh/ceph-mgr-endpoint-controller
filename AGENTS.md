# AGENTS.md

Kubernetes controller that keeps service endpoints pointed to the active Ceph manager.

## Tech Stack

- Go 1.25.5
- go-ceph v0.37.0 (RADOS client, requires CGO)
- Kubernetes client-go v0.35.0
- librados (system library)

## Build

Requires librados-dev for CGO compilation:

```
# Debian/Ubuntu
apt-get install librados-dev

# Build
CGO_ENABLED=1 go build -o ceph-mgr-endpoint-controller .

# Docker
docker build -t ceph-mgr-endpoint-controller .
```

## Project Structure

Single-file Go application:

- `main.go` - All application code
- `Dockerfile` - Multi-stage build with librados

## Code Patterns

- Standard library `flag` for CLI args
- `log/slog` for structured logging
- Kubernetes client-go for API interactions
- go-ceph RADOS for Ceph communication

## Boundaries

- Never modify `/etc/ceph/` paths or credentials handling
- Keep as single-file application unless complexity requires splitting
- Maintain CGO requirement (go-ceph needs it)
