package controller

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"strings"
	"time"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes"
	"k8s.io/utils/ptr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	networkv1alpha1 "github.com/djkormo/traffic-operator/api/v1alpha1"
)

const (
	strTrue = "true"
	strYes  = "yes"
)

type TrafficCheckReconciler struct {
	client.Client
	Scheme    *runtime.Scheme
	Clientset *kubernetes.Clientset
	// LogReader overrides pod log fetching in tests. If nil, Clientset is used.
	LogReader func(ctx context.Context, namespace, podName string) (string, error)
}

// +kubebuilder:rbac:groups=network.djkormo.github.io,resources=trafficchecks,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=network.djkormo.github.io,resources=trafficchecks/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=batch,resources=jobs,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups="",resources=pods,verbs=get;list;watch
// +kubebuilder:rbac:groups="",resources=pods/log,verbs=get
// +kubebuilder:rbac:groups="",resources=events,verbs=create;patch

func (r *TrafficCheckReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	var tc networkv1alpha1.TrafficCheck
	if err := r.Get(ctx, req.NamespacedName, &tc); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	// 1. Check if reconciliation is stopped via annotation.
	if isReconciliationStopped(&tc) {
		logger.Info("Reconciliation is stopped via annotation", "trafficcheck", tc.Name)
		return ctrl.Result{}, nil
	}

	jobName := fmt.Sprintf("traffic-probe-%s-g%d", tc.Name, tc.Generation)
	var existingJob batchv1.Job
	err := r.Get(ctx, client.ObjectKey{Namespace: tc.Namespace, Name: jobName}, &existingJob)

	// Check if an on-demand re-run was requested via annotation.
	if isReRunRequested(&tc) {
		if err == nil {
			logger.Info("Re-run requested via annotation; deleting existing probe job", "job", jobName)
			if delErr := r.Delete(ctx, &existingJob, client.PropagationPolicy(metav1.DeletePropagationBackground)); delErr != nil && !apierrors.IsNotFound(delErr) {
				return ctrl.Result{}, delErr
			}
			return ctrl.Result{RequeueAfter: 1 * time.Second}, nil
		}
		// Clear re-run annotation once job is gone or being created
		if tc.Annotations != nil && tc.Annotations[networkv1alpha1.AnnotationReRun] == strTrue {
			delete(tc.Annotations, networkv1alpha1.AnnotationReRun)
			if updateErr := r.Update(ctx, &tc); updateErr != nil {
				return ctrl.Result{}, updateErr
			}
		}
	}

	// 2. Create the probe Job if it does not exist (initial run or after previous job was deleted).
	if apierrors.IsNotFound(err) {
		// Minimum cooldown of 5s between automatic re-runs of the same generation if previous results exist (prevents tight looping if TTL=0)
		if tc.Status.ObservedGeneration == tc.Generation && len(tc.Status.Results) > 0 && time.Since(tc.Status.Results[0].LastChecked.Time) < 5*time.Second {
			requeueIn := 5*time.Second - time.Since(tc.Status.Results[0].LastChecked.Time)
			return ctrl.Result{RequeueAfter: requeueIn}, nil
		}

		job := r.buildProbeJob(&tc, jobName)
		if err := ctrl.SetControllerReference(&tc, job, r.Scheme); err != nil {
			return ctrl.Result{}, err
		}

		logger.Info("Creating probe job", "job", jobName, "ttl", *job.Spec.TTLSecondsAfterFinished)
		if err := r.Create(ctx, job); err != nil {
			if apierrors.IsAlreadyExists(err) {
				return ctrl.Result{RequeueAfter: 2 * time.Second}, nil
			}
			return ctrl.Result{}, err
		}
		return ctrl.Result{RequeueAfter: 3 * time.Second}, nil
	} else if err != nil {
		return ctrl.Result{}, err
	}

	// If the existing Job is in the process of being deleted, wait for it to be fully removed.
	if existingJob.DeletionTimestamp != nil {
		return ctrl.Result{RequeueAfter: 1 * time.Second}, nil
	}

	// 3. Wait until the Job finishes (Succeeded or Failed).
	if existingJob.Status.Succeeded == 0 && existingJob.Status.Failed == 0 {
		return ctrl.Result{RequeueAfter: 3 * time.Second}, nil
	}

	// If results for this Job have already been collected, wait for TTL cleanup or deletion.
	if existingJob.Annotations != nil && existingJob.Annotations[networkv1alpha1.AnnotationJobCollected] == strTrue {
		return ctrl.Result{}, nil
	}

	// 4. Find the Pod created by this Job and read its logs.
	probePod, err := r.findJobPod(ctx, tc.Namespace, jobName)
	if err != nil {
		return ctrl.Result{RequeueAfter: 3 * time.Second}, err
	}

	results, allPassed, err := r.collectResults(ctx, probePod)
	if err != nil {
		return ctrl.Result{}, err
	}

	tc.Status.ObservedGeneration = tc.Generation
	tc.Status.Results = results
	if allPassed {
		r.setCondition(&tc, metav1.ConditionTrue, "AllTargetsReachable", "All configured targets are reachable")
	} else {
		r.setCondition(&tc, metav1.ConditionFalse, "TargetsUnreachable", "One or more targets failed connectivity check")
	}

	if err := r.Status().Update(ctx, &tc); err != nil {
		return ctrl.Result{}, err
	}

	// Mark Job as collected so it won't be re-evaluated while waiting for TTL cleanup.
	if existingJob.Annotations == nil {
		existingJob.Annotations = make(map[string]string)
	}
	existingJob.Annotations[networkv1alpha1.AnnotationJobCollected] = strTrue
	if err := r.Update(ctx, &existingJob); err != nil && !apierrors.IsNotFound(err) {
		logger.V(1).Info("Could not mark job as collected", "job", jobName, "error", err)
	}

	// Kubernetes TTL controller deletes the Job and its Pod after TTLSecondsAfterFinished.
	return ctrl.Result{}, nil
}

