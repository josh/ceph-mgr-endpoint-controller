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

| Value                                         | Description                                       | Default                                     |
| --------------------------------------------- | ------------------------------------------------- | ------------------------------------------- |
| `image.repository`                            | Container image repository                        | `ghcr.io/josh/ceph-mgr-endpoint-controller` |
| `image.tag`                                   | Container image tag (chart `appVersion`)          | `""`                                        |
| `image.pullPolicy`                            | Image pull policy                                 | `IfNotPresent`                              |
| `secret.name`                                 | Secret name containing Ceph credentials           | `ceph-mgr-endpoint-controller-secret`       |
| `secret.userID`                               | Secret key for user ID                            | `userID`                                    |
| `secret.userKey`                              | Secret key for user key                           | `userKey`                                   |
| `config.create`                               | Create a ConfigMap for ceph.conf                  | `true`                                      |
| `config.name`                                 | ConfigMap name for ceph.conf                      | `ceph-config`                               |
| `config.clusterID`                            | Ceph cluster FSID                                 | `""`                                        |
| `config.monitors`                             | List of monitor addresses                         | `[]`                                        |
| `controller.serviceName`                      | Parent Service name for EndpointSlices            | `ceph-mgr`                                  |
| `controller.dashboardSliceName`               | EndpointSlice name for dashboard                  | `ceph-mgr-dashboard`                        |
| `controller.prometheusSliceName`              | EndpointSlice name for prometheus                 | `ceph-mgr-prometheus`                       |
| `controller.interval`                         | Polling interval                                  | `30s`                                       |
| `controller.maxConsecutiveFailures`           | Consecutive sync failures before exit             | `10`                                        |
| `controller.debug`                            | Enable debug logging                              | `false`                                     |
| `service.create`                              | Create a Service for the EndpointSlices           | `true`                                      |
| `service.ports.dashboard`                     | Dashboard service port                            | `8443`                                      |
| `service.ports.prometheus`                    | Prometheus service port                           | `9283`                                      |
| `serviceAccount.create`                       | Create a ServiceAccount                           | `true`                                      |
| `serviceAccount.name`                         | ServiceAccount name override                      | `""`                                        |
| `networkPolicy.ingress.enabled`               | Manage ingress to the controller pod              | `false`                                     |
| `networkPolicy.ingress.rules`                 | Kubernetes ingress rules                          | `[]`                                        |
| `networkPolicy.egress.enabled`                | Manage egress from the controller pod             | `false`                                     |
| `networkPolicy.egress.dns.enabled`            | Allow DNS resolution                              | `true`                                      |
| `networkPolicy.egress.dns.to`                 | Peers for the DNS rule (any if empty)             | `[]`                                        |
| `networkPolicy.egress.kubeApiServer.enabled`  | Allow the Kubernetes API                          | `true`                                      |
| `networkPolicy.egress.kubeApiServer.hosts`    | API server addresses                              | `[]`                                        |
| `networkPolicy.egress.kubeApiServer.port`     | API server port                                   | `6443`                                      |
| `networkPolicy.egress.monitors.enabled`       | Allow the Ceph monitors                           | `true`                                      |
| `networkPolicy.egress.monitors.hosts`         | Monitor addresses                                 | `[]`                                        |
| `networkPolicy.egress.monitors.ports`         | Monitor ports                                     | `[3300, 6789]`                              |
| `networkPolicy.egress.manager.enabled`        | Allow the Ceph manager session                    | `true`                                      |
| `networkPolicy.egress.manager.hosts`          | Manager addresses (defaults to monitor addresses) | `[]`                                        |
| `networkPolicy.egress.manager.portRange.from` | First messenger port                              | `6800`                                      |
| `networkPolicy.egress.manager.portRange.to`   | Last messenger port                               | `7300`                                      |
| `networkPolicy.egress.rules`                  | Additional raw egress rules                       | `[]`                                        |
| `resources.limits.cpu`                        | Container CPU limit                               | `50m`                                       |
| `resources.limits.memory`                     | Container memory limit                            | `64Mi`                                      |
| `resources.requests.cpu`                      | Container CPU request                             | `10m`                                       |
| `resources.requests.memory`                   | Container memory request                          | `32Mi`                                      |

See [values.yaml](./charts/ceph-mgr-endpoint-controller/values.yaml) for all options.

## Network policy

Network policy is disabled by default so upgrades preserve the chart's existing connectivity. Ingress and egress are controlled independently. Enabling a direction with an empty `rules` list denies all traffic in that direction.

The controller does not listen for inbound connections, so it requires no ingress traffic. The selectorless `ceph-mgr` Service exposes the dashboard on port 8443 and Prometheus on port 9283 by default, then sends clients directly to the dynamically discovered external Ceph Manager ports in its EndpointSlices. That traffic does not pass through the controller pod and is not governed by this NetworkPolicy; the controller also does not connect to the discovered dashboard or Prometheus endpoints. Control client access with policies on the client workloads and with the network controls protecting the external Ceph endpoints.

Egress is described as peers. The chart knows which ports each peer uses; you supply only the addresses, which are specific to your cluster:

```yaml
networkPolicy:
  ingress:
    enabled: true
    rules: []
  egress:
    enabled: true
    kubeApiServer:
      hosts: [10.0.0.1, 10.0.0.2, 10.0.0.3]
    monitors:
      hosts: [192.168.1.10, 192.168.1.11, 192.168.1.12]
    manager:
      hosts: [192.168.1.10, 192.168.1.13]
```

Addresses may be bare IPv4 or IPv6 (`/32` and `/128` are appended for you), bracketed IPv6 (`[fd00::1]`), or explicit CIDRs such as `192.168.1.0/24`, which pass through unchanged. Families may be mixed in one list.

Enabling a peer without addresses fails the render rather than producing a policy that silently drops that traffic. Set `enabled: false` on a peer you genuinely do not need, and use `rules` for anything the peers do not cover; those entries are appended verbatim.

The peers correspond to the following requirements:

- **`kubeApiServer`** — TCP to the API endpoint used by the pod's in-cluster configuration. The default port is **6443, not 443**: policies are typically evaluated after the `kubernetes` Service has been DNAT'd, so the rule must name the backend port that the API server actually listens on. Set `port` if yours differs.
- **`monitors`** — TCP to every Ceph monitor in `mon_host`, on messenger v2 port 3300 and legacy messenger v1 port 6789 by default.
- **`manager`** — TCP to the active Ceph manager over the messenger port range 6800-7300. librados opens a session to the manager independently of the `ceph mgr services` command this controller issues, so this is required even though the controller only ever talks to a monitor directly. Without it the session is blocked and retried indefinitely while the controller otherwise appears healthy. A manager or MDS may run on a host that never runs a monitor, so these addresses are configured separately; they default to the monitor addresses.
- **`dns`** — UDP and TCP to the cluster DNS service, needed when any endpoint above is given as a hostname. Restrict it with `dns.to` if you do not want to permit port 53 to any destination.

Image pulls and the mounting of Secrets, ConfigMaps, and service account credentials are performed by Kubernetes node components and do not require controller pod rules. The chart does not create an Ingress resource.

## Requirements

- Ceph configuration (`/etc/ceph/ceph.conf`) and client keyring must be accessible
- Keyring must have permission to run `ceph mgr services`
