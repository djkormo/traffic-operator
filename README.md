# traffic-operator

`traffic-operator` is a Kubernetes operator designed to validate network connectivity and policy enforcement within a Kubernetes cluster and towards external endpoints.

It continuously reconciles `TrafficCheck` custom resources by orchestrating ephemeral probe jobs and reporting target reachability directly in the resource status.

## Architecture & How It Works

```
                                  +---------------------+
                                  |    TrafficCheck     |
                                  |   Custom Resource   |
                                  +----------+----------+
                                             |
                                             v
+------------------------+        +----------+----------+
|  Kubernetes API Server | <----> |   traffic-operator  |
+------------------------+        |  Controller Manager |
                                  +----------+----------+
                                             |
                                    Creates ephemeral Job
                                             |
                                             v
                                  +----------+----------+
                                  |    Probe Job / Pod  |
                                  | (nicolaka/netshoot) |
                                  +----------+----------+
                                             |
                    +------------------------+------------------------+
                    |                                                 |
                    v                                                 v
        +-----------+-----------+                         +-----------+-----------+
        |  In-Cluster Service   |                         |   External Endpoint   |
        |  (TCP / HTTP / HTTPS) |                         |  (TCP / HTTP / HTTPS) |
        +-----------------------+                         +-----------------------+
```

1. **User creates a `TrafficCheck` CR** defining targets (internal Kubernetes services or external endpoints).
2. **`traffic-operator` reconciles the CR** and spins up a targeted probe Job using a lightweight networking container (`nicolaka/netshoot` by default).
3. **Probe executes checks**: Supports TCP port connectivity, UDP, HTTP, and HTTPS endpoints with status code assertion and TLS verification controls.
4. **Operator updates CR status**: Collects results (latency, status codes, failure messages) and sets the `Ready` condition (`AllTargetsReachable` or `TargetsUnreachable`).
5. **Job cleanup**: Ephemeral jobs and pods are automatically cleaned up via Kubernetes `ttlSecondsAfterFinished`.

---

## Installation via Helm

The `traffic-operator` Helm chart is published and hosted via GitHub Pages (`gh-pages` branch) at:
**`https://djkormo.github.io/traffic-operator`**

### Option 1: Install from GitHub Pages Repository (Recommended)

1. **Add the Helm chart repository**:
   ```bash
   helm repo add traffic-operator https://djkormo.github.io/traffic-operator
   helm repo update
   ```

2. **Search for available versions**:
   ```bash
   helm search repo traffic-operator
   ```

3. **Install the operator**:
   ```bash
   helm install traffic-operator traffic-operator/traffic-operator \
     --namespace traffic-operator-system \
     --create-namespace
   ```

4. **(Optional) Install or upgrade with custom values**:
   ```bash
   helm upgrade --install traffic-operator traffic-operator/traffic-operator \
     --namespace traffic-operator-system \
     --create-namespace \
     -f my-values.yaml
   ```

### Option 2: Install from Local Chart Directory

If you cloned this repository locally, you can install directly from the [`charts/traffic-operator`](file:///mnt/c/development/kubernetes/traffic-operator/charts/traffic-operator) directory:

```bash
helm install traffic-operator ./charts/traffic-operator \
  --namespace traffic-operator-system \
  --create-namespace
```

### Verify Deployment

```bash
kubectl get pods -n traffic-operator-system -l control-plane=controller-manager
```

For full Helm chart configuration options and values reference, see the [Chart README](file:///mnt/c/development/kubernetes/traffic-operator/charts/traffic-operator/README.md).

---

## Installation via Kustomize

Alternatively, deploy using Kustomize:

```bash
make deploy IMG=djkormo/traffic-operator:0.0.1
```

---

## Usage Example

Create a `TrafficCheck` custom resource:

```yaml
apiVersion: network.djkormo.github.io/v1alpha1
kind: TrafficCheck
metadata:
  name: connectivity-check
  namespace: default
spec:
  podLabels:
    app: probe-client
  probeImage: nicolaka/netshoot:latest
  ttlSecondsAfterFinished: 120
  targets:
    - name: external-api
      type: External
      protocol: HTTPS
      host: api.github.com
      port: 443
      path: /
      expectedStatusCode: 200
      timeoutSeconds: 5
    - name: internal-kubernetes-api
      type: KubernetesService
      protocol: HTTPS
      host: kubernetes
      namespace: default
      port: 443
      path: /livez
      insecureSkipVerify: true
      expectedStatusCode: 401
      timeoutSeconds: 3
```

Check results:

```bash
kubectl get trafficcheck connectivity-check
kubectl describe trafficcheck connectivity-check
```

---

## Development & Helm Makefile Targets

| Target | Description |
|---|---|
| `make build` | Build the operator manager binary |
| `make manifests` | Generate CRD and RBAC manifests |
| `make helm-update-crds` | Regenerate and copy CRDs to `charts/traffic-operator/crds/` |
| `make helm-lint` | Lint the Helm chart |
| `make helm-template` | Render the Helm templates |
| `make helm-docs` | Regenerate Helm documentation using `helm-docs` |
| `make helm-package` | Package the Helm chart into `dist/` |
