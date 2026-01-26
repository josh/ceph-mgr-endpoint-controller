# ceph-mgr-endpoint-controller

A Kubernetes controller that discovers Ceph Manager services (Dashboard, Prometheus) and creates corresponding Kubernetes EndpointSlices for service discovery.

## Overview

This controller connects to a Ceph cluster via RADOS, queries the manager for available services, and synchronizes their addresses as Kubernetes EndpointSlices. This enables Kubernetes Services to route traffic to Ceph services without manual endpoint management.

## Usage

```
ceph-mgr-endpoint-controller [flags]

Flags:
  -namespace string        Kubernetes namespace for EndpointSlices (default "ceph")
  -service string          Parent Service name for EndpointSlices
  -dashboard-slice string  EndpointSlice name for dashboard
  -prometheus-slice string EndpointSlice name for prometheus
  -interval duration       Polling interval (0 = run once and exit)
  -kubeconfig string       Path to kubeconfig (uses in-cluster config if empty)
  -debug                   Enable debug logging
```

## Installation

```bash
helm install ceph-mgr-endpoint-controller ./charts/ceph-mgr-endpoint-controller \
  --set cephConfig.secret.name=your-ceph-keyring
```

See [values.yaml](./charts/ceph-mgr-endpoint-controller/values.yaml) for configuration options.

## Requirements

- Ceph configuration (`/etc/ceph/ceph.conf`) and client keyring must be accessible
- Keyring must have permission to run `ceph mgr services`
