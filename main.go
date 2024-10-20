package main

import (
	"flag"
	"fmt"
	"os"

	sync "github.com/gravitational/sync-controller/controller"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/cluster"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
)

var (
	scheme   = runtime.NewScheme()
	setupLog = ctrl.Log.WithName("setup")
)

func init() {
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))
}

type stringArray []string

// String is an implementation of the flag.Value interface
func (i *stringArray) String() string {
	return fmt.Sprintf("%v", *i)
}

// Set is an implementation of the flag.Value interface
func (i *stringArray) Set(value string) error {
	*i = append(*i, value)
	return nil
}

func main() {
	var (
		probeAddr            string
		enableLeaderElection bool
		remoteKubeconfig     string
		group, version, kind string
		remoteResourceSuffix string
		localNamespaceSuffix string
		namespacePrefix      string
		localSecretNames     stringArray
	)

	flag.StringVar(&probeAddr, "health-probe-bind-address", ":8081", "The address the probe endpoint binds to.")
	flag.BoolVar(&enableLeaderElection, "leader-elect", false,
		"Enable leader election for controller manager. "+
			"Enabling this will ensure there is only one active controller manager.")
	flag.StringVar(&remoteKubeconfig, "remote-kubeconfig", "", "absolute path to the kubeconfig file")
	flag.StringVar(&group, "group", "", "group of the resource to sync")
	flag.StringVar(&version, "version", "", "version of the resource to sync")
	flag.StringVar(&kind, "kind", "", "kind of the resource to sync")
	flag.StringVar(&remoteResourceSuffix, "remote-resource-suffix", "", "suffix to append to all remote resources")
	flag.StringVar(&localNamespaceSuffix, "local-namespace-suffix", "", "suffix to append to all local namespaces")
	flag.StringVar(&namespacePrefix, "namespace-prefix", "", "prefix to require for all namespaces (others are ignored)")
	flag.Var(&localSecretNames, "local-secret-name", "secrets to sync from the local cluster to the remote cluster")
	opts := zap.Options{
		Development: true,
	}
	opts.BindFlags(flag.CommandLine)
	flag.Parse()

	ctrl.SetLogger(zap.New(zap.UseFlagOptions(&opts)))

	if remoteKubeconfig == "" {
		setupLog.Error(fmt.Errorf("no remote kubeconfig provided"), "unable to get remote kubeconfig")
		os.Exit(1)
	}

	remoteConfig, err := clientcmd.BuildConfigFromFlags("", remoteKubeconfig)
	if err != nil {
		setupLog.Error(err, "failed to create remote config")
		os.Exit(1)
	}

	remoteCluster, err := cluster.New(remoteConfig, func(options *cluster.Options) {
		options.Scheme = scheme
	})
	if err != nil {
		setupLog.Error(err, "unable to create remote cluster")
		os.Exit(1)
	}
	localConfig, err := rest.InClusterConfig()
	if err != nil {
		setupLog.Error(err, "failed to get in-cluster config as local config")
		os.Exit(1)
	}
	mgr, err := ctrl.NewManager(localConfig, ctrl.Options{
		Scheme:                 scheme,
		HealthProbeBindAddress: probeAddr,
		LeaderElection:         enableLeaderElection,
		LeaderElectionID:       fmt.Sprintf("%s.%s.%s", kind, version, group),
	})
	if err != nil {
		setupLog.Error(err, "unable to start manager")
		os.Exit(1)
	}
	syncReconciler := &sync.Reconciler{
		Client:       mgr.GetClient(),
		RemoteClient: remoteCluster.GetClient(),
		RemoteCache:  remoteCluster.GetCache(),
		Scheme:       mgr.GetScheme(),
		GroupVersionKind: schema.GroupVersionKind{
			Group:   group,
			Version: version,
			Kind:    kind,
		},
		RemoteResourceSuffix:   remoteResourceSuffix,
		LocalNamespaceSuffix:   localNamespaceSuffix,
		NamespacePrefix:        namespacePrefix,
		LocalSecretNames:       localSecretNames,
		LocalPropagationPolicy: client.PropagationPolicy(metav1.DeletePropagationForeground),
		ConcurrentReconciles:   1,
	}

	if err := syncReconciler.SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to setup sync reconciler")
		os.Exit(1)
	}

	if err := mgr.AddHealthzCheck("healthz", healthz.Ping); err != nil {
		setupLog.Error(err, "unable to set up health check")
		os.Exit(1)
	}
	if err := mgr.AddReadyzCheck("readyz", healthz.Ping); err != nil {
		setupLog.Error(err, "unable to set up ready check")
		os.Exit(1)
	}

	setupLog.Info("starting manager")
	if err := mgr.Start(ctrl.SetupSignalHandler()); err != nil {
		setupLog.Error(err, "problem running manager")
		os.Exit(1)
	}
}
