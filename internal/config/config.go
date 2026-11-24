package config

const (
	DefaultProbeImage           = "nicolaka/netshoot:latest"
	DefaultProbeConfigMapName   = "traffic-operator-config"
	ProbeImageConfigMapKey      = "DEFAULT_PROBE_IMAGE"
	ServiceAccountNamespacePath = "/var/run/secrets/kubernetes.io/serviceaccount/namespace"
)
