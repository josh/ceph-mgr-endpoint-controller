# ceph-mgr-endpoint-controller

A Kubernetes controller that discovers Ceph Manager services (Dashboard, Prometheus) and creates corresponding Kubernetes EndpointSlices for service discovery.

## Overview

This controller connects to a Ceph cluster via RADOS, queries the manager for available services, and synchronizes their addresses as Kubernetes EndpointSlices. This enables Kubernetes Services to route traffic to Ceph services without manual endpoint management.

## Installation

1. Create a secret containing your Ceph credentials:

```bash
kubectl create secret generic ceph-mgr-endpoint-controller-secret \
 --from-literal=userID="<your-ceph-user>" \
 --from-literal=userKey="<your-ceph-key>"
```

2. Install the chart:

```bash
helm install ceph-mgr-endpoint-controller ./charts/ceph-mgr-endpoint-controller \
 --set config.clusterID="<your-cluster-fsid>" \
 --set config.monitors="{192.168.1.10,192.168.1.11}"
```

## Configuration

| Value                            | Description                             | Default                                     |
| -------------------------------- | --------------------------------------- | ------------------------------------------- |
| `image.repository`               | Container image repository              | `ghcr.io/josh/ceph-mgr-endpoint-controller` |
| `image.tag`                      | Container image tag                     | `""`                                        |
| `image.pullPolicy`               | Image pull policy                       | `IfNotPresent`                              |
| `secret.name`                    | Secret name containing Ceph credentials | `ceph-mgr-endpoint-controller-secret`       |
| `secret.userID`                  | Secret key for user ID                  | `userID`                                    |
| `secret.userKey`                 | Secret key for user key                 | `userKey`                                   |
| `config.create`                  | Create a ConfigMap for ceph.conf        | `true`                                      |
| `config.name`                    | ConfigMap name for ceph.conf            | `ceph-config`                               |
| `config.clusterID`               | Ceph cluster FSID                       | `""`                                        |
| `config.monitors`                | List of monitor addresses               | `[]`                                        |
| `controller.serviceName`         | Parent Service name for EndpointSlices  | `ceph-mgr`                                  |
| `controller.dashboardSliceName`  | EndpointSlice name for dashboard        | `ceph-mgr-dashboard`                        |
| `controller.prometheusSliceName` | EndpointSlice name for prometheus       | `ceph-mgr-prometheus`                       |
| `controller.interval`            | Polling interval                        | `30s`                                       |
| `controller.debug`               | Enable debug logging                    | `false`                                     |
| `service.create`                 | Create a Service for the EndpointSlices | `true`                                      |
| `service.ports.dashboard`        | Dashboard service port                  | `8443`                                      |
| `service.ports.prometheus`       | Prometheus service port                 | `9283`                                      |
| `serviceAccount.create`          | Create a ServiceAccount                 | `true`                                      |
| `serviceAccount.name`            | ServiceAccount name override            | `""`                                        |
| `resources.limits.cpu`           | Container CPU limit                     | `50m`                                       |
| `resources.limits.memory`        | Container memory limit                  | `64Mi`                                      |
| `resources.requests.cpu`         | Container CPU request                   | `10m`                                       |
| `resources.requests.memory`      | Container memory request                | `32Mi`                                      |

See [values.yaml](./charts/ceph-mgr-endpoint-controller/values.yaml) for all options.

## Requirements

- Ceph configuration (`/etc/ceph/ceph.conf`) and client keyring must be accessible
- Keyring must have permission to run `ceph mgr services`
