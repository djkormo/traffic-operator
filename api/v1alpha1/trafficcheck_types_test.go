package v1alpha1

import (
	"reflect"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/utils/ptr"
)

func TestSchemeRegistration(t *testing.T) {
	s := runtime.NewScheme()
	if err := AddToScheme(s); err != nil {
		t.Fatalf("AddToScheme failed: %v", err)
	}

	gvk := schema.GroupVersionKind{
		Group:   GroupVersion.Group,
		Version: GroupVersion.Version,
		Kind:    "TrafficCheck",
	}
	if !s.Recognizes(gvk) {
		t.Errorf("Scheme does not recognize %v", gvk)
	}

	listGVK := schema.GroupVersionKind{
		Group:   GroupVersion.Group,
		Version: GroupVersion.Version,
		Kind:    "TrafficCheckList",
	}
	if !s.Recognizes(listGVK) {
		t.Errorf("Scheme does not recognize %v", listGVK)
	}
}

func TestConstants(t *testing.T) {
	if TargetTypeService != "KubernetesService" {
		t.Errorf("unexpected TargetTypeService: %s", TargetTypeService)
	}
	if TargetTypeExternal != "External" {
		t.Errorf("unexpected TargetTypeExternal: %s", TargetTypeExternal)
	}
	if ProtocolTCP != "TCP" || ProtocolUDP != "UDP" || ProtocolHTTP != "HTTP" || ProtocolHTTPS != "HTTPS" {
		t.Errorf("unexpected protocol constants")
	}
	if AnnotationStopReconcile != "network.djkormo.github.io/stop-reconcile" {
		t.Errorf("unexpected AnnotationStopReconcile: %s", AnnotationStopReconcile)
	}
	if AnnotationReRun != "network.djkormo.github.io/re-run" {
		t.Errorf("unexpected AnnotationReRun: %s", AnnotationReRun)
	}
	if AnnotationValueTrue != "true" || AnnotationValueYes != "yes" {
		t.Errorf("unexpected annotation value constants")
	}
}

func TestTargetEndpointDeepCopy(t *testing.T) {
	var nilEndpoint *TargetEndpoint
	if nilEndpoint.DeepCopy() != nil {
		t.Error("expected nil DeepCopy to return nil")
	}

	endpoint := &TargetEndpoint{
		Name:               "test-endpoint",
		Type:               TargetTypeService,
		Protocol:           ProtocolHTTPS,
		Host:               "api-svc",
		Namespace:          "prod",
		Port:               8443,
		Access:             ptr.To(false),
		Path:               "/healthz",
		ExpectedStatusCode: 200,
		InsecureSkipVerify: true,
		TimeoutSeconds:     5,
	}

	copied := endpoint.DeepCopy()
	if copied == nil {
		t.Fatal("expected non-nil copied endpoint")
	}
	if !reflect.DeepEqual(copied, endpoint) {
		t.Errorf("copied endpoint mismatch: got %+v, want %+v", copied, endpoint)
	}
	*copied.Access = true
	if *endpoint.Access {
		t.Error("modifying copied Access should not modify original")
	}
}

func TestTargetResultDeepCopy(t *testing.T) {
	var nilResult *TargetResult
	if nilResult.DeepCopy() != nil {
		t.Error("expected nil DeepCopy to return nil")
	}

	now := metav1.NewTime(time.Now().Truncate(time.Second))
	res := &TargetResult{
		Name:        "test-target",
		Protocol:    ProtocolHTTP,
		Address:     "http://localhost:8080",
		Reachable:   true,
		Message:     "HTTP 200",
		LastChecked: now,
	}

	copied := res.DeepCopy()
	if copied == nil {
		t.Fatal("expected non-nil copied result")
	}
	if *copied != *res {
		t.Errorf("copied result mismatch: got %+v, want %+v", copied, res)
	}
}

func TestTrafficCheckSpecDeepCopy(t *testing.T) {
	var nilSpec *TrafficCheckSpec
	if nilSpec.DeepCopy() != nil {
		t.Error("expected nil DeepCopy to return nil")
	}

	// Empty spec
	emptySpec := &TrafficCheckSpec{}
	copiedEmpty := emptySpec.DeepCopy()
	if copiedEmpty == nil {
		t.Fatal("expected non-nil copiedEmpty")
	}

	// Populated spec
	spec := &TrafficCheckSpec{
		PodLabels: map[string]string{
			"app": "frontend",
		},
		ServiceAccountName: "probe-sa",
		Targets: []TargetEndpoint{
			{
				Name: "target-1",
				Type: TargetTypeExternal,
				Host: "example.com",
				Port: 443,
			},
		},
		ProbeImage:              "custom/netshoot:v1",
		TTLSecondsAfterFinished: ptr.To(int32(60)),
	}

	copied := spec.DeepCopy()
	if copied == nil {
		t.Fatal("expected non-nil copied spec")
	}

	// Verify deep copy isolation
	copied.PodLabels["new"] = "label"
	if _, exists := spec.PodLabels["new"]; exists {
		t.Error("modifying copied PodLabels should not modify original")
	}

	*copied.TTLSecondsAfterFinished = 120
	if *spec.TTLSecondsAfterFinished != 60 {
		t.Error("modifying copied TTLSecondsAfterFinished should not modify original")
	}
}

