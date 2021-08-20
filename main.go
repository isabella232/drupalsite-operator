/*
Copyright 2021 CERN.

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
	"flag"
	"os"

	// Import all Kubernetes client auth plugins (e.g. Azure, GCP, OIDC, etc.)
	// to ensure that exec-entrypoint and run can make use of them.
	_ "k8s.io/client-go/plugin/pkg/client/auth"

	dbodv1a1 "gitlab.cern.ch/drupal/paas/dbod-operator/api/v1alpha1"
	drupalwebservicesv1alpha1 "gitlab.cern.ch/drupal/paas/drupalsite-operator/apis/drupal.webservices/v1alpha1"
	controllers "gitlab.cern.ch/drupal/paas/drupalsite-operator/controllers/drupal.webservices"
	authz "gitlab.cern.ch/paas-tools/operators/authz-operator/api/v1alpha1"
	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"

	configv2 "gitlab.cern.ch/drupal/paas/drupalsite-operator/apis/config/v2"
	// +kubebuilder:scaffold:imports
	buildv1 "github.com/openshift/api/build/v1"
	imagev1 "github.com/openshift/api/image/v1"
	routev1 "github.com/openshift/api/route/v1"
	velerov1 "github.com/vmware-tanzu/velero/pkg/apis/velero/v1"
	appsv1 "k8s.io/api/apps/v1"
)

var (
	scheme   = runtime.NewScheme()
	setupLog = ctrl.Log.WithName("setup")
)

func init() {
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))

	utilruntime.Must(drupalwebservicesv1alpha1.AddToScheme(scheme))
	utilruntime.Must(authz.AddToScheme(scheme))
	utilruntime.Must(dbodv1a1.AddToScheme(scheme))
	utilruntime.Must(configv2.AddToScheme(scheme))
	utilruntime.Must(configv2.AddToScheme(scheme))
	// +kubebuilder:scaffold:scheme
	utilruntime.Must(appsv1.AddToScheme(scheme))
	utilruntime.Must(routev1.AddToScheme(scheme))
	utilruntime.Must(imagev1.AddToScheme(scheme))
	utilruntime.Must(buildv1.AddToScheme(scheme))
	utilruntime.Must(velerov1.AddToScheme(scheme))
}

func main() {
	var configFile string

	opts := zap.Options{
		Development: true,
	}
	opts.BindFlags(flag.CommandLine)
	ctrl.SetLogger(zap.New(zap.UseFlagOptions(&opts)))
	flag.Parse()

	var err error
	ctrlConfig := configv2.ProjectConfig{}
	options := ctrl.Options{
		Scheme:                  scheme,
		MetricsBindAddress:      ":8080",
		Port:                    9443,
		HealthProbeBindAddress:  ":8081",
		LeaderElection:          true,
		LeaderElectionID:        "78d40201.cern.ch",
		LeaderElectionNamespace: "openshift-cern-drupal",
	}
	// TODO: Set CONFIG_FILE env var through subscription
	os.Setenv("CONFIG_FILE", "./config/manager/controller_manager_config.yaml")
	configFile = os.Getenv("CONFIG_FILE")
	if configFile != "" {
		options, err = options.AndFrom(ctrl.ConfigFile().AtPath(configFile).OfKind(&ctrlConfig))
		if err != nil {
			setupLog.Error(err, "unable to load the config file")
			os.Exit(1)
		}
	} else {
		setupLog.Error(err, "CONFIG_FILE enviroment variable is not defined")
		os.Exit(1)
	}

	controllers.SiteBuilderImage = ctrlConfig.SiteBuilderImage
	controllers.NginxImage = ctrlConfig.NginxImage
	controllers.PhpFpmExporterImage = ctrlConfig.PhpFpmExporterImage
	controllers.WebDAVImage = ctrlConfig.WebDAVImage
	controllers.SMTPHost = ctrlConfig.SMTPHost
	controllers.VeleroNamespace = ctrlConfig.VeleroNamespace

	controllers.BuildResources, err = controllers.ResourceRequestLimit("250Mi", "250m", "300Mi", "1000m")
	if err != nil {
		setupLog.Error(err, "Invalid configuration: can't parse build resources")
		os.Exit(1)
	}

	mgr, err := ctrl.NewManager(ctrl.GetConfigOrDie(), options)
	if err != nil {
		setupLog.Error(err, "unable to start manager")
		os.Exit(1)
	}

	if err = (&controllers.DrupalSiteReconciler{
		Client: mgr.GetClient(),
		Log:    ctrl.Log.WithName("controllers").WithName("DrupalSite"),
		Scheme: mgr.GetScheme(),
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "DrupalSite")
		os.Exit(1)
	}
	// +kubebuilder:scaffold:builder

	if err := mgr.AddHealthzCheck("health", healthz.Ping); err != nil {
		setupLog.Error(err, "unable to set up health check")
		os.Exit(1)
	}
	if err := mgr.AddReadyzCheck("check", healthz.Ping); err != nil {
		setupLog.Error(err, "unable to set up ready check")
		os.Exit(1)
	}

	setupLog.Info("starting manager")
	if err := mgr.Start(ctrl.SetupSignalHandler()); err != nil {
		setupLog.Error(err, "problem running manager")
		os.Exit(1)
	}
}
