package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// TargetType distinguishes between in-cluster Services and external endpoints.
// +kubebuilder:validation:Enum=KubernetesService;External
type TargetType string

const (
	TargetTypeService  TargetType = "KubernetesService"
	TargetTypeExternal TargetType = "External"
)

const (
	// AnnotationStopReconcile stops reconciliation when set to "true".
	AnnotationStopReconcile = "network.djkormo.github.io/stop-reconcile"
	// AnnotationReconcile controls reconciliation behavior.
	// Set to "disabled" to stop reconciliation.
	// Set to "now" or "force" to trigger reconciliation.
	AnnotationReconcile = "network.djkormo.github.io/reconcile"
	// AnnotationPaused is an alias to pause reconciliation when set to "true".
	AnnotationPaused = "network.djkormo.github.io/paused"
	// AnnotationK8sPaused is the standard Kubernetes paused annotation.
	AnnotationK8sPaused = "reconcile.kubernetes.io/paused"

	// AnnotationReRun triggers a re-run of the traffic check when set to "true".
	AnnotationReRun = "network.djkormo.github.io/re-run"
	// AnnotationReconcileAt triggers reconciliation when set to a new timestamp.
	AnnotationReconcileAt = "network.djkormo.github.io/reconcile-at"

	// AnnotationJobCollected marks that results from this Job have already been collected.
	AnnotationJobCollected = "traffic-operator/collected"

	// AnnotationValueTrue is the string literal "true".
	AnnotationValueTrue = "true"
	// AnnotationValueYes is the string literal "yes".
	AnnotationValueYes = "yes"
)

// ProtocolType defines the protocol for the connectivity check.
// +kubebuilder:validation:Enum=TCP;UDP;HTTP;HTTPS
type ProtocolType string

const (
	ProtocolTCP   ProtocolType = "TCP"
	ProtocolUDP   ProtocolType = "UDP"
	ProtocolHTTP  ProtocolType = "HTTP"
	ProtocolHTTPS ProtocolType = "HTTPS"
)

type TargetEndpoint struct {
	// Name is a human-readable identifier for this target.
	// +kubebuilder:validation:MinLength=1
	Name string `json:"name"`

	// Type specifies whether the target is a KubernetesService or External.
	Type TargetType `json:"type"`

	// Protocol specifies TCP, UDP, HTTP, or HTTPS.
	// +kubebuilder:default=TCP
	// +optional
	Protocol ProtocolType `json:"protocol,omitempty"`

	// Host is the Service name (for KubernetesService) or FQDN/IP (for External).
	// +kubebuilder:validation:MinLength=1
	Host string `json:"host"`

	// Namespace is optional; used when Type is KubernetesService (defaults to CR namespace).
	// +optional
	Namespace string `json:"namespace,omitempty"`

	// Port to test connectivity against.
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Maximum=65535
	Port int32 `json:"port"`

	// Access is the expected connectivity outcome. False asserts that traffic is blocked.
	// +kubebuilder:default=true
	// +optional
	Access *bool `json:"access,omitempty"`

	// Path is the URL path for HTTP/HTTPS checks (e.g. "/healthz"). Defaults to "/".
	// +kubebuilder:default="/"
	// +optional
	Path string `json:"path,omitempty"`

	// ExpectedStatusCode asserts a specific HTTP response code (e.g. 200).
	// If 0, any 2xx or 3xx status code is treated as reachable.
	// +kubebuilder:validation:Minimum=0
	// +kubebuilder:validation:Maximum=599
	// +optional
	ExpectedStatusCode int32 `json:"expectedStatusCode,omitempty"`

	// InsecureSkipVerify skips TLS certificate verification for HTTPS targets.
	// +optional
	InsecureSkipVerify bool `json:"insecureSkipVerify,omitempty"`

	// TimeoutSeconds for the connection attempt.
	// +kubebuilder:default=5
	// +kubebuilder:validation:Minimum=1
	// +optional
	TimeoutSeconds int32 `json:"timeoutSeconds,omitempty"`
}

type TrafficCheckSpec struct {
	// PodLabels are applied directly to the Job's probe pod (e.g., to match NetworkPolicies).
	// +optional
	PodLabels map[string]string `json:"podLabels,omitempty"`

	// ServiceAccountName is the optional ServiceAccount assigned to the probe pod.
	// +optional
	ServiceAccountName string `json:"serviceAccountName,omitempty"`

	// Targets is the list of internal or external endpoints to verify.
	// +kubebuilder:validation:MinItems=1
	Targets []TargetEndpoint `json:"targets"`

	// ProbeImage optionally overrides the operator's configured default image for connectivity checks.
	// +optional
	ProbeImage string `json:"probeImage,omitempty"`

	// TTLSecondsAfterFinished limits the lifetime of a finished Job before Kubernetes deletes it.
	// +kubebuilder:default=60
	// +kubebuilder:validation:Minimum=0
	// +optional
	TTLSecondsAfterFinished *int32 `json:"ttlSecondsAfterFinished,omitempty"`
}

type TargetResult struct {
	Name        string       `json:"name"`
	Protocol    ProtocolType `json:"protocol"`
	Address     string       `json:"address"`
	Reachable   bool         `json:"reachable"`
	Message     string       `json:"message,omitempty"`
	LastChecked metav1.Time  `json:"lastChecked"`
}

type TrafficCheckStatus struct {
	// JobGeneration tracks the spec generation for which a probe Job was created.
	JobGeneration int64 `json:"jobGeneration,omitempty"`

	// ObservedGeneration tracks the spec generation that produced the current results.
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`

	// Results holds the connectivity outcome for each configured target.
	Results []TargetResult `json:"results,omitempty"`

	// NonCompliantTargets holds the names of targets whose observed access did not match the expectation.
	NonCompliantTargets []string `json:"nonCompliantTargets,omitempty"`

	// Conditions represent the latest available observations of the TrafficCheck state.
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="Ready",type="string",JSONPath=".status.conditions[?(@.type=='Ready')].status"
// +kubebuilder:printcolumn:name="Reconciled",type="string",JSONPath=".status.conditions[?(@.type=='Reconciled')].status"
// +kubebuilder:printcolumn:name="Namespace",type="string",JSONPath=".metadata.namespace"
// +kubebuilder:printcolumn:name="NonCompliant",type="string",JSONPath=".status.nonCompliantTargets"
// +kubebuilder:printcolumn:name="CheckTime",type="date",JSONPath=".status.results[0].lastChecked"
// +kubebuilder:printcolumn:name="Age",type="date",JSONPath=".metadata.creationTimestamp"

type TrafficCheck struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   TrafficCheckSpec   `json:"spec,omitempty"`
	Status TrafficCheckStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

type TrafficCheckList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []TrafficCheck `json:"items"`
}

func init() {
	SchemeBuilder.Register(&TrafficCheck{}, &TrafficCheckList{})
}
