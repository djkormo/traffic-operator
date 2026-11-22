package main

import (
	"context"
	"errors"
	"flag"
	"os"
	"testing"

	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"

	networkv1alpha1 "github.com/djkormo/traffic-operator/api/v1alpha1"
)

const (
	testValidHost          = "http://127.0.0.1:6443"
	testInvalidHost        = "cache:/:@/invalid-url"
	testMetricsBindAddress = "--metrics-bind-address=0"
	testProbeBindAddress   = "--health-probe-bind-address=0"
)

func TestSchemeInitialization(t *testing.T) {
	if !scheme.IsGroupRegistered(networkv1alpha1.GroupVersion.Group) {
		t.Errorf("expected group %s to be registered in scheme", networkv1alpha1.GroupVersion.Group)
	}
}

func TestParseFlagsDefaults(t *testing.T) {
	fs := flag.NewFlagSet("test", flag.ContinueOnError)
	opts, err := parseFlags(fs, []string{})
	if err != nil {
		t.Fatalf("unexpected error parsing default flags: %v", err)
	}
	if opts.MetricsAddr != ":8080" {
		t.Errorf("expected metrics-bind-address :8080, got %s", opts.MetricsAddr)
	}
	if opts.ProbeAddr != ":8081" {
		t.Errorf("expected health-probe-bind-address :8081, got %s", opts.ProbeAddr)
	}
	if opts.EnableLeaderElection != false {
		t.Errorf("expected leader-elect false, got %v", opts.EnableLeaderElection)
	}
	if !opts.ZapOpts.Development {
		t.Errorf("expected ZapOpts.Development true")
	}
}

func TestParseFlagsCustom(t *testing.T) {
	fs := flag.NewFlagSet("test", flag.ContinueOnError)
	args := []string{
		"--metrics-bind-address=:9090",
		"--health-probe-bind-address=:9091",
		"--leader-elect=true",
		"--zap-devel=false",
	}
	opts, err := parseFlags(fs, args)
	if err != nil {
		t.Fatalf("unexpected error parsing custom flags: %v", err)
	}
	if opts.MetricsAddr != ":9090" {
		t.Errorf("expected metrics-bind-address :9090, got %s", opts.MetricsAddr)
	}
	if opts.ProbeAddr != ":9091" {
		t.Errorf("expected health-probe-bind-address :9091, got %s", opts.ProbeAddr)
	}
	if opts.EnableLeaderElection != true {
		t.Errorf("expected leader-elect true, got %v", opts.EnableLeaderElection)
	}
	if opts.ZapOpts.Development {
		t.Errorf("expected ZapOpts.Development false")
	}
}

func TestParseFlagsError(t *testing.T) {
	fs := flag.NewFlagSet("test", flag.ContinueOnError)
	_, err := parseFlags(fs, []string{"--non-existent-flag"})
	if err == nil {
		t.Error("expected error parsing unknown flag, got nil")
	}
}

func TestSetupManagerSuccess(t *testing.T) {
	origSetupReconciler := setupReconciler
	defer func() { setupReconciler = origSetupReconciler }()
	setupReconciler = func(mgr ctrl.Manager, clientset *kubernetes.Clientset) error {
		return nil
	}

	cfg := &rest.Config{Host: testValidHost}
	opts := &Options{
		MetricsAddr:          "0",
		ProbeAddr:            "0",
		EnableLeaderElection: false,
	}

	mgr, err := setupManager(cfg, opts)
	if err != nil {
		t.Fatalf("setupManager failed: %v", err)
	}
	if mgr == nil {
		t.Fatal("expected non-nil manager")
	}
}

func TestSetupManagerInvalidConfig(t *testing.T) {
	cfg := &rest.Config{Host: testInvalidHost}
	opts := &Options{
		MetricsAddr:          "0",
		ProbeAddr:            "0",
		EnableLeaderElection: false,
	}

	mgr, err := setupManager(cfg, opts)
	if err == nil {
		t.Error("expected setupManager to fail with invalid config, got nil error")
	}
	if mgr != nil {
		t.Error("expected nil manager on failure")
	}
}

func TestSetupManagerClientsetError(t *testing.T) {
	origNewClientset := newClientset
	defer func() { newClientset = origNewClientset }()

	newClientset = func(c *rest.Config) (*kubernetes.Clientset, error) {
		return nil, errors.New("simulated clientset creation error")
	}

	cfg := &rest.Config{Host: testValidHost}
	opts := &Options{
		MetricsAddr:          "0",
		ProbeAddr:            "0",
		EnableLeaderElection: false,
	}

	mgr, err := setupManager(cfg, opts)
	if err == nil {
		t.Error("expected setupManager to fail when clientset creation fails")
	}
	if mgr != nil {
		t.Error("expected nil manager on failure")
	}
}