func isReconciliationStopped(tc *networkv1alpha1.TrafficCheck) bool {
	if tc.Annotations == nil {
		return false
	}
	if val, ok := tc.Annotations[networkv1alpha1.AnnotationStopReconcile]; ok {
		if strings.ToLower(val) == strTrue || val == "1" || strings.ToLower(val) == strYes {
			return true
		}
	}
	if val, ok := tc.Annotations[networkv1alpha1.AnnotationPaused]; ok {
		if strings.ToLower(val) == strTrue || val == "1" || strings.ToLower(val) == strYes {
			return true
		}
	}
	if val, ok := tc.Annotations[networkv1alpha1.AnnotationK8sPaused]; ok {
		if strings.ToLower(val) == strTrue || val == "1" || strings.ToLower(val) == strYes {
			return true
		}
	}
	if val, ok := tc.Annotations[networkv1alpha1.AnnotationReconcile]; ok {
		lower := strings.ToLower(val)
		if lower == "disabled" || lower == "false" || lower == "stop" || lower == "paused" {
			return true
		}
	}
	return false
}

func isReRunRequested(tc *networkv1alpha1.TrafficCheck) bool {
	if tc.Annotations == nil {
		return false
	}
	if val, ok := tc.Annotations[networkv1alpha1.AnnotationReRun]; ok {
		if strings.ToLower(val) == strTrue || val == "1" || strings.ToLower(val) == strYes {
			return true
		}
	}
	if val, ok := tc.Annotations[networkv1alpha1.AnnotationReconcile]; ok {
		lower := strings.ToLower(val)
		if lower == "now" || lower == "force" {
			return true
		}
	}
	return false
}

