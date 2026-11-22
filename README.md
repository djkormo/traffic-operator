Look at https://djkormo.github.io/traffic-operator/

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
4. **Operator updates CR status**: Collects results (status codes and failure messages) and sets `Ready=True` when every target matches its expected access (`AllTargetsMatched`), or `Ready=False` when one or more expectations fail (`AccessExpectationFailed`).
5. **Job cleanup**: Ephemeral jobs and pods are automatically cleaned up via Kubernetes `ttlSecondsAfterFinished`.

---

## Installation via Helm

The recommended way to deploy `traffic-operator` is via the provided Helm chart in [`charts/traffic-operator`](file:///mnt/c/development/kubernetes/traffic-operator/charts/traffic-operator).

### Install the Chart

```bash
helm install traffic-operator ./charts/traffic-operator \
  --namespace traffic-operator-system \
  --create-namespace
```

### Verify Deployment

```bash
kubectl get pods -n traffic-operator-system
```

For full Helm chart configuration options, see the [Chart README](file:///mnt/c/development/kubernetes/traffic-operator/charts/traffic-operator/README.md).

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
      access: true
      path: /
      expectedStatusCode: 200
      timeoutSeconds: 5
    - name: internal-kubernetes-api
      type: KubernetesService
      protocol: HTTPS
      host: kubernetes
      namespace: default
      port: 443
      access: true
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

Set `access: false` on a target to assert that the probe cannot reach it. The `Ready` condition is true when the observed result matches each target's expected `access` value; `.status.results[].reachable` continues to report observed connectivity. Thus, a correctly blocked target has `reachable: false` while the `TrafficCheck` can still be `Ready=True`. Omitting `access` defaults to `true`.

`access` is an expectation only: the operator creates probe Jobs and reports results, but does not create or enforce NetworkPolicies. Configure a policy separately so it selects the probe pod. The labels under `spec.podLabels` are applied to that pod and should match the policy's pod selector. For service targets, ensure DNS is allowed when the probe uses a service hostname; otherwise a DNS failure can also appear as unreachable.

```yaml
spec:
  podLabels:
    app: frontend
  targets:
    - name: blocked-database
      type: KubernetesService
      protocol: TCP
      host: database
      namespace: production
      port: 5432
      access: false
```

The deny example is also available as [`config/samples/network_v1alpha1_trafficcheck_deny.yaml`](config/samples/network_v1alpha1_trafficcheck_deny.yaml). It assumes your cluster has a policy that blocks the selected probe pod from reaching the database.

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

When a probe Job finishes, its results are recorded in the `TrafficCheck.status`. Kubernetes cleans up the Job and its Pod after `ttlSecondsAfterFinished` has elapsed. The controller does not automatically recreate a Job for the same spec generation after the Job is deleted, either manually or by TTL cleanup.

To run another probe, use one of these methods:

1. **On-demand re-run**: Set the re-run annotation. If the current Job still exists, the controller deletes it and then creates a replacement Job for the current generation.
   ```bash
   kubectl annotate trafficcheck connectivity-check network.djkormo.github.io/re-run="true" --overwrite
   ```
2. **Change the spec**: Updating the `TrafficCheck` spec creates a probe for the new generation, provided reconciliation is not paused.

Deleting a Job manually only removes that Job; it does not request another probe. Removing a stop or pause annotation resumes reconciliation but does not by itself rerun a Job that was already deleted. To remove a probe Job manually:
   ```bash
  kubectl delete job traffic-probe-connectivity-check-g1 -n default
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