func TestSetupManagerReconcilerError(t *testing.T) {
	origSetupReconciler := setupReconciler
	defer func() { setupReconciler = origSetupReconciler }()

	setupReconciler = func(mgr ctrl.Manager, clientset *kubernetes.Clientset) error {
		return errors.New("simulated reconciler setup error")
	}

	cfg := &rest.Config{Host: testValidHost}
	opts := &Options{
		MetricsAddr:          "0",
		ProbeAddr:            "0",
		EnableLeaderElection: false,
	}

	mgr, err := setupManager(cfg, opts)
	if err == nil {
		t.Error("expected setupManager to fail when setupReconciler fails")
	}
	if mgr != nil {
		t.Error("expected nil manager on failure")
	}
}

func TestSetupManagerHealthChecksError(t *testing.T) {
	origSetupReconciler := setupReconciler
	origSetupHealthChecks := setupHealthChecks
	defer func() {
		setupReconciler = origSetupReconciler
		setupHealthChecks = origSetupHealthChecks
	}()

	setupReconciler = func(mgr ctrl.Manager, clientset *kubernetes.Clientset) error {
		return nil
	}
	setupHealthChecks = func(mgr ctrl.Manager) error {
		return errors.New("simulated health checks setup error")
	}

	cfg := &rest.Config{Host: testValidHost}
	opts := &Options{
		MetricsAddr:          "0",
		ProbeAddr:            "0",
		EnableLeaderElection: false,
	}

	mgr, err := setupManager(cfg, opts)
	if err == nil {
		t.Error("expected setupManager to fail when setupHealthChecks fails")
	}
	if mgr != nil {
		t.Error("expected nil manager on failure")
	}
}

func TestDefaultSetupReconciler(t *testing.T) {
	cfg := &rest.Config{Host: testValidHost}
	mgr, err := ctrl.NewManager(cfg, ctrl.Options{
		Scheme:                 scheme,
		Metrics:                metricsserver.Options{BindAddress: "0"},
		HealthProbeBindAddress: "0",
	})
	if err != nil {
		t.Fatalf("ctrl.NewManager failed: %v", err)
	}

	clientset, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		t.Fatalf("kubernetes.NewForConfig failed: %v", err)
	}

	// First registration should succeed
	if err := defaultSetupReconciler(mgr, clientset); err != nil {
		t.Fatalf("defaultSetupReconciler failed: %v", err)
	}

	// Duplicate registration should fail
	if err := defaultSetupReconciler(mgr, clientset); err == nil {
		t.Error("expected duplicate reconciler registration to fail, got nil")
	}
}

func TestDefaultSetupHealthChecks(t *testing.T) {
	cfg := &rest.Config{Host: testValidHost}
	mgr, err := ctrl.NewManager(cfg, ctrl.Options{
		Scheme:                 scheme,
		Metrics:                metricsserver.Options{BindAddress: "0"},
		HealthProbeBindAddress: "0",
	})
	if err != nil {
		t.Fatalf("ctrl.NewManager failed: %v", err)
	}

	if err := defaultSetupHealthChecks(mgr); err != nil {
		t.Fatalf("defaultSetupHealthChecks failed: %v", err)
	}
}

