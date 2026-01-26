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

## Kubernetes Deployment

```yaml
apiVersion: v1
kind: Namespace
metadata:
  name: ceph
---
apiVersion: v1
kind: ServiceAccount
metadata:
  name: ceph-mgr-endpoint-controller
  namespace: ceph
---
apiVersion: rbac.authorization.k8s.io/v1
kind: Role
metadata:
  name: ceph-mgr-endpoint-controller
  namespace: ceph
rules:
  - apiGroups: ["discovery.k8s.io"]
    resources: ["endpointslices"]
    verbs: ["get", "create", "update"]
---
apiVersion: rbac.authorization.k8s.io/v1
kind: RoleBinding
metadata:
  name: ceph-mgr-endpoint-controller
  namespace: ceph
roleRef:
  apiGroup: rbac.authorization.k8s.io
  kind: Role
  name: ceph-mgr-endpoint-controller
subjects:
  - kind: ServiceAccount
    name: ceph-mgr-endpoint-controller
    namespace: ceph
---
apiVersion: apps/v1
kind: Deployment
metadata:
  name: ceph-mgr-endpoint-controller
  namespace: ceph
spec:
  replicas: 1
  selector:
    matchLabels:
      app: ceph-mgr-endpoint-controller
  template:
    metadata:
      labels:
        app: ceph-mgr-endpoint-controller
    spec:
      serviceAccountName: ceph-mgr-endpoint-controller
      containers:
        - name: controller
          image: ghcr.io/josh/ceph-mgr-endpoint-controller:latest
          args:
            - -namespace=ceph
            - -service=ceph-mgr
            - -dashboard-slice=ceph-mgr-dashboard
            - -prometheus-slice=ceph-mgr-prometheus
            - -interval=30s
          volumeMounts:
            - name: ceph-config
              mountPath: /etc/ceph
              readOnly: true
      volumes:
        - name: ceph-config
          projected:
            sources:
              - configMap:
                  name: ceph-config
              - secret:
                  name: ceph-keyring
---
apiVersion: v1
kind: Service
metadata:
  name: ceph-mgr
  namespace: ceph
spec:
  ports:
    - name: dashboard
      port: 8443
      targetPort: dashboard
    - name: prometheus
      port: 9283
      targetPort: prometheus
```

## Requirements

- Ceph configuration (`/etc/ceph/ceph.conf`) and client keyring must be accessible
- Keyring must have permission to run `ceph mgr services`
