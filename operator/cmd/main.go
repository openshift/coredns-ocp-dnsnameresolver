/*
Copyright 2024.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package main

import (
	"crypto/tls"
	"flag"
	"os"

	// Import all Kubernetes client auth plugins (e.g. Azure, GCP, OIDC, etc.)
	// to ensure that exec-entrypoint and run can make use of them.
	ocpnetworkv1alpha1 "github.com/openshift/api/network/v1alpha1"

	"github.com/openshift/coredns-ocp-dnsnameresolver/operator/controller/dnsnameresolver"
	dnsnameresolvercrd "github.com/openshift/coredns-ocp-dnsnameresolver/operator/controller/dnsnameresolver-crd"

	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	_ "k8s.io/client-go/plugin/pkg/client/auth"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"
	"sigs.k8s.io/controller-runtime/pkg/webhook"
	//+kubebuilder:scaffold:imports
)

var (
	scheme   = runtime.NewScheme()
	setupLog = ctrl.Log.WithName("setup")
)

func init() {
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))
	utilruntime.Must(apiextensionsv1.AddToScheme(scheme))
	utilruntime.Must(ocpnetworkv1alpha1.Install(scheme))

	//+kubebuilder:scaffold:scheme
}

func main() {
	var (
		metricsAddr              string
		enableLeaderElection     bool
		probeAddr                string
		secureMetrics            bool
		enableHTTP2              bool
		coreDNSNamespace         string
		coreDNSServieName        string
		dnsNameResolverNamespace string
	)
	flag.StringVar(&metricsAddr, "metrics-bind-address", ":8080", "The address the metric endpoint binds to.")
	flag.StringVar(&probeAddr, "health-probe-bind-address", ":8081", "The address the probe endpoint binds to.")
	flag.BoolVar(&enableLeaderElection, "leader-elect", false,
		"Enable leader election for controller manager. "+
			"Enabling this will ensure there is only one active controller manager.")
	flag.BoolVar(&secureMetrics, "metrics-secure", false,
		"If set the metrics endpoint is served securely")
	flag.BoolVar(&enableHTTP2, "enable-http2", false,
		"If set, HTTP/2 will be enabled for the metrics and webhook servers")
	flag.StringVar(&coreDNSNamespace, "coredns-namespace", "kube-system", "The namespace of the CoreDNS resources.")
	flag.StringVar(&coreDNSServieName, "coredns-service-name", "kube-dns", "The name of the CoreDNS service.")
	flag.StringVar(&dnsNameResolverNamespace, "dns-name-resolver-namespace", "ovn-kubernetes",
		"The namespace to watch for the DNSNameResolver objects.")
	opts := zap.Options{
		Development: true,
	}
	opts.BindFlags(flag.CommandLine)
	flag.Parse()

	ctrl.SetLogger(zap.New(zap.UseFlagOptions(&opts)))

	// if the enable-http2 flag is false (the default), http/2 should be disabled
	// due to its vulnerabilities. More specifically, disabling http/2 will
	// prevent from being vulnerable to the HTTP/2 Stream Cancelation and
	// Rapid Reset CVEs. For more information see:
	// - https://github.com/advisories/GHSA-qppj-fm5r-hxr3
	// - https://github.com/advisories/GHSA-4374-p667-p6c8
	disableHTTP2 := func(c *tls.Config) {
		setupLog.Info("disabling http/2")
		c.NextProtos = []string{"http/1.1"}
	}

	tlsOpts := []func(*tls.Config){}
	if !enableHTTP2 {
		tlsOpts = append(tlsOpts, disableHTTP2)
	}

	webhookServer := webhook.NewServer(webhook.Options{
		TLSOpts: tlsOpts,
	})

	mgr, err := ctrl.NewManager(ctrl.GetConfigOrDie(), ctrl.Options{
		Scheme: scheme,
		Metrics: metricsserver.Options{
			BindAddress:   metricsAddr,
			SecureServing: secureMetrics,
			TLSOpts:       tlsOpts,
		},
		WebhookServer:          webhookServer,
		HealthProbeBindAddress: probeAddr,
		LeaderElection:         enableLeaderElection,
		LeaderElectionID:       "3a3a07d4.openshift.io",
	})
	if err != nil {
		setupLog.Error(err, "unable to start manager")
		os.Exit(1)
	}

	// Set up the DNSNameResolver controller. This controller is unmanaged by
	// the manager. The reason why the controller is unmanaged is so that it
	// doesn't get automatically started when the operator starts; we only
	// want the controller to start if we need it. The dnsnameresolvercrd
	// controller starts it and the caches after it creates the
	// DNSNameResolver CRD.
	dnsNameResolverController, dnsNameResolverControllerCaches, err :=
		dnsnameresolver.NewUnmanaged(mgr, dnsnameresolver.Config{
			OperandNamespace:         coreDNSNamespace,
			ServiceName:              coreDNSServieName,
			DNSNameResolverNamespace: dnsNameResolverNamespace,
		})
	if err != nil {
		setupLog.Error(err, "failed to create dnsnameresolver controller")
		os.Exit(1)
	}

	// Set up the dnsnameresolvercrd controller.
	if _, err := dnsnameresolvercrd.New(mgr, dnsnameresolvercrd.Config{
		DependentCaches: dnsNameResolverControllerCaches,
		DependentControllers: []controller.Controller{
			dnsNameResolverController,
		},
	}); err != nil {
		setupLog.Error(err, "failed to create dnsnameresolvercrd controller")
	}

	//+kubebuilder:scaffold:builder

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
