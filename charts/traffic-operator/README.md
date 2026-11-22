# traffic-operator

![Version: 0.4.0](https://img.shields.io/badge/Version-0.4.0-informational?style=flat-square) ![Type: application](https://img.shields.io/badge/Type-application-informational?style=flat-square) ![AppVersion: 0.0.4](https://img.shields.io/badge/AppVersion-0.0.4-informational?style=flat-square)

A Helm chart for traffic-operator, a Kubernetes Operator for automated network connectivity validation

**Homepage:** <https://github.com/djkormo/traffic-operator>

## Overview

`traffic-operator` is a Kubernetes operator designed to validate network connectivity and policy enforcement within a Kubernetes cluster and towards external endpoints. It continuously reconciles `TrafficCheck` custom resources by orchestrating ephemeral probe jobs and reporting target reachability directly in the resource status.

### Features

- **Automated Connectivity Checks**: Verify TCP, UDP, HTTP, and HTTPS endpoints.
- **In-Cluster & External Targets**: Test internal Kubernetes Services as well as external FQDNs / IPs.
- **Status Reporting**: Aggregates target latency, status codes, and overall reachability condition (`AllTargetsReachable` / `TargetsUnreachable`).
- **Security First**: Runs adhering to the Kubernetes `restricted` Pod Security Standard (non-root, read-only root filesystem, dropped capabilities).
- **Prometheus Metrics**: Built-in metrics service and optional `ServiceMonitor` integration.
- **RBAC Aggregate Roles**: Provides out-of-the-box `admin`, `editor`, and `viewer` ClusterRoles.

## Source Code

* <https://github.com/djkormo/traffic-operator>

## Prerequisites

- Kubernetes 1.26+
- Helm 3.8.0+

## Installing the Chart

### Option 1: From the Helm Repository (GitHub Pages)

The `traffic-operator` chart is hosted via GitHub Pages on the `gh-pages` branch:

```bash
helm repo add traffic-operator https://djkormo.github.io/traffic-operator
helm repo update

helm install traffic-operator traffic-operator/traffic-operator \
  --namespace traffic-operator-system \
  --create-namespace
```

### Option 2: From Local Source

```bash
helm install traffic-operator ./charts/traffic-operator \
  --namespace traffic-operator-system \
  --create-namespace
```

### Custom Resource Definitions (CRDs)

Helm automatically installs CRDs located in the `crds/` directory during initial chart installation. If you manage CRDs separately or use GitOps tools such as ArgoCD, you can apply the CRD manually:

```bash
kubectl apply -f charts/traffic-operator/crds/network.djkormo.github.io_trafficchecks.yaml
```

### Verify Installation

```bash
kubectl get pods -n traffic-operator-system -l control-plane=controller-manager
```

## Usage Example

Create a `TrafficCheck` resource to verify connectivity to both an external API and an internal service:

```yaml
apiVersion: network.djkormo.github.io/v1alpha1
kind: TrafficCheck
metadata:
  name: example-check
  namespace: default
spec:
  podLabels:
    app: example-probe
  probeImage: nicolaka/netshoot:latest
  ttlSecondsAfterFinished: 120
  targets:
    - name: external-github
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

Check the results:

```bash
kubectl get trafficcheck example-check -o wide
kubectl describe trafficcheck example-check
```

## Uninstalling the Chart

To uninstall the `traffic-operator` release:

```bash
helm uninstall traffic-operator --namespace traffic-operator-system
```

> [!NOTE]
> Custom Resource Definitions (CRDs) are not deleted by Helm on chart uninstallation to prevent accidental data loss. To remove them manually:
> ```bash
> kubectl delete crd trafficchecks.network.djkormo.github.io
> ```

## Values

| Key | Type | Default | Description |
|-----|------|---------|-------------|
| replicaCount | int | `1` | Number of replicas for the traffic-operator deployment |
| image.repository | string | `"djkormo/traffic-operator"` | Image repository for traffic-operator |
| image.pullPolicy | string | `"IfNotPresent"` | Image pull policy |
| image.tag | string | `""` | Overrides the image tag whose default is the chart appVersion. |
| imagePullSecrets | list | `[]` | Image pull secrets for private container registries |
| nameOverride | string | `""` | Override the chart name |
| fullnameOverride | string | `""` | Override the full chart name |
| commonLabels | object | `{}` | Common labels to apply to all resources |
| deploymentAnnotations | object | `{}` | Annotations to apply to the Deployment |
| podAnnotations | object | `{}` | Annotations to apply to operator pods |
| podLabels | object | `{}` | Labels to apply to operator pods |
| podSecurityContext | object | `{"runAsNonRoot":true,"seccompProfile":{"type":"RuntimeDefault"}}` | Pod-level security context adheres to the "restricted" Pod Security Standard |
| securityContext | object | `{"allowPrivilegeEscalation":false,"capabilities":{"drop":["ALL"]},"readOnlyRootFilesystem":true}` | Container-level security context |
| terminationGracePeriodSeconds | int | `10` | Termination grace period in seconds |
| priorityClassName | string | `""` | Optional priority class name for operator pod |
| serviceAccount.create | bool | `true` | Specifies whether a service account should be created |
| serviceAccount.automount | bool | `true` | Automatically mount service account token in pod |
| serviceAccount.annotations | object | `{}` | Annotations to add to the service account |
| serviceAccount.name | string | `""` | The name of the service account to use. If not set and create is true, a name is generated using the fullname template |
| leaderElection.enabled | bool | `true` | Enable leader election for the operator controller manager |
| probe.enabled | bool | `true` | Enable health check probes |
| probe.port | int | `8081` | Port for health probes (:8081) |
| livenessProbe | object | `{"httpGet":{"path":"/healthz","port":8081},"initialDelaySeconds":15,"periodSeconds":20}` | Liveness probe configuration |
| readinessProbe | object | `{"httpGet":{"path":"/readyz","port":8081},"initialDelaySeconds":5,"periodSeconds":10}` | Readiness probe configuration |
| logging.logLevel | string | `"info"` | Zap log level (debug, info, error) |
| logging.encoder | string | `"console"` | Zap encoder (json, console) |
| logging.development | bool | `false` | Development mode for logger (more verbose stacktraces) |
| metrics.enabled | bool | `true` | Enable metrics endpoint |
| metrics.port | int | `8443` | Port for metrics endpoint (:8443) |
| metrics.service.type | string | `"ClusterIP"` | Metrics service type |
| metrics.service.port | int | `8443` | Metrics service port |
| metrics.service.labels | object | `{}` | Additional labels for metrics service |
| metrics.service.annotations | object | `{}` | Additional annotations for metrics service |
| metrics.serviceMonitor.enabled | bool | `false` | Enable Prometheus ServiceMonitor |
| metrics.serviceMonitor.namespace | string | `""` | ServiceMonitor namespace (defaults to release namespace) |
| metrics.serviceMonitor.interval | string | `"30s"` | Scrape interval |
| metrics.serviceMonitor.scrapeTimeout | string | `"10s"` | Scrape timeout |
| metrics.serviceMonitor.honorLabels | bool | `false` | Honor labels |
| metrics.serviceMonitor.labels | object | `{}` | Additional labels for the ServiceMonitor |
| metrics.serviceMonitor.annotations | object | `{}` | Additional annotations for the ServiceMonitor |
| metrics.serviceMonitor.tlsConfig.insecureSkipVerify | bool | `true` | Skip TLS verification for metrics scraping (default true unless cert-manager is configured) |
| metrics.serviceMonitor.tlsConfig.serverName | string | `""` | TLS server name |
| metrics.serviceMonitor.tlsConfig.caSecret | object | `{}` | CA secret reference (optional) |
| metrics.serviceMonitor.metricRelabelings | list | `[]` | Metric relabelings |
| metrics.serviceMonitor.relabelings | list | `[]` | Relabelings |
| networkPolicy.enabled | bool | `false` | Enable NetworkPolicy to restrict access to operator metrics |
| networkPolicy.annotations | object | `{}` | Annotations for NetworkPolicy |
| networkPolicy.ingress | list | `[{"from":[{"namespaceSelector":{"matchLabels":{"metrics":"enabled"}}}],"ports":[{"port":8443,"protocol":"TCP"}]}]` | Ingress rules for the NetworkPolicy |
| rbac.create | bool | `true` | Specifies whether RBAC resources should be created |
| rbac.extraRules | list | `[]` | Extra RBAC rules to append to the manager ClusterRole |
| rbac.userRoles.create | bool | `true` | Create TrafficCheck Admin, Editor, and Viewer ClusterRoles |
| rbac.metricsAuth.enabled | bool | `false` | Enable RBAC for metrics tokenreviews and subjectaccessreviews |
| resources | object | `{"limits":{"cpu":"500m","memory":"128Mi"},"requests":{"cpu":"10m","memory":"64Mi"}}` | Resource limits and requests for the operator container |
| nodeSelector | object | `{}` | Node labels for pod assignment |
| tolerations | list | `[]` | Node tolerations for pod assignment |
| affinity | object | `{}` | Node affinity for pod assignment |
| extraArgs | list | `[]` | Extra command line arguments for the operator container |
| env | list | `[]` | Extra environment variables for the operator container |
| volumeMounts | list | `[]` | Additional volume mounts for the operator container |
| volumes | list | `[]` | Additional volumes for the operator pod |

## Maintainers

| Name | Email | Url |
| ---- | ------ | --- |
| djkormo |  | <https://github.com/djkormo> |
