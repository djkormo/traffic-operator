# traffic-operator

> **Latest release: Helm chart `0.4.0` (appVersion `0.0.4`) — [release notes](#helm-chart-changelog)**


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

> **Latest available chart version: `0.4.0`** (appVersion `0.0.4`)

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

3. **Install the operator** (latest `0.4.0`):
   ```bash
   helm install traffic-operator traffic-operator/traffic-operator \
     --namespace traffic-operator-system \
     --create-namespace
   ```

4. **Install a specific version**:
   ```bash
   helm install traffic-operator traffic-operator/traffic-operator \
     --namespace traffic-operator-system \
     --create-namespace \
     --version 0.4.0
   ```

5. **(Optional) Install or upgrade with custom values**:
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
make deploy IMG=djkormo/traffic-operator:0.0.4
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

## Stopping and Triggering Reconciliation

### Stopping Reconciliation on a TrafficCheck

To temporarily or permanently stop reconciliation on a `TrafficCheck` resource (preventing probe jobs from being created or re-run), annotate the resource with `network.djkormo.github.io/stop-reconcile: "true"`:

```bash
# Stop reconciliation
kubectl annotate trafficcheck connectivity-check network.djkormo.github.io/stop-reconcile="true" --overwrite

# Equivalent aliases also supported:
kubectl annotate trafficcheck connectivity-check network.djkormo.github.io/reconcile="disabled" --overwrite
kubectl annotate trafficcheck connectivity-check network.djkormo.github.io/paused="true" --overwrite
```

While reconciliation is stopped, the operator skips probe job creation and logs that reconciliation is paused.

### Reconciling TrafficCheck After Jobs are Completed and Deleted

When a probe Job finishes, its results are recorded in the `TrafficCheck.status`. Kubernetes cleans up the Job and its Pod after `ttlSecondsAfterFinished` has elapsed.

You can reconcile the `TrafficCheck` resource through any of the following methods:

1. **Automatic Reconciliation after Job Deletion**:
   Once the completed Job is deleted (either automatically via `ttlSecondsAfterFinished` or manually via `kubectl delete job`), the operator automatically reconciles and runs a fresh connectivity probe, as long as reconciliation is not stopped by annotation.

2. **Resume Reconciliation (Remove Stop Annotation)**:
   If reconciliation was previously stopped via annotation, remove the annotation to resume:
   ```bash
   kubectl annotate trafficcheck connectivity-check network.djkormo.github.io/stop-reconcile-
   ```

3. **On-Demand Re-run via Annotation**:
   To immediately trigger a fresh probe without waiting:
   ```bash
   kubectl annotate trafficcheck connectivity-check network.djkormo.github.io/re-run="true" --overwrite
   ```
   If an existing probe Job exists, the operator will remove it and launch a new check.

4. **Manual Job Deletion**:
   Deleting the current probe Job triggers an immediate re-reconciliation:
   ```bash
   kubectl delete job -l traffic-operator/probe=true
   ```

---

## Development & Helm Makefile Targets

| Target | Description |
|---|---|
| `make build` | Build the operator manager binary |
| `make manifests` | Generate CRD and RBAC manifests |
| `make helm-update-crds` | Regenerate and copy CRDs to `charts/traffic-operator/crds/` |
| `make helm-update-rbac` | Regenerate and update RBAC templates in `charts/traffic-operator/templates/` from `config/rbac/` |
| `make helm-update` | Regenerate and update both CRDs and RBAC in the Helm chart |
| `make helm-lint` | Lint the Helm chart |
| `make helm-template` | Render the Helm templates |
| `make helm-docs` | Regenerate Helm documentation using `helm-docs` |
| `make helm-package` | Package the Helm chart into `dist/` |

---

## Helm Chart Changelog

### 0.4.0 (2026-11-22)

- **Operator appVersion**: `0.0.4`
- Added the `access` target expectation, defaulting to `true`
- Added support for asserting that traffic is blocked (`access: false`)
- Added a deny-policy sample for blocked connectivity validation
- Download: [`traffic-operator-0.4.0.tgz`](https://github.com/djkormo/traffic-operator/releases/download/traffic-operator-0.4.0/traffic-operator-0.4.0.tgz)

### 0.3.0 (2026-11-22)

- **Operator appVersion**: `0.0.3`
- Added `jobGeneration` status tracking for probe reconciliation
- Added probe re-run and reconciliation lifecycle improvements
- Download: [`traffic-operator-0.3.0.tgz`](https://github.com/djkormo/traffic-operator/releases/download/traffic-operator-0.3.0/traffic-operator-0.3.0.tgz)

### 0.2.0 (2026-11-22)

- **Operator appVersion**: `0.0.2`
- Added `version` package exposing `Application`, `Release`, `Commit`, and `BuildTime` build metadata
- Operator startup log now includes full build information
- Expanded RBAC roles: added permissions for `events`, `pods/log`, `batch/jobs`
- Improved controller reconciliation: probe job lifecycle management, re-run annotation support, and stop-reconcile annotation support
- Extended `TrafficCheck` CRD status with per-target `TargetResult` including latency, reachability and last-checked timestamp
- Helm chart values extended with `podAnnotations`, `podLabels`, resource limit/request tuning
- Download: [`traffic-operator-0.2.0.tgz`](https://github.com/djkormo/traffic-operator/releases/download/traffic-operator-0.2.0/traffic-operator-0.2.0.tgz)

### 0.1.0

- Initial Helm chart release
- appVersion `0.0.1`
- Basic `TrafficCheck` CRD and controller deployment
- Download: [`traffic-operator-0.1.0.tgz`](https://github.com/djkormo/traffic-operator/releases/download/traffic-operator-0.1.0/traffic-operator-0.1.0.tgz)