func TestDefaultSetupHealthChecksError(t *testing.T) {
	cfg := &rest.Config{Host: testValidHost}
	mgr, err := ctrl.NewManager(cfg, ctrl.Options{
		Scheme:                 scheme,
		Metrics:                metricsserver.Options{BindAddress: "0"},
		HealthProbeBindAddress: "0",
	})
	if err != nil {
		t.Fatalf("ctrl.NewManager failed: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_ = mgr.Start(ctx)

	if err := defaultSetupHealthChecks(mgr); err == nil {
		t.Error("expected defaultSetupHealthChecks to fail on started manager, got nil")
	}
}

type mockManagerWithReadyzError struct {
	ctrl.Manager
}

func (m *mockManagerWithReadyzError) AddHealthzCheck(name string, check healthz.Checker) error {
	return nil
}

func (m *mockManagerWithReadyzError) AddReadyzCheck(name string, check healthz.Checker) error {
	return errors.New("simulated readyz check error")
}

func TestDefaultSetupHealthChecksReadyzError(t *testing.T) {
	mockMgr := &mockManagerWithReadyzError{}
	if err := defaultSetupHealthChecks(mockMgr); err == nil {
		t.Error("expected error when AddReadyzCheck fails, got nil")
	}
}

func TestDefaultStartManager(t *testing.T) {
	cfg := &rest.Config{Host: testValidHost}
	mgr, err := ctrl.NewManager(cfg, ctrl.Options{
		Scheme:                 scheme,
		Metrics:                metricsserver.Options{BindAddress: "0"},
		HealthProbeBindAddress: "0",
	})
	if err != nil {
		t.Fatalf("ctrl.NewManager failed: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately

	// Starting with an already cancelled context should return quickly
	_ = defaultStartManager(mgr, ctx)
}

func TestRunSuccess(t *testing.T) {
	origGetConfig := getConfig
	origStartManager := startManager
	origSetupReconciler := setupReconciler
	origSetupSignalHandler := setupSignalHandler
	defer func() {
		getConfig = origGetConfig
		startManager = origStartManager
		setupReconciler = origSetupReconciler
		setupSignalHandler = origSetupSignalHandler
	}()

	setupSignalHandler = func() context.Context {
		return context.Background()
	}
	setupReconciler = func(mgr ctrl.Manager, clientset *kubernetes.Clientset) error {
		return nil
	}
	getConfig = func() *rest.Config {
		return &rest.Config{Host: testValidHost}
	}
	startManager = func(mgr ctrl.Manager, ctx context.Context) error {
		return nil
	}

	args := []string{
		testMetricsBindAddress,
		testProbeBindAddress,
	}
	if err := run(args); err != nil {
		t.Fatalf("run failed: %v", err)
	}
}

func TestRunHelp(t *testing.T) {
	if err := run([]string{"--help"}); err != nil {
		t.Fatalf("expected nil for --help, got: %v", err)
	}
}

func TestRunParseFlagsError(t *testing.T) {
	if err := run([]string{"--unknown-flag-123"}); err == nil {
		t.Error("expected error for unknown flag, got nil")
	}
}

func TestRunSetupManagerError(t *testing.T) {
	origGetConfig := getConfig
	defer func() { getConfig = origGetConfig }()

	getConfig = func() *rest.Config {
		return &rest.Config{Host: testInvalidHost}
	}

	if err := run([]string{}); err == nil {
		t.Error("expected error from setupManager, got nil")
	}
}

func TestRunStartManagerError(t *testing.T) {
	origGetConfig := getConfig
	origStartManager := startManager
	origSetupReconciler := setupReconciler
	origSetupSignalHandler := setupSignalHandler
	defer func() {
		getConfig = origGetConfig
		startManager = origStartManager
		setupReconciler = origSetupReconciler
		setupSignalHandler = origSetupSignalHandler
	}()

	setupSignalHandler = func() context.Context {
		return context.Background()
	}
	setupReconciler = func(mgr ctrl.Manager, clientset *kubernetes.Clientset) error {
		return nil
	}
	getConfig = func() *rest.Config {
		return &rest.Config{Host: testValidHost}
	}
	startManager = func(mgr ctrl.Manager, ctx context.Context) error {
		return errors.New("simulated start error")
	}

	args := []string{
		testMetricsBindAddress,
		testProbeBindAddress,
	}
	if err := run(args); err == nil {
		t.Error("expected startManager error, got nil")
	}
}

func TestMainSuccess(t *testing.T) {
	origGetConfig := getConfig
	origStartManager := startManager
	origSetupReconciler := setupReconciler
	origSetupSignalHandler := setupSignalHandler
	origOsExit := osExit
	origArgs := os.Args
	defer func() {
		getConfig = origGetConfig
		startManager = origStartManager
		setupReconciler = origSetupReconciler
		setupSignalHandler = origSetupSignalHandler
		osExit = origOsExit
		os.Args = origArgs
	}()

	setupSignalHandler = func() context.Context {
		return context.Background()
	}
	setupReconciler = func(mgr ctrl.Manager, clientset *kubernetes.Clientset) error {
		return nil
	}
	getConfig = func() *rest.Config {
		return &rest.Config{Host: testValidHost}
	}
	startManager = func(mgr ctrl.Manager, ctx context.Context) error {
		return nil
	}
	exited := false
	osExit = func(code int) {
		exited = true
	}

	os.Args = []string{"traffic-operator", testMetricsBindAddress, testProbeBindAddress}
	main()

	if exited {
		t.Error("expected main not to call os.Exit on success")
	}
}

func TestMainError(t *testing.T) {
	origGetConfig := getConfig
	origSetupSignalHandler := setupSignalHandler
	origOsExit := osExit
	origArgs := os.Args
	defer func() {
		getConfig = origGetConfig
		setupSignalHandler = origSetupSignalHandler
		osExit = origOsExit
		os.Args = origArgs
	}()

	setupSignalHandler = func() context.Context {
		return context.Background()
	}
	getConfig = func() *rest.Config {
		return &rest.Config{Host: testInvalidHost}
	}
	var exitCode int
	exited := false
	osExit = func(code int) {
		exited = true
		exitCode = code
	}

	os.Args = []string{"traffic-operator"}
	main()

	if !exited {
		t.Error("expected main to call os.Exit on error")
	}
	if exitCode != 1 {
		t.Errorf("expected exit code 1, got %d", exitCode)
	}
}

func TestSignalHandlerDefault(t *testing.T) {
	ctx := setupSignalHandler()
	if ctx == nil {
		t.Error("expected non-nil signal handler context")
	}
	select {
	case <-ctx.Done():
		t.Error("context should not be cancelled initially")
	default:
	}
}
