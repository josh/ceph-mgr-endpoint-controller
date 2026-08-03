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

| Value                               | Description                             | Default                                     |
| ----------------------------------- | --------------------------------------- | ------------------------------------------- |
| `image.repository`                  | Container image repository              | `ghcr.io/josh/ceph-mgr-endpoint-controller` |
| `image.tag`                         | Container image tag (chart `appVersion`) | `""`                                       |
| `image.pullPolicy`                  | Image pull policy                       | `IfNotPresent`                              |
| `secret.name`                       | Secret name containing Ceph credentials | `ceph-mgr-endpoint-controller-secret`       |
| `secret.userID`                     | Secret key for user ID                  | `userID`                                    |
| `secret.userKey`                    | Secret key for user key                 | `userKey`                                   |
| `config.create`                     | Create a ConfigMap for ceph.conf        | `true`                                      |
| `config.name`                       | ConfigMap name for ceph.conf            | `ceph-config`                               |
| `config.clusterID`                  | Ceph cluster FSID                       | `""`                                        |
| `config.monitors`                   | List of monitor addresses               | `[]`                                        |
| `controller.serviceName`            | Parent Service name for EndpointSlices  | `ceph-mgr`                                  |
| `controller.dashboardSliceName`     | EndpointSlice name for dashboard        | `ceph-mgr-dashboard`                        |
| `controller.prometheusSliceName`    | EndpointSlice name for prometheus       | `ceph-mgr-prometheus`                       |
| `controller.interval`               | Polling interval                        | `30s`                                       |
| `controller.maxConsecutiveFailures` | Consecutive sync failures before exit   | `10`                                        |
| `controller.debug`                  | Enable debug logging                    | `false`                                     |
| `service.create`                    | Create a Service for the EndpointSlices | `true`                                      |
| `service.ports.dashboard`           | Dashboard service port                  | `8443`                                      |
| `service.ports.prometheus`          | Prometheus service port                 | `9283`                                      |
| `serviceAccount.create`             | Create a ServiceAccount                 | `true`                                      |
| `serviceAccount.name`               | ServiceAccount name override            | `""`                                        |
| `networkPolicy.ingress.enabled`     | Manage ingress to the controller pod    | `false`                                     |
| `networkPolicy.ingress.rules`       | Kubernetes ingress rules                | `[]`                                        |
| `networkPolicy.egress.enabled`      | Manage egress from the controller pod   | `false`                                     |
| `networkPolicy.egress.rules`        | Kubernetes egress rules                 | `[]`                                        |
| `resources.limits.cpu`              | Container CPU limit                     | `50m`                                       |
| `resources.limits.memory`           | Container memory limit                  | `64Mi`                                      |
| `resources.requests.cpu`            | Container CPU request                   | `10m`                                       |
| `resources.requests.memory`         | Container memory request                | `32Mi`                                      |

See [values.yaml](./charts/ceph-mgr-endpoint-controller/values.yaml) for all options.

## Network policy

Network policy is disabled by default so upgrades preserve the chart's existing connectivity. Ingress and egress are controlled independently. Enabling a direction with an empty `rules` list denies all traffic in that direction.

The controller does not listen for inbound connections, so it requires no ingress traffic. The selectorless `ceph-mgr` Service exposes the dashboard on port 8443 and Prometheus on port 9283 by default, then sends clients directly to the dynamically discovered external Ceph Manager ports in its EndpointSlices. That traffic does not pass through the controller pod and is not governed by this NetworkPolicy; the controller also does not connect to the discovered dashboard or Prometheus endpoints. Control client access with policies on the client workloads and with the network controls protecting the external Ceph endpoints.

The controller requires the following egress:

- TCP to the Kubernetes API endpoint used by the pod's in-cluster configuration.
- TCP to every Ceph monitor in `mon_host`. Ceph commonly uses messenger v2 port 3300 and legacy messenger v1 port 6789, but deployments can use different ports.
- UDP and TCP to the cluster DNS service when any required endpoint is specified by hostname.

The exact API, monitor, and DNS destinations depend on the cluster and CNI. Supply native Kubernetes NetworkPolicy rules for those destinations. For example:

```yaml
networkPolicy:
  ingress:
    enabled: true
    rules: []
  egress:
    enabled: true
    rules:
      # Kubernetes API; replace the CIDR and port for your cluster.
      - to:
          - ipBlock:
              cidr: 10.0.0.1/32
        ports:
          - protocol: TCP
            port: 443
      # Ceph monitors; replace the CIDR and ports for your cluster.
      - to:
          - ipBlock:
              cidr: 192.168.1.0/24
        ports:
          - protocol: TCP
            port: 3300
          - protocol: TCP
            port: 6789
      # DNS; adjust the selectors if your DNS pods use different labels.
      - to:
          - namespaceSelector:
              matchLabels:
                kubernetes.io/metadata.name: kube-system
            podSelector:
              matchLabels:
                k8s-app: kube-dns
        ports:
          - protocol: UDP
            port: 53
          - protocol: TCP
            port: 53
```

Image pulls and the mounting of Secrets, ConfigMaps, and service account credentials are performed by Kubernetes node components and do not require controller pod rules. The chart does not create an Ingress resource.

## Requirements

- Ceph configuration (`/etc/ceph/ceph.conf`) and client keyring must be accessible
- Keyring must have permission to run `ceph mgr services`
