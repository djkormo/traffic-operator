package main

import (
	"context"
	"flag"
	"os"

	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/client-go/kubernetes"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"

	networkv1alpha1 "github.com/djkormo/traffic-operator/api/v1alpha1"
	"github.com/djkormo/traffic-operator/internal/controller"
	"github.com/djkormo/traffic-operator/version"
)

var (
	scheme             = runtime.NewScheme()
	setupLog           = ctrl.Log.WithName("setup")
	getConfig          = ctrl.GetConfigOrDie
	newClientset       = kubernetes.NewForConfig
	setupReconciler    = defaultSetupReconciler
	setupHealthChecks  = defaultSetupHealthChecks
	setupSignalHandler = ctrl.SetupSignalHandler
	startManager       = defaultStartManager
	osExit             = os.Exit
)

func init() {
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))
	utilruntime.Must(networkv1alpha1.AddToScheme(scheme))
}

type Options struct {
	MetricsAddr          string
	ProbeAddr            string
	EnableLeaderElection bool
	ZapOpts              zap.Options
}

func parseFlags(flagSet *flag.FlagSet, args []string) (*Options, error) {
	opts := &Options{
		ZapOpts: zap.Options{Development: true},
	}

	flagSet.StringVar(&opts.MetricsAddr, "metrics-bind-address", ":8080", "The address the metrics endpoint binds to.")
	flagSet.StringVar(&opts.ProbeAddr, "health-probe-bind-address", ":8081", "The address the probe endpoint binds to.")
	flagSet.BoolVar(&opts.EnableLeaderElection, "leader-elect", false, "Enable leader election for controller manager.")

	opts.ZapOpts.BindFlags(flagSet)

	if err := flagSet.Parse(args); err != nil {
		return nil, err
	}

	return opts, nil
}

func defaultSetupReconciler(mgr ctrl.Manager, clientset *kubernetes.Clientset) error {
	if err := (&controller.TrafficCheckReconciler{
		Client:    mgr.GetClient(),
		Scheme:    mgr.GetScheme(),
		Clientset: clientset,
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "TrafficCheck")
		return err
	}
	return nil
}

func defaultSetupHealthChecks(mgr ctrl.Manager) error {
	if err := mgr.AddHealthzCheck("healthz", healthz.Ping); err != nil {
		setupLog.Error(err, "unable to set up health check")
		return err
	}
	if err := mgr.AddReadyzCheck("readyz", healthz.Ping); err != nil {
		setupLog.Error(err, "unable to set up ready check")
		return err
	}
	return nil
}

func defaultStartManager(mgr ctrl.Manager, ctx context.Context) error {
	return mgr.Start(ctx)
}

func setupManager(cfg *rest.Config, opts *Options) (ctrl.Manager, error) {
	mgr, err := ctrl.NewManager(cfg, ctrl.Options{
		Scheme:                 scheme,
		Metrics:                metricsserver.Options{BindAddress: opts.MetricsAddr},
		HealthProbeBindAddress: opts.ProbeAddr,
		LeaderElection:         opts.EnableLeaderElection,
		LeaderElectionID:       "traffic-operator.example.com",
	})
	if err != nil {
		setupLog.Error(err, "unable to start manager")
		return nil, err
	}

	clientset, err := newClientset(mgr.GetConfig())
	if err != nil {
		setupLog.Error(err, "unable to create kubernetes clientset")
		return nil, err
	}

	if err := setupReconciler(mgr, clientset); err != nil {
		return nil, err
	}

	if err := setupHealthChecks(mgr); err != nil {
		return nil, err
	}

	return mgr, nil
}

func run(args []string) error {
	fs := flag.NewFlagSet("traffic-operator", flag.ContinueOnError)
	opts, err := parseFlags(fs, args)
	if err != nil {
		if err == flag.ErrHelp {
			return nil
		}
		return err
	}

	ctrl.SetLogger(zap.New(zap.UseFlagOptions(&opts.ZapOpts)))

	cfg := getConfig()
	mgr, err := setupManager(cfg, opts)
	if err != nil {
		return err
	}

	setupLog.Info("starting manager",
		"Application", version.Application,
		"BuildTime", version.BuildTime,
		"Release", version.Release,
		"Commit", version.Commit,
	)
	if err := startManager(mgr, setupSignalHandler()); err != nil {
		setupLog.Error(err, "problem running manager")
		return err
	}
	return nil
}

func main() {
	if err := run(os.Args[1:]); err != nil {
		setupLog.Error(err, "problem running manager")
		osExit(1)
	}
}
