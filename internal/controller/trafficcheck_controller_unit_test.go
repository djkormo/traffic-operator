package controller

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	networkv1alpha1 "github.com/djkormo/traffic-operator/api/v1alpha1"
)

type failingListClient struct {
	client.Client
}

func (f failingListClient) List(ctx context.Context, list client.ObjectList, opts ...client.ListOption) error {
	return fmt.Errorf("simulated list failure")
}

type customHookClient struct {
	client.Client
	createErr error
	deleteErr error
	statusErr error
}

func (c customHookClient) Create(ctx context.Context, obj client.Object, opts ...client.CreateOption) error {
	if c.createErr != nil {
		return c.createErr
	}
	return c.Client.Create(ctx, obj, opts...)
}

func (c customHookClient) Delete(ctx context.Context, obj client.Object, opts ...client.DeleteOption) error {
	if c.deleteErr != nil {
		return c.deleteErr
	}
	return c.Client.Delete(ctx, obj, opts...)
}

func (c customHookClient) Status() client.SubResourceWriter {
	if c.statusErr != nil {
		return failingStatusWriter{err: c.statusErr}
	}
	return c.Client.Status()
}

type failingStatusWriter struct {
	err error
}

func (f failingStatusWriter) Create(ctx context.Context, obj client.Object, subResource client.Object, opts ...client.SubResourceCreateOption) error {
	return f.err
}

func (f failingStatusWriter) Update(ctx context.Context, obj client.Object, opts ...client.SubResourceUpdateOption) error {
	return f.err
}

func (f failingStatusWriter) Patch(ctx context.Context, obj client.Object, patch client.Patch, opts ...client.SubResourcePatchOption) error {
	return f.err
}

