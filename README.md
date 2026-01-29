# ceph-mgr-endpoint-controller

A Kubernetes controller that discovers Ceph Manager services (Dashboard, Prometheus) and creates corresponding Kubernetes EndpointSlices for service discovery.

## Overview

This controller connects to a Ceph cluster via RADOS, queries the manager for available services, and synchronizes their addresses as Kubernetes EndpointSlices. This enables Kubernetes Services to route traffic to Ceph services without manual endpoint management.

## Installation

```bash
helm install ceph-mgr-endpoint-controller ./charts/ceph-mgr-endpoint-controller \
  --set ceph.keyring.name=your-ceph-keyring
```

## Configuration

| Value                          | Description                             | Default               |
| ------------------------------ | --------------------------------------- | --------------------- |
| `controller.mode`              | Run as `deployment` or `cronjob`        | `deployment`          |
| `controller.serviceName`       | Parent Service name for EndpointSlices  | `ceph-mgr`            |
| `controller.dashboardSliceName`| EndpointSlice name for dashboard        | `ceph-mgr-dashboard`  |
| `controller.prometheusSliceName`| EndpointSlice name for prometheus      | `ceph-mgr-prometheus` |
| `controller.interval`          | Polling interval                        | `30s`                 |
| `controller.debug`             | Enable debug logging                    | `false`               |
| `service.create`               | Create a Service for the EndpointSlices | `true`                |
| `service.ports.dashboard`      | Dashboard service port                  | `8443`                |
| `service.ports.prometheus`     | Prometheus service port                 | `9283`                |
| `ceph.config.name`             | ConfigMap containing ceph.conf          | `ceph-config`         |
| `ceph.keyring.name`            | Secret containing Ceph keyring          | `ceph-keyring`        |

See [values.yaml](./charts/ceph-mgr-endpoint-controller/values.yaml) for all options.

## Requirements

- Ceph configuration (`/etc/ceph/ceph.conf`) and client keyring must be accessible
- Keyring must have permission to run `ceph mgr services`