func (r *TrafficCheckReconciler) buildProbeJob(tc *networkv1alpha1.TrafficCheck, name string) *batchv1.Job {
	labels := make(map[string]string, len(tc.Spec.PodLabels)+1)
	for k, v := range tc.Spec.PodLabels {
		labels[k] = v
	}
	labels["traffic-operator/probe"] = strTrue

	scriptLines := make([]string, 0, len(tc.Spec.Targets))
	for _, t := range tc.Spec.Targets {
		scriptLines = append(scriptLines, buildTargetCommand(t, tc.Namespace))
	}

	image := tc.Spec.ProbeImage
	if image == "" {
		image = "nicolaka/netshoot:latest"
	}

	ttl := int32(60)
	if tc.Spec.TTLSecondsAfterFinished != nil {
		ttl = *tc.Spec.TTLSecondsAfterFinished
	}

	return &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: tc.Namespace,
		},
		Spec: batchv1.JobSpec{
			TTLSecondsAfterFinished: ptr.To(ttl),
			BackoffLimit:            ptr.To(int32(0)),
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: labels,
				},
				Spec: corev1.PodSpec{
					ServiceAccountName:           tc.Spec.ServiceAccountName,
					AutomountServiceAccountToken: ptr.To(false),
					RestartPolicy:                corev1.RestartPolicyNever,
					SecurityContext: &corev1.PodSecurityContext{
						RunAsNonRoot: ptr.To(true),
						RunAsUser:    ptr.To(int64(65534)), // nobody
						RunAsGroup:   ptr.To(int64(65534)),
						SeccompProfile: &corev1.SeccompProfile{
							Type: corev1.SeccompProfileTypeRuntimeDefault,
						},
					},
					Containers: []corev1.Container{
						{
							Name:    "probe",
							Image:   image,
							Command: []string{"/bin/sh", "-c", strings.Join(scriptLines, "; ")},
							SecurityContext: &corev1.SecurityContext{
								AllowPrivilegeEscalation: ptr.To(false),
								ReadOnlyRootFilesystem:   ptr.To(true),
								Capabilities: &corev1.Capabilities{
									Drop: []corev1.Capability{"ALL"},
								},
							},
						},
					},
				},
			},
		},
	}
}

func buildTargetCommand(t networkv1alpha1.TargetEndpoint, defaultNS string) string {
	host := resolveHost(t, defaultNS)
	timeout := t.TimeoutSeconds
	if timeout == 0 {
		timeout = 5
	}
	proto := t.Protocol
	if proto == "" {
		proto = networkv1alpha1.ProtocolTCP
	}

	switch proto {
	case networkv1alpha1.ProtocolHTTP, networkv1alpha1.ProtocolHTTPS:
		scheme := strings.ToLower(string(proto))
		path := t.Path
		if path == "" {
			path = "/"
		}
		url := fmt.Sprintf("%s://%s:%d%s", scheme, host, t.Port, path)

		insecureFlag := ""
		if proto == networkv1alpha1.ProtocolHTTPS && t.InsecureSkipVerify {
			insecureFlag = "-k "
		}

		condition := `[ "$code" -ge 200 ] && [ "$code" -lt 400 ]`
		if t.ExpectedStatusCode > 0 {
			condition = fmt.Sprintf(`[ "$code" -eq %d ]`, t.ExpectedStatusCode)
		}

		return fmt.Sprintf(
			`code=$(curl -sS -o /dev/null -w "%%{http_code}" --max-time %d %s"%s" 2>/dev/null); [ -z "$code" ] && code=000; `+
				`if %s; then echo "RESULT|%s|%s|%s|PASS|HTTP $code"; else echo "RESULT|%s|%s|%s|FAIL|HTTP $code"; fi`,
			timeout, insecureFlag, url, condition,
			t.Name, proto, url,
			t.Name, proto, url,
		)

	default: // TCP or UDP
		ncFlags := fmt.Sprintf("-z -w %d", timeout)
		if proto == networkv1alpha1.ProtocolUDP {
			ncFlags = fmt.Sprintf("-z -u -w %d", timeout)
		}
		addr := fmt.Sprintf("%s:%d", host, t.Port)
		return fmt.Sprintf(
			`if nc %s %s %d 2>/dev/null; then echo "RESULT|%s|%s|%s|PASS|%s connection succeeded"; else echo "RESULT|%s|%s|%s|FAIL|%s connection timed out or refused"; fi`,
			ncFlags, host, t.Port,
			t.Name, proto, addr, proto,
			t.Name, proto, addr, proto,
		)
	}
}