var _ = Describe("TrafficCheck Controller Unit Tests and Edge Cases", func() {
	const (
		unitNamespace = "default"
		valOne        = "1"
	)
	ctx := context.Background()

	newReconciler := func(fakeLogs string) *TrafficCheckReconciler {
		return &TrafficCheckReconciler{
			Client:            k8sClient,
			Scheme:            k8sClient.Scheme(),
			DefaultProbeImage: testNetshootImg,
			LogReader: func(ctx context.Context, ns, podName string) (string, error) {
				return fakeLogs, nil
			},
		}
	}

	Describe("isReconciliationStopped", func() {
		It("returns false when annotations is nil", func() {
			tc := &networkv1alpha1.TrafficCheck{}
			Expect(isReconciliationStopped(tc)).To(BeFalse())
		})

		It("returns true for various stop-reconcile values", func() {
			for _, val := range []string{strTrue, "True", valOne, strYes, "YES"} {
				tc := &networkv1alpha1.TrafficCheck{
					ObjectMeta: metav1.ObjectMeta{
						Annotations: map[string]string{networkv1alpha1.AnnotationStopReconcile: val},
					},
				}
				Expect(isReconciliationStopped(tc)).To(BeTrue(), "expected true for %s", val)
			}
		})

		It("returns true for paused annotations", func() {
			for _, key := range []string{networkv1alpha1.AnnotationPaused, networkv1alpha1.AnnotationK8sPaused} {
				for _, val := range []string{strTrue, valOne, strYes} {
					tc := &networkv1alpha1.TrafficCheck{
						ObjectMeta: metav1.ObjectMeta{
							Annotations: map[string]string{key: val},
						},
					}
					Expect(isReconciliationStopped(tc)).To(BeTrue(), "expected true for %s=%s", key, val)
				}
			}
		})

		It("returns true for reconcile annotation stop keywords", func() {
			for _, val := range []string{"disabled", "false", "stop", "paused", "PAUSED"} {
				tc := &networkv1alpha1.TrafficCheck{
					ObjectMeta: metav1.ObjectMeta{
						Annotations: map[string]string{networkv1alpha1.AnnotationReconcile: val},
					},
				}
				Expect(isReconciliationStopped(tc)).To(BeTrue(), "expected true for reconcile=%s", val)
			}
		})

		It("returns false when annotations do not stop reconciliation", func() {
			tc := &networkv1alpha1.TrafficCheck{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: map[string]string{
						networkv1alpha1.AnnotationStopReconcile: "no",
						networkv1alpha1.AnnotationReconcile:     "active",
					},
				},
			}
			Expect(isReconciliationStopped(tc)).To(BeFalse())
		})
	})

	Describe("isReRunRequested", func() {
		It("returns false when annotations is nil", func() {
			tc := &networkv1alpha1.TrafficCheck{}
			Expect(isReRunRequested(tc)).To(BeFalse())
		})

		It("returns true for re-run annotation values", func() {
			for _, val := range []string{strTrue, valOne, strYes} {
				tc := &networkv1alpha1.TrafficCheck{
					ObjectMeta: metav1.ObjectMeta{
						Annotations: map[string]string{networkv1alpha1.AnnotationReRun: val},
					},
				}
				Expect(isReRunRequested(tc)).To(BeTrue(), "expected true for re-run=%s", val)
			}
		})

		It("returns true for reconcile annotation re-run keywords", func() {
			for _, val := range []string{"now", "force", "NOW"} {
				tc := &networkv1alpha1.TrafficCheck{
					ObjectMeta: metav1.ObjectMeta{
						Annotations: map[string]string{networkv1alpha1.AnnotationReconcile: val},
					},
				}
				Expect(isReRunRequested(tc)).To(BeTrue(), "expected true for reconcile=%s", val)
			}
		})

		It("returns false for non-rerun values", func() {
			tc := &networkv1alpha1.TrafficCheck{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: map[string]string{
						networkv1alpha1.AnnotationReRun:     "no",
						networkv1alpha1.AnnotationReconcile: "other",
					},
				},
			}
			Expect(isReRunRequested(tc)).To(BeFalse())
		})
	})

	Describe("buildProbeJob and command builders", func() {
		r := &TrafficCheckReconciler{DefaultProbeImage: testNetshootImg}

		It("uses default probe image and default TTL when unspecified", func() {
			tc := &networkv1alpha1.TrafficCheck{
				ObjectMeta: metav1.ObjectMeta{Name: "def-job", Namespace: unitNamespace},
				Spec: networkv1alpha1.TrafficCheckSpec{
					Targets: []networkv1alpha1.TargetEndpoint{
						{Name: "t1", Type: networkv1alpha1.TargetTypeExternal, Host: testExampleHost, Port: 80},
					},
				},
			}
			job := r.buildProbeJob(tc, "probe-job")
			Expect(job.Spec.Template.Spec.Containers[0].Image).To(Equal(testNetshootImg))
			Expect(*job.Spec.TTLSecondsAfterFinished).To(Equal(int32(60)))

			tc.Spec.ProbeImage = "custom/netshoot:v1"
			job = r.buildProbeJob(tc, "probe-job-custom-image")
			Expect(job.Spec.Template.Spec.Containers[0].Image).To(Equal("custom/netshoot:v1"))
		})

		It("builds HTTP commands with default path, timeout, and without insecure flag", func() {
			t := networkv1alpha1.TargetEndpoint{
				Name:     "http-default",
				Type:     networkv1alpha1.TargetTypeExternal,
				Protocol: networkv1alpha1.ProtocolHTTPS,
				Host:     "secure.example.com",
				Port:     443,
			}
			cmd := buildTargetCommand(t, unitNamespace)
			Expect(cmd).To(ContainSubstring("--max-time 5"))
			Expect(cmd).To(ContainSubstring(`"https://secure.example.com:443/"`))
			Expect(cmd).NotTo(ContainSubstring("-k"))
			Expect(cmd).To(ContainSubstring(`[ "$code" -ge 200 ] && [ "$code" -lt 400 ]`))
		})

		It("defaults protocol to TCP when protocol is empty", func() {
			t := networkv1alpha1.TargetEndpoint{
				Name: "tcp-default",
				Type: networkv1alpha1.TargetTypeService,
				Host: "mysvc",
				Port: 8080,
			}
			cmd := buildTargetCommand(t, "custom-ns")
			Expect(cmd).To(ContainSubstring("mysvc.custom-ns.svc.cluster.local 8080"))
			Expect(cmd).To(ContainSubstring("nc -z -w 5"))
		})

		It("expects blocked connectivity when access is false", func() {
			access := false
			target := networkv1alpha1.TargetEndpoint{
				Name:   "blocked-target",
				Type:   networkv1alpha1.TargetTypeExternal,
				Host:   testExampleHost,
				Port:   443,
				Access: &access,
			}
			cmd := buildTargetCommand(target, unitNamespace)
			Expect(cmd).To(ContainSubstring(`[ "$reachable" = "false" ]`))
			Expect(cmd).To(ContainSubstring(`RESULT|blocked-target|TCP|example.com:443|$check|$reachable|$message`))
		})
	})

	Describe("findJobPod", func() {
		It("returns error when no pods exist for job", func() {
			r := &TrafficCheckReconciler{Client: k8sClient}
			_, err := r.findJobPod(ctx, unitNamespace, "non-existent-job-xyz")
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("no pod found for job"))
		})

		It("handles findJobPod list error and selects latest pod correctly", func() {
			failingClient := failingListClient{Client: k8sClient}
			rFail := &TrafficCheckReconciler{Client: failingClient}
			_, err := rFail.findJobPod(ctx, unitNamespace, "job-xyz")
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(Equal("simulated list failure"))

			tOld := metav1.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
			tNew := metav1.Date(2025, 1, 2, 0, 0, 0, 0, time.UTC)
			podList := &corev1.PodList{
				Items: []corev1.Pod{
					{
						ObjectMeta: metav1.ObjectMeta{
							Name:              "pod-older",
							Namespace:         unitNamespace,
							Labels:            map[string]string{testLabelJobName: "multi-job"},
							CreationTimestamp: tOld,
						},
					},
					{
						ObjectMeta: metav1.ObjectMeta{
							Name:              "pod-newer",
							Namespace:         unitNamespace,
							Labels:            map[string]string{testLabelJobName: "multi-job"},
							CreationTimestamp: tNew,
						},
					},
				},
			}
			fakeCl := fake.NewClientBuilder().WithScheme(scheme.Scheme).WithLists(podList).Build()
			rFake := &TrafficCheckReconciler{Client: fakeCl}
			pod, err := rFake.findJobPod(ctx, unitNamespace, "multi-job")
			Expect(err).NotTo(HaveOccurred())
			Expect(pod.Name).To(Equal("pod-newer"))
		})
	})

	Describe("readPodLogs and collectResults", func() {
		It("returns error from collectResults when readPodLogs fails", func() {
			r := &TrafficCheckReconciler{
				LogReader: func(ctx context.Context, ns, podName string) (string, error) {
					return "", fmt.Errorf("pod log read failure")
				},
			}
			pod := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "p1", Namespace: unitNamespace}}
			_, _, _, err := r.collectResults(ctx, pod)
			Expect(err).To(MatchError("pod log read failure"))
		})

		It("ignores malformed log lines in collectResults", func() {
			r := &TrafficCheckReconciler{
				LogReader: func(ctx context.Context, ns, podName string) (string, error) {
					return "invalid line\nRESULT|incomplete|parts\nRESULT|ok|TCP|addr:80|PASS|ok message\n", nil
				},
			}
			pod := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "p2", Namespace: unitNamespace}}
			results, nonCompliantTargets, allPassed, err := r.collectResults(ctx, pod)
			Expect(err).NotTo(HaveOccurred())
			Expect(allPassed).To(BeTrue())
			Expect(results).To(HaveLen(1))
			Expect(results[0].Name).To(Equal("ok"))
			Expect(nonCompliantTargets).To(BeEmpty())
		})

		It("reports actual connectivity separately from the expectation result", func() {
			r := &TrafficCheckReconciler{
				LogReader: func(ctx context.Context, ns, podName string) (string, error) {
					return "RESULT|blocked|TCP|db:5432|PASS|false|TCP connection timed out\n" +
						"RESULT|unexpectedly-blocked|TCP|api:443|FAIL|false|TCP connection timed out\n" +
						"RESULT|unexpectedly-open|TCP|web:80|FAIL|true|TCP connection succeeded\n", nil
				},
			}
			pod := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "p3", Namespace: unitNamespace}}
			results, nonCompliantTargets, allPassed, err := r.collectResults(ctx, pod)
			Expect(err).NotTo(HaveOccurred())
			Expect(allPassed).To(BeFalse())
			Expect(results).To(HaveLen(3))
			Expect(results[0].Reachable).To(BeFalse())
			Expect(results[1].Reachable).To(BeFalse())
			Expect(results[0].Message).To(ContainSubstring("timed out"))
			Expect(nonCompliantTargets).To(Equal([]string{"unexpectedly-blocked", "unexpectedly-open"}))
		})

		It("calls Clientset GetLogs when LogReader is nil and handles errors and streams", func() {
			cs, err := kubernetes.NewForConfig(cfg)
			Expect(err).NotTo(HaveOccurred())
			r := &TrafficCheckReconciler{
				Clientset: cs,
			}
			pod := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "non-existent-pod-xyz", Namespace: unitNamespace}}
			_, err = r.readPodLogs(ctx, pod)
			Expect(err).To(HaveOccurred())

			// Test successful log streaming via test server
			ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				w.WriteHeader(http.StatusOK)
				_, _ = w.Write([]byte("RESULT|web|HTTP|http://example:80|PASS|HTTP 200\n"))
			}))
			defer ts.Close()
			streamCs, err := kubernetes.NewForConfig(&rest.Config{Host: ts.URL})
			Expect(err).NotTo(HaveOccurred())
			rStream := &TrafficCheckReconciler{Clientset: streamCs}
			logs, err := rStream.readPodLogs(ctx, pod)
			Expect(err).NotTo(HaveOccurred())
			Expect(logs).To(ContainSubstring("RESULT|web|HTTP"))
		})
	})

	Describe("SetupWithManager", func() {
		It("registers reconciler with manager successfully", func() {
			mgr, err := ctrl.NewManager(cfg, ctrl.Options{
				Scheme:  scheme.Scheme,
				Metrics: metricsserver.Options{BindAddress: "0"},
			})
			Expect(err).NotTo(HaveOccurred())
			r := &TrafficCheckReconciler{
				Client: mgr.GetClient(),
				Scheme: mgr.GetScheme(),
			}
			Expect(r.SetupWithManager(mgr)).To(Succeed())
		})
	})

	Describe("Reconcile edge cases", func() {
		It("ignores not-found errors when TrafficCheck CR is deleted", func() {
			r := newReconciler("")
			res, err := r.Reconcile(ctx, reconcile.Request{
				NamespacedName: types.NamespacedName{Name: "does-not-exist", Namespace: unitNamespace},
			})
			Expect(err).NotTo(HaveOccurred())
			Expect(res).To(Equal(reconcile.Result{}))
		})

		It("requeues when probe Job is still running (not finished yet)", func() {
			crName := "check-job-running"
			tc := &networkv1alpha1.TrafficCheck{
				ObjectMeta: metav1.ObjectMeta{Name: crName, Namespace: unitNamespace},
				Spec: networkv1alpha1.TrafficCheckSpec{
					Targets: []networkv1alpha1.TargetEndpoint{{Name: "t1", Type: networkv1alpha1.TargetTypeExternal, Host: testExampleHost, Port: 80}},
				},
			}
			Expect(k8sClient.Create(ctx, tc)).To(Succeed())
			defer func() { _ = k8sClient.Delete(ctx, tc) }()

			r := newReconciler("")
			req := reconcile.Request{NamespacedName: types.NamespacedName{Name: crName, Namespace: unitNamespace}}

			// 1st reconcile: creates job
			_, err := r.Reconcile(ctx, req)
			Expect(err).NotTo(HaveOccurred())
			var updated networkv1alpha1.TrafficCheck
			Expect(k8sClient.Get(ctx, req.NamespacedName, &updated)).To(Succeed())
			reconciledCond := meta.FindStatusCondition(updated.Status.Conditions, "Reconciled")
			Expect(reconciledCond).NotTo(BeNil())
			Expect(reconciledCond.Status).To(Equal(metav1.ConditionFalse))

			// 2nd reconcile: job exists with Succeeded=0 and Failed=0
			res, err := r.Reconcile(ctx, req)
			Expect(err).NotTo(HaveOccurred())
			Expect(res.RequeueAfter).To(Equal(3 * time.Second))
		})

		It("does not recreate a manually deleted running job", func() {
			crName := "check-job-manual-delete"
			tc := &networkv1alpha1.TrafficCheck{
				ObjectMeta: metav1.ObjectMeta{Name: crName, Namespace: unitNamespace},
				Spec: networkv1alpha1.TrafficCheckSpec{
					Targets: []networkv1alpha1.TargetEndpoint{{Name: "t1", Type: networkv1alpha1.TargetTypeExternal, Host: testExampleHost, Port: 80}},
				},
			}
			Expect(k8sClient.Create(ctx, tc)).To(Succeed())
			defer func() { _ = k8sClient.Delete(ctx, tc) }()

			r := newReconciler("")
			req := reconcile.Request{NamespacedName: types.NamespacedName{Name: crName, Namespace: unitNamespace}}
			_, err := r.Reconcile(ctx, req)
			Expect(err).NotTo(HaveOccurred())

			jobName := fmt.Sprintf("traffic-probe-%s-g1", crName)
			var job batchv1.Job
			Expect(k8sClient.Get(ctx, types.NamespacedName{Name: jobName, Namespace: unitNamespace}, &job)).To(Succeed())
			Expect(k8sClient.Delete(ctx, &job, client.PropagationPolicy(metav1.DeletePropagationBackground))).To(Succeed())
			Eventually(func() bool {
				return apierrors.IsNotFound(k8sClient.Get(ctx, types.NamespacedName{Name: jobName, Namespace: unitNamespace}, &job))
			}).Should(BeTrue())

			res, err := r.Reconcile(ctx, req)
			Expect(err).NotTo(HaveOccurred())
			Expect(res.RequeueAfter).To(BeZero())
			Expect(apierrors.IsNotFound(k8sClient.Get(ctx, types.NamespacedName{Name: jobName, Namespace: unitNamespace}, &job))).To(BeTrue())
		})

		It("requeues when probe Job is in the process of being deleted", func() {
			crName := "check-job-deleting"
			tc := &networkv1alpha1.TrafficCheck{
				ObjectMeta: metav1.ObjectMeta{Name: crName, Namespace: unitNamespace},
				Spec: networkv1alpha1.TrafficCheckSpec{
					Targets: []networkv1alpha1.TargetEndpoint{{Name: "t1", Type: networkv1alpha1.TargetTypeExternal, Host: testExampleHost, Port: 80}},
				},
			}
			Expect(k8sClient.Create(ctx, tc)).To(Succeed())
			defer func() { _ = k8sClient.Delete(ctx, tc) }()

			r := newReconciler("")
			req := reconcile.Request{NamespacedName: types.NamespacedName{Name: crName, Namespace: unitNamespace}}

			_, err := r.Reconcile(ctx, req)
			Expect(err).NotTo(HaveOccurred())

			jobName := fmt.Sprintf("traffic-probe-%s-g1", crName)
			var job batchv1.Job
			Expect(k8sClient.Get(ctx, types.NamespacedName{Name: jobName, Namespace: unitNamespace}, &job)).To(Succeed())

			// Add a finalizer so it enters DeletionTimestamp != nil state
			job.Finalizers = []string{"test.finalizer/block-delete"}
			Expect(k8sClient.Update(ctx, &job)).To(Succeed())
			Expect(k8sClient.Delete(ctx, &job)).To(Succeed())

			res, err := r.Reconcile(ctx, req)
			Expect(err).NotTo(HaveOccurred())
			Expect(res.RequeueAfter).To(Equal(1 * time.Second))

			// Clean up finalizer so job is removed
			Expect(k8sClient.Get(ctx, types.NamespacedName{Name: jobName, Namespace: unitNamespace}, &job)).To(Succeed())
			job.Finalizers = []string{}
			_ = k8sClient.Update(ctx, &job)
		})

		It("requeues with error if job pod cannot be found when job succeeds", func() {
			crName := "check-job-no-pod"
			tc := &networkv1alpha1.TrafficCheck{
				ObjectMeta: metav1.ObjectMeta{Name: crName, Namespace: unitNamespace},
				Spec: networkv1alpha1.TrafficCheckSpec{
					Targets: []networkv1alpha1.TargetEndpoint{{Name: "t1", Type: networkv1alpha1.TargetTypeExternal, Host: testExampleHost, Port: 80}},
				},
			}
			Expect(k8sClient.Create(ctx, tc)).To(Succeed())
			defer func() { _ = k8sClient.Delete(ctx, tc) }()

			r := newReconciler("")
			req := reconcile.Request{NamespacedName: types.NamespacedName{Name: crName, Namespace: unitNamespace}}

			_, err := r.Reconcile(ctx, req)
			Expect(err).NotTo(HaveOccurred())

			jobName := fmt.Sprintf("traffic-probe-%s-g1", crName)
			var job batchv1.Job
			Expect(k8sClient.Get(ctx, types.NamespacedName{Name: jobName, Namespace: unitNamespace}, &job)).To(Succeed())
			job.Status.Succeeded = 1
			Expect(k8sClient.Status().Update(ctx, &job)).To(Succeed())

			// Reconcile without creating the pod -> findJobPod returns error
			res, err := r.Reconcile(ctx, req)
			Expect(err).To(HaveOccurred())
			Expect(res.RequeueAfter).To(Equal(3 * time.Second))
		})

		It("returns error when collectResults fails during reconcile", func() {
			crName := "check-job-collect-err"
			tc := &networkv1alpha1.TrafficCheck{
				ObjectMeta: metav1.ObjectMeta{Name: crName, Namespace: unitNamespace},
				Spec: networkv1alpha1.TrafficCheckSpec{
					Targets: []networkv1alpha1.TargetEndpoint{{Name: "t1", Type: networkv1alpha1.TargetTypeExternal, Host: testExampleHost, Port: 80}},
				},
			}
			Expect(k8sClient.Create(ctx, tc)).To(Succeed())
			defer func() { _ = k8sClient.Delete(ctx, tc) }()

			r := &TrafficCheckReconciler{
				Client:            k8sClient,
				Scheme:            k8sClient.Scheme(),
				DefaultProbeImage: testNetshootImg,
				LogReader: func(ctx context.Context, ns, podName string) (string, error) {
					return "", fmt.Errorf("simulated log read failure")
				},
			}
			req := reconcile.Request{NamespacedName: types.NamespacedName{Name: crName, Namespace: unitNamespace}}

			_, err := r.Reconcile(ctx, req)
			Expect(err).NotTo(HaveOccurred())

			jobName := fmt.Sprintf("traffic-probe-%s-g1", crName)
			var job batchv1.Job
			Expect(k8sClient.Get(ctx, types.NamespacedName{Name: jobName, Namespace: unitNamespace}, &job)).To(Succeed())

			pod := &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:      jobName + "-pod",
					Namespace: unitNamespace,
					Labels:    map[string]string{testLabelJobName: jobName},
				},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{{Name: testContainerProbe, Image: testNetshootImg}},
				},
			}
			Expect(k8sClient.Create(ctx, pod)).To(Succeed())
			defer func() { _ = k8sClient.Delete(ctx, pod) }()

			job.Status.Succeeded = 1
			Expect(k8sClient.Status().Update(ctx, &job)).To(Succeed())

			_, err = r.Reconcile(ctx, req)
			Expect(err).To(MatchError("simulated log read failure"))
		})

		It("does not recreate a completed job after deletion", func() {
			crName := "check-cooldown"
			tc := &networkv1alpha1.TrafficCheck{
				ObjectMeta: metav1.ObjectMeta{Name: crName, Namespace: unitNamespace},
				Spec: networkv1alpha1.TrafficCheckSpec{
					Targets: []networkv1alpha1.TargetEndpoint{{Name: "t1", Type: networkv1alpha1.TargetTypeExternal, Host: testExampleHost, Port: 80}},
				},
			}
			Expect(k8sClient.Create(ctx, tc)).To(Succeed())
			defer func() { _ = k8sClient.Delete(ctx, tc) }()

			tc.Status.ObservedGeneration = tc.Generation
			tc.Status.Results = []networkv1alpha1.TargetResult{
				{
					Name:        "t1",
					Protocol:    networkv1alpha1.ProtocolTCP,
					Address:     "example.com:80",
					Reachable:   true,
					LastChecked: metav1.Now(),
				},
			}
			Expect(k8sClient.Status().Update(ctx, tc)).To(Succeed())

			r := newReconciler("")
			req := reconcile.Request{NamespacedName: types.NamespacedName{Name: crName, Namespace: unitNamespace}}

			res, err := r.Reconcile(ctx, req)
			Expect(err).NotTo(HaveOccurred())
			Expect(res.RequeueAfter).To(BeZero())

			var job batchv1.Job
			Expect(apierrors.IsNotFound(k8sClient.Get(ctx, types.NamespacedName{Name: "traffic-probe-" + crName + "-g1", Namespace: unitNamespace}, &job))).To(BeTrue())
		})

		It("handles AlreadyExists and generic error on Job creation", func() {
			crName := "check-create-already-exists"
			tc := &networkv1alpha1.TrafficCheck{
				ObjectMeta: metav1.ObjectMeta{Name: crName, Namespace: unitNamespace},
				Spec: networkv1alpha1.TrafficCheckSpec{
					Targets: []networkv1alpha1.TargetEndpoint{{Name: "t1", Type: networkv1alpha1.TargetTypeExternal, Host: testExampleHost, Port: 80}},
				},
			}
			Expect(k8sClient.Create(ctx, tc)).To(Succeed())
			defer func() { _ = k8sClient.Delete(ctx, tc) }()

			// Test AlreadyExists branch
			hookCl := customHookClient{
				Client:    k8sClient,
				createErr: apierrors.NewAlreadyExists(schema.GroupResource{Group: "batch", Resource: "jobs"}, "probe"),
			}
			r := &TrafficCheckReconciler{Client: hookCl, Scheme: k8sClient.Scheme(), DefaultProbeImage: testNetshootImg}
			res, err := r.Reconcile(ctx, reconcile.Request{NamespacedName: types.NamespacedName{Name: crName, Namespace: unitNamespace}})
			Expect(err).NotTo(HaveOccurred())
			Expect(res.RequeueAfter).To(Equal(2 * time.Second))

			// Test generic create error branch
			hookClGeneric := customHookClient{
				Client:    k8sClient,
				createErr: fmt.Errorf("simulated create failure"),
			}
			rGeneric := &TrafficCheckReconciler{Client: hookClGeneric, Scheme: k8sClient.Scheme(), DefaultProbeImage: testNetshootImg}
			_, err = rGeneric.Reconcile(ctx, reconcile.Request{NamespacedName: types.NamespacedName{Name: crName, Namespace: unitNamespace}})
			Expect(err).To(MatchError("simulated create failure"))
		})

		It("handles delete error during re-run", func() {
			crName := "check-rerun-del-err"
			tc := &networkv1alpha1.TrafficCheck{
				ObjectMeta: metav1.ObjectMeta{
					Name:      crName,
					Namespace: unitNamespace,
					Annotations: map[string]string{
						networkv1alpha1.AnnotationReRun: strTrue,
					},
				},
				Spec: networkv1alpha1.TrafficCheckSpec{
					Targets: []networkv1alpha1.TargetEndpoint{{Name: "t1", Type: networkv1alpha1.TargetTypeExternal, Host: testExampleHost, Port: 80}},
				},
			}
			Expect(k8sClient.Create(ctx, tc)).To(Succeed())
			defer func() { _ = k8sClient.Delete(ctx, tc) }()

			jobName := fmt.Sprintf("traffic-probe-%s-g1", crName)
			job := &batchv1.Job{
				ObjectMeta: metav1.ObjectMeta{Name: jobName, Namespace: unitNamespace},
				Spec: batchv1.JobSpec{
					Template: corev1.PodTemplateSpec{
						Spec: corev1.PodSpec{
							RestartPolicy: corev1.RestartPolicyNever,
							Containers:    []corev1.Container{{Name: testContainerProbe, Image: testNetshootImg}},
						},
					},
				},
			}
			Expect(k8sClient.Create(ctx, job)).To(Succeed())
			defer func() { _ = k8sClient.Delete(ctx, job) }()

			hookCl := customHookClient{
				Client:    k8sClient,
				deleteErr: fmt.Errorf("simulated delete failure"),
			}
			r := &TrafficCheckReconciler{Client: hookCl, Scheme: k8sClient.Scheme(), DefaultProbeImage: testNetshootImg}
			_, err := r.Reconcile(ctx, reconcile.Request{NamespacedName: types.NamespacedName{Name: crName, Namespace: unitNamespace}})
			Expect(err).To(MatchError("simulated delete failure"))
		})

		It("handles status update error", func() {
			crName := "check-status-err"
			tc := &networkv1alpha1.TrafficCheck{
				ObjectMeta: metav1.ObjectMeta{Name: crName, Namespace: unitNamespace},
				Spec: networkv1alpha1.TrafficCheckSpec{
					Targets: []networkv1alpha1.TargetEndpoint{{Name: "t1", Type: networkv1alpha1.TargetTypeExternal, Host: testExampleHost, Port: 80}},
				},
			}
			Expect(k8sClient.Create(ctx, tc)).To(Succeed())
			defer func() { _ = k8sClient.Delete(ctx, tc) }()

			r := newReconciler("RESULT|t1|TCP|example.com:80|PASS|ok\n")
			req := reconcile.Request{NamespacedName: types.NamespacedName{Name: crName, Namespace: unitNamespace}}
			_, err := r.Reconcile(ctx, req)
			Expect(err).NotTo(HaveOccurred())

			jobName := fmt.Sprintf("traffic-probe-%s-g1", crName)
			var job batchv1.Job
			Expect(k8sClient.Get(ctx, types.NamespacedName{Name: jobName, Namespace: unitNamespace}, &job)).To(Succeed())

			pod := &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:      jobName + "-pod",
					Namespace: unitNamespace,
					Labels:    map[string]string{testLabelJobName: jobName},
				},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{{Name: testContainerProbe, Image: testNetshootImg}},
				},
			}
			Expect(k8sClient.Create(ctx, pod)).To(Succeed())
			defer func() { _ = k8sClient.Delete(ctx, pod) }()

			job.Status.Succeeded = 1
			Expect(k8sClient.Status().Update(ctx, &job)).To(Succeed())

			hookCl := customHookClient{
				Client:    k8sClient,
				statusErr: fmt.Errorf("simulated status failure"),
			}
			r.Client = hookCl
			_, err = r.Reconcile(ctx, req)
			Expect(err).To(MatchError("simulated status failure"))
		})
	})
})
