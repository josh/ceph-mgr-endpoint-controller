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

| Value                                           | Description                             | Default                                     |
| ----------------------------------------------- | --------------------------------------- | ------------------------------------------- |
| `image.repository`                              | Container image repository              | `ghcr.io/josh/ceph-mgr-endpoint-controller` |
| `image.tag`                                     | Container image tag                     | `""`                                        |
| `image.pullPolicy`                              | Image pull policy                       | `IfNotPresent`                              |
| `ceph.secret.name`                              | Secret name containing Ceph credentials | `ceph-mgr-endpoint-controller-secret`       |
| `ceph.secret.userID`                            | Secret key for user ID                  | `userID`                                    |
| `ceph.secret.userKey`                           | Secret key for user key                 | `userKey`                                   |
| `ceph.config.create`                            | Create a ConfigMap for ceph.conf        | `true`                                      |
| `ceph.config.name`                              | ConfigMap name for ceph.conf            | `ceph-config`                               |
| `ceph.config.clusterID`                         | Ceph cluster FSID                       | `""`                                        |
| `ceph.config.monitors`                          | List of monitor addresses               | `[]`                                        |
| `controller.mode`                               | Run as `deployment` or `cronjob`        | `deployment`                                |
| `controller.serviceName`                        | Parent Service name for EndpointSlices  | `ceph-mgr`                                  |
| `controller.dashboardSliceName`                 | EndpointSlice name for dashboard        | `ceph-mgr-dashboard`                        |
| `controller.prometheusSliceName`                | EndpointSlice name for prometheus       | `ceph-mgr-prometheus`                       |
| `controller.interval`                           | Polling interval                        | `30s`                                       |
| `controller.debug`                              | Enable debug logging                    | `false`                                     |
| `controller.cronjob.schedule`                   | CronJob schedule                        | `*/5 * * * *`                               |
| `controller.cronjob.concurrencyPolicy`          | CronJob concurrency policy              | `Forbid`                                    |
| `controller.cronjob.successfulJobsHistoryLimit` | Successful job history limit            | `1`                                         |
| `controller.cronjob.failedJobsHistoryLimit`     | Failed job history limit                | `1`                                         |
| `service.create`                                | Create a Service for the EndpointSlices | `true`                                      |
| `service.ports.dashboard`                       | Dashboard service port                  | `8443`                                      |
| `service.ports.prometheus`                      | Prometheus service port                 | `9283`                                      |
| `serviceAccount.create`                         | Create a ServiceAccount                 | `true`                                      |
| `serviceAccount.name`                           | ServiceAccount name override            | `""`                                        |
| `resources`                                     | Container resource requests/limits      | `{}`                                        |
| `nodeSelector`                                  | Node selector constraints               | `{}`                                        |
| `tolerations`                                   | Pod tolerations                         | `[]`                                        |
| `affinity`                                      | Pod affinity rules                      | `{}`                                        |

See [values.yaml](./charts/ceph-mgr-endpoint-controller/values.yaml) for all options.

## Requirements

- Ceph configuration (`/etc/ceph/ceph.conf`) and client keyring must be accessible
- Keyring must have permission to run `ceph mgr services`