func TestTrafficCheckStatusDeepCopy(t *testing.T) {
	var nilStatus *TrafficCheckStatus
	if nilStatus.DeepCopy() != nil {
		t.Error("expected nil DeepCopy to return nil")
	}

	// Empty status
	emptyStatus := &TrafficCheckStatus{}
	copiedEmpty := emptyStatus.DeepCopy()
	if copiedEmpty == nil {
		t.Fatal("expected non-nil copiedEmpty")
	}

	// Populated status
	now := metav1.Now()
	status := &TrafficCheckStatus{
		ObservedGeneration: 2,
		Results: []TargetResult{
			{
				Name:        "target-1",
				Protocol:    ProtocolTCP,
				Address:     "1.2.3.4:80",
				Reachable:   true,
				LastChecked: now,
			},
		},
		Conditions: []metav1.Condition{
			{
				Type:               "Ready",
				Status:             metav1.ConditionTrue,
				ObservedGeneration: 2,
				Reason:             "AllTargetsReachable",
				Message:            "ok",
			},
		},
	}

	copied := status.DeepCopy()
	if copied == nil {
		t.Fatal("expected non-nil copied status")
	}

	// Verify deep copy isolation
	const modifiedStr = "modified"
	copied.Results[0].Name = modifiedStr
	if status.Results[0].Name == modifiedStr {
		t.Error("modifying copied Results should not modify original")
	}

	copied.Conditions[0].Reason = modifiedStr
	if status.Conditions[0].Reason == modifiedStr {
		t.Error("modifying copied Conditions should not modify original")
	}
}

func TestTrafficCheckDeepCopy(t *testing.T) {
	var nilTC *TrafficCheck
	if nilTC.DeepCopy() != nil {
		t.Error("expected nil DeepCopy to return nil")
	}
	if nilTC.DeepCopyObject() != nil {
		t.Error("expected nil DeepCopyObject to return nil")
	}

	tc := &TrafficCheck{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "network.djkormo.github.io/v1alpha1",
			Kind:       "TrafficCheck",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-check",
			Namespace: "default",
			Labels: map[string]string{
				"env": "prod",
			},
		},
		Spec: TrafficCheckSpec{
			Targets: []TargetEndpoint{
				{Name: "ext", Type: TargetTypeExternal, Host: "api.github.com", Port: 443},
			},
		},
		Status: TrafficCheckStatus{
			ObservedGeneration: 1,
		},
	}

	copied := tc.DeepCopy()
	if copied == nil {
		t.Fatal("expected non-nil copied TrafficCheck")
	}
	if copied.Name != tc.Name || copied.Namespace != tc.Namespace {
		t.Errorf("copied TrafficCheck mismatch: got %+v, want %+v", copied, tc)
	}

	runtimeObj := tc.DeepCopyObject()
	if runtimeObj == nil {
		t.Fatal("expected non-nil DeepCopyObject result")
	}
	if _, ok := runtimeObj.(*TrafficCheck); !ok {
		t.Errorf("expected *TrafficCheck type from DeepCopyObject, got %T", runtimeObj)
	}
}

func TestTrafficCheckListDeepCopy(t *testing.T) {
	var nilList *TrafficCheckList
	if nilList.DeepCopy() != nil {
		t.Error("expected nil DeepCopy to return nil")
	}
	if nilList.DeepCopyObject() != nil {
		t.Error("expected nil DeepCopyObject to return nil")
	}

	// Empty list
	emptyList := &TrafficCheckList{}
	copiedEmpty := emptyList.DeepCopy()
	if copiedEmpty == nil {
		t.Fatal("expected non-nil copiedEmpty")
	}

	list := &TrafficCheckList{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "network.djkormo.github.io/v1alpha1",
			Kind:       "TrafficCheckList",
		},
		ListMeta: metav1.ListMeta{
			ResourceVersion: "12345",
		},
		Items: []TrafficCheck{
			{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "item-1",
					Namespace: "default",
				},
				Spec: TrafficCheckSpec{
					Targets: []TargetEndpoint{
						{Name: "ext", Type: TargetTypeExternal, Host: "example.com", Port: 80},
					},
				},
			},
		},
	}

	copied := list.DeepCopy()
	if copied == nil {
		t.Fatal("expected non-nil copied TrafficCheckList")
	}
	if len(copied.Items) != 1 || copied.Items[0].Name != "item-1" {
		t.Errorf("copied list mismatch: got %+v, want %+v", copied, list)
	}

	runtimeObj := list.DeepCopyObject()
	if runtimeObj == nil {
		t.Fatal("expected non-nil DeepCopyObject result")
	}
	if _, ok := runtimeObj.(*TrafficCheckList); !ok {
		t.Errorf("expected *TrafficCheckList type from DeepCopyObject, got %T", runtimeObj)
	}
}
