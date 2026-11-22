package controller

import (
	"context"
	"fmt"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	networkv1alpha1 "github.com/djkormo/traffic-operator/api/v1alpha1"
)

var _ = Describe("TrafficCheck Controller", func() {
	const namespace = "default"
	const defaultServiceAccount = "default"

	ctx := context.Background()

	newReconciler := func(fakeLogs string) *TrafficCheckReconciler {
		return &TrafficCheckReconciler{
			Client: k8sClient,
			Scheme: k8sClient.Scheme(),
			LogReader: func(ctx context.Context, ns, podName string) (string, error) {
				return fakeLogs, nil
			},
		}
	}

	simulateJobCompletion := func(jobName string) {
		var job batchv1.Job
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: jobName, Namespace: namespace}, &job)).To(Succeed())

		pod := &corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name:      jobName + "-pod",
				Namespace: namespace,
				Labels:    map[string]string{testLabelJobName: jobName},
			},
			Spec: corev1.PodSpec{
				Containers: []corev1.Container{{Name: testContainerProbe, Image: testNetshootImg}},
			},
		}
		Expect(k8sClient.Create(ctx, pod)).To(Succeed())

		job.Status.Succeeded = 1
		Expect(k8sClient.Status().Update(ctx, &job)).To(Succeed())
	}

	deleteJobAndPods := func(jobName string) {
		var job batchv1.Job
		if err := k8sClient.Get(ctx, types.NamespacedName{Name: jobName, Namespace: namespace}, &job); err == nil {
			job.Finalizers = []string{}
			_ = k8sClient.Update(ctx, &job)
			_ = k8sClient.Delete(ctx, &job, client.PropagationPolicy(metav1.DeletePropagationBackground))
		}
		var pod corev1.Pod
		if err := k8sClient.Get(ctx, types.NamespacedName{Name: jobName + "-pod", Namespace: namespace}, &pod); err == nil {
			pod.Finalizers = []string{}
			_ = k8sClient.Update(ctx, &pod)
			_ = k8sClient.Delete(ctx, &pod, client.PropagationPolicy(metav1.DeletePropagationBackground))
		}
	}

	It("creates a restricted Job with custom TTL and podLabels, then updates status on TCP PASS", func() {
		crName := "check-tcp-pass"
		ttl := int32(120)
		tc := &networkv1alpha1.TrafficCheck{
			ObjectMeta: metav1.ObjectMeta{
				Name:      crName,
				Namespace: namespace,
			},
			Spec: networkv1alpha1.TrafficCheckSpec{
				PodLabels:               map[string]string{"app": "frontend", "tier": "web"},
				ServiceAccountName:      defaultServiceAccount,
				TTLSecondsAfterFinished: ptr.To(ttl),
				Targets: []networkv1alpha1.TargetEndpoint{
					{Name: "k8s-api", Type: networkv1alpha1.TargetTypeService, Host: "kubernetes", Namespace: namespace, Port: 443, TimeoutSeconds: 3},
					{Name: "github", Type: networkv1alpha1.TargetTypeExternal, Host: "api.github.com", Port: 443, TimeoutSeconds: 5},
				},
			},
		}
		Expect(k8sClient.Create(ctx, tc)).To(Succeed())
		defer func() { _ = k8sClient.Delete(ctx, tc) }()

		fakeLogs := "RESULT|k8s-api|TCP|kubernetes.default.svc.cluster.local:443|PASS|TCP connection succeeded\n" +
			"RESULT|github|TCP|api.github.com:443|PASS|TCP connection succeeded\n"
		r := newReconciler(fakeLogs)
		req := reconcile.Request{NamespacedName: types.NamespacedName{Name: crName, Namespace: namespace}}

		// 1st Reconcile: create Job
		res, err := r.Reconcile(ctx, req)
		Expect(err).NotTo(HaveOccurred())
		Expect(res.RequeueAfter).To(BeNumerically(">", 0))

		jobName := fmt.Sprintf("traffic-probe-%s-g1", crName)
		var job batchv1.Job
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: jobName, Namespace: namespace}, &job)).To(Succeed())

		// Verify Job configuration
		Expect(job.Spec.TTLSecondsAfterFinished).To(Equal(ptr.To(int32(120))))
		Expect(job.Spec.BackoffLimit).To(Equal(ptr.To(int32(0))))
		Expect(job.Spec.Template.Labels).To(HaveKeyWithValue("app", "frontend"))
		Expect(job.Spec.Template.Labels).To(HaveKeyWithValue("tier", "web"))
		Expect(job.Spec.Template.Labels).To(HaveKeyWithValue("traffic-operator/probe", strTrue))
		Expect(job.Spec.Template.Spec.ServiceAccountName).To(Equal(defaultServiceAccount))
		Expect(job.Spec.Template.Spec.AutomountServiceAccountToken).To(Equal(ptr.To(false)))
		Expect(job.OwnerReferences).To(HaveLen(1))
		Expect(job.OwnerReferences[0].Name).To(Equal(crName))

		// Verify Pod & Container SecurityContext
		podSec := job.Spec.Template.Spec.SecurityContext
		Expect(podSec).NotTo(BeNil())
		Expect(podSec.RunAsNonRoot).To(Equal(ptr.To(true)))
		Expect(podSec.RunAsUser).To(Equal(ptr.To(int64(65534))))
		Expect(podSec.RunAsGroup).To(Equal(ptr.To(int64(65534))))
		Expect(podSec.SeccompProfile.Type).To(Equal(corev1.SeccompProfileTypeRuntimeDefault))

		containerSec := job.Spec.Template.Spec.Containers[0].SecurityContext
		Expect(containerSec).NotTo(BeNil())
		Expect(containerSec.AllowPrivilegeEscalation).To(Equal(ptr.To(false)))
		Expect(containerSec.ReadOnlyRootFilesystem).To(Equal(ptr.To(true)))
		Expect(containerSec.Capabilities.Drop).To(ContainElement(corev1.Capability("ALL")))

		// 2nd Reconcile: collect results after Job finishes
		simulateJobCompletion(jobName)
		res, err = r.Reconcile(ctx, req)
		Expect(err).NotTo(HaveOccurred())
		Expect(res.RequeueAfter).To(BeZero())

		var updated networkv1alpha1.TrafficCheck
		Expect(k8sClient.Get(ctx, req.NamespacedName, &updated)).To(Succeed())
		Expect(updated.Status.ObservedGeneration).To(Equal(int64(1)))
		Expect(updated.Status.Results).To(HaveLen(2))
		Expect(updated.Status.Results[0].Protocol).To(Equal(networkv1alpha1.ProtocolTCP))
		Expect(updated.Status.Results[0].Reachable).To(BeTrue())
		Expect(updated.Status.Results[1].Reachable).To(BeTrue())

		readyCond := meta.FindStatusCondition(updated.Status.Conditions, "Ready")
		Expect(readyCond).NotTo(BeNil())
		Expect(readyCond.Status).To(Equal(metav1.ConditionTrue))
		Expect(readyCond.Reason).To(Equal("AllTargetsMatched"))
	})

	It("sets Ready=False when any target fails", func() {
		crName := "check-fail"
		tc := &networkv1alpha1.TrafficCheck{
			ObjectMeta: metav1.ObjectMeta{
				Name:      crName,
				Namespace: namespace,
			},
			Spec: networkv1alpha1.TrafficCheckSpec{
				PodLabels: map[string]string{"app": "isolated"},
				Targets: []networkv1alpha1.TargetEndpoint{
					{Name: "blocked-ext", Type: networkv1alpha1.TargetTypeExternal, Host: testExampleHost, Port: 443},
				},
			},
		}
		Expect(k8sClient.Create(ctx, tc)).To(Succeed())
		defer func() { _ = k8sClient.Delete(ctx, tc) }()

		r := newReconciler("RESULT|blocked-ext|TCP|example.com:443|FAIL|TCP connection timed out or refused\n")
		req := reconcile.Request{NamespacedName: types.NamespacedName{Name: crName, Namespace: namespace}}

		_, err := r.Reconcile(ctx, req)
		Expect(err).NotTo(HaveOccurred())

		simulateJobCompletion(fmt.Sprintf("traffic-probe-%s-g1", crName))
		_, err = r.Reconcile(ctx, req)
		Expect(err).NotTo(HaveOccurred())

		var updated networkv1alpha1.TrafficCheck
		Expect(k8sClient.Get(ctx, req.NamespacedName, &updated)).To(Succeed())
		Expect(updated.Status.Results).To(HaveLen(1))
		Expect(updated.Status.Results[0].Reachable).To(BeFalse())

		readyCond := meta.FindStatusCondition(updated.Status.Conditions, "Ready")
		Expect(readyCond).NotTo(BeNil())
		Expect(readyCond.Status).To(Equal(metav1.ConditionFalse))
		Expect(readyCond.Reason).To(Equal("AccessExpectationFailed"))
	})

	It("sets Ready=True when traffic is correctly blocked", func() {
		crName := "check-blocked-traffic"
		expectAccess := false
		tc := &networkv1alpha1.TrafficCheck{
			ObjectMeta: metav1.ObjectMeta{Name: crName, Namespace: namespace},
			Spec: networkv1alpha1.TrafficCheckSpec{
				Targets: []networkv1alpha1.TargetEndpoint{{
					Name: "blocked-db", Type: networkv1alpha1.TargetTypeService, Host: "database", Namespace: "production", Port: 5432, Access: &expectAccess,
				}},
			},
		}
		Expect(k8sClient.Create(ctx, tc)).To(Succeed())
		defer func() { _ = k8sClient.Delete(ctx, tc) }()

		r := newReconciler("RESULT|blocked-db|TCP|database.production.svc.cluster.local:5432|PASS|false|TCP connection timed out or refused\n")
		req := reconcile.Request{NamespacedName: types.NamespacedName{Name: crName, Namespace: namespace}}
		_, err := r.Reconcile(ctx, req)
		Expect(err).NotTo(HaveOccurred())

		jobName := fmt.Sprintf("traffic-probe-%s-g1", crName)
		simulateJobCompletion(jobName)
		_, err = r.Reconcile(ctx, req)
		Expect(err).NotTo(HaveOccurred())

		var updated networkv1alpha1.TrafficCheck
		Expect(k8sClient.Get(ctx, req.NamespacedName, &updated)).To(Succeed())
		Expect(updated.Status.Results).To(HaveLen(1))
		Expect(updated.Status.Results[0].Reachable).To(BeFalse())
		readyCond := meta.FindStatusCondition(updated.Status.Conditions, "Ready")
		Expect(readyCond).NotTo(BeNil())
		Expect(readyCond.Status).To(Equal(metav1.ConditionTrue))
		Expect(readyCond.Reason).To(Equal("AllTargetsMatched"))
	})

	It("probes UDP targets with -u flag and records UDP protocol in status", func() {
		crName := "check-udp"
		tc := &networkv1alpha1.TrafficCheck{
			ObjectMeta: metav1.ObjectMeta{
				Name:      crName,
				Namespace: namespace,
			},
			Spec: networkv1alpha1.TrafficCheckSpec{
				Targets: []networkv1alpha1.TargetEndpoint{
					{
						Name:           "cluster-dns",
						Type:           networkv1alpha1.TargetTypeService,
						Protocol:       networkv1alpha1.ProtocolUDP,
						Host:           "kube-dns",
						Namespace:      "kube-system",
						Port:           53,
						TimeoutSeconds: 3,
					},
				},
			},
		}
		Expect(k8sClient.Create(ctx, tc)).To(Succeed())
		defer func() { _ = k8sClient.Delete(ctx, tc) }()

		r := newReconciler("RESULT|cluster-dns|UDP|kube-dns.kube-system.svc.cluster.local:53|PASS|UDP connection succeeded\n")
		req := reconcile.Request{NamespacedName: types.NamespacedName{Name: crName, Namespace: namespace}}

		_, err := r.Reconcile(ctx, req)
		Expect(err).NotTo(HaveOccurred())

		jobName := fmt.Sprintf("traffic-probe-%s-g1", crName)
		var job batchv1.Job
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: jobName, Namespace: namespace}, &job)).To(Succeed())
		Expect(job.Spec.Template.Spec.Containers[0].Command[2]).To(ContainSubstring("nc -z -u -w 3 kube-dns.kube-system.svc.cluster.local 53"))

		simulateJobCompletion(jobName)
		_, err = r.Reconcile(ctx, req)
		Expect(err).NotTo(HaveOccurred())

		var updated networkv1alpha1.TrafficCheck
		Expect(k8sClient.Get(ctx, req.NamespacedName, &updated)).To(Succeed())
		Expect(updated.Status.Results).To(HaveLen(1))
		Expect(updated.Status.Results[0].Protocol).To(Equal(networkv1alpha1.ProtocolUDP))
		Expect(updated.Status.Results[0].Reachable).To(BeTrue())
		Expect(updated.Status.Results[0].Message).To(Equal("UDP connection succeeded"))
	})

	It("probes HTTP and HTTPS targets with curl and records HTTP status codes", func() {
		crName := "check-http"
		tc := &networkv1alpha1.TrafficCheck{
			ObjectMeta: metav1.ObjectMeta{
				Name:      crName,
				Namespace: namespace,
			},
			Spec: networkv1alpha1.TrafficCheckSpec{
				Targets: []networkv1alpha1.TargetEndpoint{
					{
						Name:               "k8s-livez",
						Type:               networkv1alpha1.TargetTypeService,
						Protocol:           networkv1alpha1.ProtocolHTTPS,
						Host:               "kubernetes",
						Namespace:          namespace,
						Port:               443,
						Path:               "/livez",
						ExpectedStatusCode: 200,
						InsecureSkipVerify: true,
						TimeoutSeconds:     3,
					},
				},
			},
		}
		Expect(k8sClient.Create(ctx, tc)).To(Succeed())
		defer func() { _ = k8sClient.Delete(ctx, tc) }()

		r := newReconciler("RESULT|k8s-livez|HTTPS|https://kubernetes.default.svc.cluster.local:443/livez|PASS|HTTP 200\n")
		req := reconcile.Request{NamespacedName: types.NamespacedName{Name: crName, Namespace: namespace}}

		_, err := r.Reconcile(ctx, req)
		Expect(err).NotTo(HaveOccurred())

		jobName := fmt.Sprintf("traffic-probe-%s-g1", crName)
		var job batchv1.Job
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: jobName, Namespace: namespace}, &job)).To(Succeed())
		cmd := job.Spec.Template.Spec.Containers[0].Command[2]
		Expect(cmd).To(ContainSubstring(`curl -sS -o /dev/null -w "%{http_code}" --max-time 3 -k "https://kubernetes.default.svc.cluster.local:443/livez"`))
		Expect(cmd).To(ContainSubstring(`[ "$code" -eq 200 ]`))

		simulateJobCompletion(jobName)
		_, err = r.Reconcile(ctx, req)
		Expect(err).NotTo(HaveOccurred())

		var updated networkv1alpha1.TrafficCheck
		Expect(k8sClient.Get(ctx, req.NamespacedName, &updated)).To(Succeed())
		Expect(updated.Status.Results).To(HaveLen(1))
		Expect(updated.Status.Results[0].Protocol).To(Equal(networkv1alpha1.ProtocolHTTPS))
		Expect(updated.Status.Results[0].Reachable).To(BeTrue())
		Expect(updated.Status.Results[0].Message).To(Equal("HTTP 200"))
	})

	It("creates a new Job when the CR spec is updated", func() {
		crName := "check-update"
		tc := &networkv1alpha1.TrafficCheck{
			ObjectMeta: metav1.ObjectMeta{
				Name:      crName,
				Namespace: namespace,
			},
			Spec: networkv1alpha1.TrafficCheckSpec{
				Targets: []networkv1alpha1.TargetEndpoint{
					{Name: "svc-a", Type: networkv1alpha1.TargetTypeService, Host: "svc-a", Port: 80},
				},
			},
		}
		Expect(k8sClient.Create(ctx, tc)).To(Succeed())
		defer func() { _ = k8sClient.Delete(ctx, tc) }()

		r := newReconciler("RESULT|svc-a|TCP|svc-a.default.svc.cluster.local:80|PASS|TCP connection succeeded\n")
		req := reconcile.Request{NamespacedName: types.NamespacedName{Name: crName, Namespace: namespace}}

		// Run generation 1 to completion
		_, err := r.Reconcile(ctx, req)
		Expect(err).NotTo(HaveOccurred())
		simulateJobCompletion(fmt.Sprintf("traffic-probe-%s-g1", crName))
		_, err = r.Reconcile(ctx, req)
		Expect(err).NotTo(HaveOccurred())

		// Update spec -> bumps metadata.generation to 2
		Expect(k8sClient.Get(ctx, req.NamespacedName, tc)).To(Succeed())
		tc.Spec.TTLSecondsAfterFinished = ptr.To(int32(30))
		Expect(k8sClient.Update(ctx, tc)).To(Succeed())

		// Reconcile should create a new Job for generation 2
		_, err = r.Reconcile(ctx, req)
		Expect(err).NotTo(HaveOccurred())

		var jobG2 batchv1.Job
		Expect(k8sClient.Get(ctx, types.NamespacedName{
			Name:      fmt.Sprintf("traffic-probe-%s-g2", crName),
			Namespace: namespace,
		}, &jobG2)).To(Succeed())
		Expect(jobG2.Spec.TTLSecondsAfterFinished).To(Equal(ptr.To(int32(30))))
	})

	It("stops reconciliation when stop-reconcile annotation is present", func() {
		crName := "check-stop-reconcile"
		tc := &networkv1alpha1.TrafficCheck{
			ObjectMeta: metav1.ObjectMeta{
				Name:      crName,
				Namespace: namespace,
				Annotations: map[string]string{
					networkv1alpha1.AnnotationStopReconcile: strTrue,
				},
			},
			Spec: networkv1alpha1.TrafficCheckSpec{
				Targets: []networkv1alpha1.TargetEndpoint{
					{Name: "svc-stop", Type: networkv1alpha1.TargetTypeService, Host: "svc-stop", Port: 80},
				},
			},
		}
		Expect(k8sClient.Create(ctx, tc)).To(Succeed())
		defer func() { _ = k8sClient.Delete(ctx, tc) }()

		r := newReconciler("RESULT|svc-stop|TCP|svc-stop.default.svc.cluster.local:80|PASS|TCP connection succeeded\n")
		req := reconcile.Request{NamespacedName: types.NamespacedName{Name: crName, Namespace: namespace}}

		res, err := r.Reconcile(ctx, req)
		Expect(err).NotTo(HaveOccurred())
		Expect(res.RequeueAfter).To(BeZero())

		jobName := fmt.Sprintf("traffic-probe-%s-g1", crName)
		var job batchv1.Job
		err = k8sClient.Get(ctx, types.NamespacedName{Name: jobName, Namespace: namespace}, &job)
		Expect(apierrors.IsNotFound(err)).To(BeTrue(), "expected no job to be created when stop-reconcile annotation is present")
	})

	It("reconciles trafficcheck after jobs are completed and deleted", func() {
		crName := "check-reconcile-after-delete"
		tc := &networkv1alpha1.TrafficCheck{
			ObjectMeta: metav1.ObjectMeta{
				Name:      crName,
				Namespace: namespace,
			},
			Spec: networkv1alpha1.TrafficCheckSpec{
				Targets: []networkv1alpha1.TargetEndpoint{
					{Name: "svc-b", Type: networkv1alpha1.TargetTypeService, Host: "svc-b", Port: 80},
				},
			},
		}
		Expect(k8sClient.Create(ctx, tc)).To(Succeed())
		defer func() { _ = k8sClient.Delete(ctx, tc) }()

		fakeLogs := "RESULT|svc-b|TCP|svc-b.default.svc.cluster.local:80|PASS|TCP connection succeeded\n"
		r := newReconciler(fakeLogs)
		req := reconcile.Request{NamespacedName: types.NamespacedName{Name: crName, Namespace: namespace}}

		// 1. Initial reconcile: creates first job
		_, err := r.Reconcile(ctx, req)
		Expect(err).NotTo(HaveOccurred())

		jobName := fmt.Sprintf("traffic-probe-%s-g1", crName)
		var job batchv1.Job
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: jobName, Namespace: namespace}, &job)).To(Succeed())

		// 2. Complete first job and collect results
		simulateJobCompletion(jobName)
		_, err = r.Reconcile(ctx, req)
		Expect(err).NotTo(HaveOccurred())

		var updated networkv1alpha1.TrafficCheck
		Expect(k8sClient.Get(ctx, req.NamespacedName, &updated)).To(Succeed())
		Expect(updated.Status.Results).To(HaveLen(1))
		Expect(updated.Status.Results[0].Reachable).To(BeTrue())

		// Verify job is marked as collected
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: jobName, Namespace: namespace}, &job)).To(Succeed())
		Expect(job.Annotations).To(HaveKeyWithValue(networkv1alpha1.AnnotationJobCollected, strTrue))

		// While job still exists, another reconcile should not create another job or re-evaluate
		res, err := r.Reconcile(ctx, req)
		Expect(err).NotTo(HaveOccurred())
		Expect(res.RequeueAfter).To(BeZero())

		// 3. Delete the completed Job (simulating TTL cleanup or manual deletion)
		deleteJobAndPods(jobName)

		Eventually(func() bool {
			var oldJob batchv1.Job
			return apierrors.IsNotFound(k8sClient.Get(ctx, types.NamespacedName{Name: jobName, Namespace: namespace}, &oldJob))
		}, "5s", "500ms").Should(BeTrue())

		// Deletion ends this generation's run; it must not start another Job.
		res, err = r.Reconcile(ctx, req)
		Expect(err).NotTo(HaveOccurred())
		Expect(res.RequeueAfter).To(BeZero())
		Expect(apierrors.IsNotFound(k8sClient.Get(ctx, types.NamespacedName{Name: jobName, Namespace: namespace}, &job))).To(BeTrue())
	})

	It("does not recreate a deleted job when stop-reconcile is removed", func() {
		crName := "check-stop-after-delete"
		tc := &networkv1alpha1.TrafficCheck{
			ObjectMeta: metav1.ObjectMeta{
				Name:      crName,
				Namespace: namespace,
			},
			Spec: networkv1alpha1.TrafficCheckSpec{
				Targets: []networkv1alpha1.TargetEndpoint{
					{Name: "svc-c", Type: networkv1alpha1.TargetTypeService, Host: "svc-c", Port: 80},
				},
			},
		}
		Expect(k8sClient.Create(ctx, tc)).To(Succeed())
		defer func() { _ = k8sClient.Delete(ctx, tc) }()

		r := newReconciler("RESULT|svc-c|TCP|svc-c.default.svc.cluster.local:80|PASS|TCP connection succeeded\n")
		req := reconcile.Request{NamespacedName: types.NamespacedName{Name: crName, Namespace: namespace}}

		// 1. Initial reconcile and job completion
		_, err := r.Reconcile(ctx, req)
		Expect(err).NotTo(HaveOccurred())

		jobName := fmt.Sprintf("traffic-probe-%s-g1", crName)
		simulateJobCompletion(jobName)
		_, err = r.Reconcile(ctx, req)
		Expect(err).NotTo(HaveOccurred())

		// 2. Set stop-reconcile annotation on TrafficCheck
		Expect(k8sClient.Get(ctx, req.NamespacedName, tc)).To(Succeed())
		if tc.Annotations == nil {
			tc.Annotations = make(map[string]string)
		}
		tc.Annotations[networkv1alpha1.AnnotationStopReconcile] = strTrue
		Expect(k8sClient.Update(ctx, tc)).To(Succeed())

		// 3. Delete the completed Job
		deleteJobAndPods(jobName)

		Eventually(func() bool {
			var oldJob batchv1.Job
			return apierrors.IsNotFound(k8sClient.Get(ctx, types.NamespacedName{Name: jobName, Namespace: namespace}, &oldJob))
		}, "5s", "500ms").Should(BeTrue())

		// 4. Reconcile while stop-reconcile is set -> should NOT recreate the job
		res, err := r.Reconcile(ctx, req)
		Expect(err).NotTo(HaveOccurred())
		Expect(res.RequeueAfter).To(BeZero())

		var job batchv1.Job
		err = k8sClient.Get(ctx, types.NamespacedName{Name: jobName, Namespace: namespace}, &job)
		Expect(apierrors.IsNotFound(err)).To(BeTrue(), "job should not be created while stop-reconcile is set")

		// 5. Removing the pause does not restart a generation whose Job was deleted.
		Expect(k8sClient.Get(ctx, req.NamespacedName, tc)).To(Succeed())
		delete(tc.Annotations, networkv1alpha1.AnnotationStopReconcile)
		Expect(k8sClient.Update(ctx, tc)).To(Succeed())

		res, err = r.Reconcile(ctx, req)
		Expect(err).NotTo(HaveOccurred())
		Expect(res.RequeueAfter).To(BeZero())
		Expect(apierrors.IsNotFound(k8sClient.Get(ctx, types.NamespacedName{Name: jobName, Namespace: namespace}, &job))).To(BeTrue())
	})

	It("triggers reconciliation on demand via re-run annotation", func() {
		crName := "check-rerun-annotation"
		tc := &networkv1alpha1.TrafficCheck{
			ObjectMeta: metav1.ObjectMeta{
				Name:      crName,
				Namespace: namespace,
			},
			Spec: networkv1alpha1.TrafficCheckSpec{
				Targets: []networkv1alpha1.TargetEndpoint{
					{Name: "svc-d", Type: networkv1alpha1.TargetTypeService, Host: "svc-d", Port: 80},
				},
			},
		}
		Expect(k8sClient.Create(ctx, tc)).To(Succeed())
		defer func() { _ = k8sClient.Delete(ctx, tc) }()

		r := newReconciler("RESULT|svc-d|TCP|svc-d.default.svc.cluster.local:80|PASS|TCP connection succeeded\n")
		req := reconcile.Request{NamespacedName: types.NamespacedName{Name: crName, Namespace: namespace}}

		// Initial run
		_, err := r.Reconcile(ctx, req)
		Expect(err).NotTo(HaveOccurred())
		jobName := fmt.Sprintf("traffic-probe-%s-g1", crName)
		simulateJobCompletion(jobName)
		_, err = r.Reconcile(ctx, req)
		Expect(err).NotTo(HaveOccurred())

		// Set re-run annotation while job still exists
		Expect(k8sClient.Get(ctx, req.NamespacedName, tc)).To(Succeed())
		if tc.Annotations == nil {
			tc.Annotations = make(map[string]string)
		}
		tc.Annotations[networkv1alpha1.AnnotationReRun] = strTrue
		Expect(k8sClient.Update(ctx, tc)).To(Succeed())

		// Reconcile deletes the existing job and requests requeue
		res, err := r.Reconcile(ctx, req)
		Expect(err).NotTo(HaveOccurred())
		Expect(res.RequeueAfter).To(BeNumerically(">", 0))

		// Once deleted, next reconcile creates a new job
		deleteJobAndPods(jobName)

		Eventually(func() error {
			_, err = r.Reconcile(ctx, req)
			if err != nil {
				return err
			}
			var job batchv1.Job
			return k8sClient.Get(ctx, types.NamespacedName{Name: jobName, Namespace: namespace}, &job)
		}, "10s", "1s").Should(Succeed())
	})
})