func resolveHost(t networkv1alpha1.TargetEndpoint, defaultNS string) string {
	if t.Type == networkv1alpha1.TargetTypeService {
		ns := t.Namespace
		if ns == "" {
			ns = defaultNS
		}
		return fmt.Sprintf("%s.%s.svc.cluster.local", t.Host, ns)
	}
	return t.Host
}

func (r *TrafficCheckReconciler) findJobPod(ctx context.Context, ns, jobName string) (*corev1.Pod, error) {
	var pods corev1.PodList
	if err := r.List(ctx, &pods, client.InNamespace(ns), client.MatchingLabels{"job-name": jobName}); err != nil {
		return nil, err
	}
	if len(pods.Items) == 0 {
		return nil, fmt.Errorf("no pod found for job %s/%s", ns, jobName)
	}
	latestPod := &pods.Items[0]
	for i := 1; i < len(pods.Items); i++ {
		if pods.Items[i].CreationTimestamp.After(latestPod.CreationTimestamp.Time) {
			latestPod = &pods.Items[i]
		}
	}
	return latestPod, nil
}

func (r *TrafficCheckReconciler) readPodLogs(ctx context.Context, pod *corev1.Pod) (logs string, err error) {
	if r.LogReader != nil {
		return r.LogReader(ctx, pod.Namespace, pod.Name)
	}
	req := r.Clientset.CoreV1().Pods(pod.Namespace).GetLogs(pod.Name, &corev1.PodLogOptions{})
	stream, err := req.Stream(ctx)
	if err != nil {
		return "", err
	}
	defer func() {
		if closeErr := stream.Close(); err == nil {
			err = closeErr
		}
	}()

	var buf bytes.Buffer
	if _, err := io.Copy(&buf, stream); err != nil {
		return "", err
	}
	return buf.String(), nil
}

func (r *TrafficCheckReconciler) collectResults(ctx context.Context, pod *corev1.Pod) ([]networkv1alpha1.TargetResult, bool, error) {
	logs, err := r.readPodLogs(ctx, pod)
	if err != nil {
		return nil, false, err
	}

	now := metav1.Now()
	lines := strings.Split(logs, "\n")
	results := make([]networkv1alpha1.TargetResult, 0, len(lines))
	allPassed := true

	for _, line := range lines {
		if !strings.HasPrefix(line, "RESULT|") {
			continue
		}
		parts := strings.Split(line, "|")
		if len(parts) != 6 {
			continue
		}
		reachable := parts[4] == "PASS"
		if !reachable {
			allPassed = false
		}
		results = append(results, networkv1alpha1.TargetResult{
			Name:        parts[1],
			Protocol:    networkv1alpha1.ProtocolType(parts[2]),
			Address:     parts[3],
			Reachable:   reachable,
			Message:     parts[5],
			LastChecked: now,
		})
	}
	return results, allPassed, nil
}

func (r *TrafficCheckReconciler) setCondition(tc *networkv1alpha1.TrafficCheck, status metav1.ConditionStatus, reason, msg string) {
	meta.SetStatusCondition(&tc.Status.Conditions, metav1.Condition{
		Type:               "Ready",
		Status:             status,
		ObservedGeneration: tc.Generation,
		Reason:             reason,
		Message:            msg,
	})
}

func (r *TrafficCheckReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&networkv1alpha1.TrafficCheck{}).
		Owns(&batchv1.Job{}).
		Complete(r)
}
