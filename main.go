/*
Copyright 2021.

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
	"archive/tar"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	// Import all Kubernetes client auth plugins (e.g. Azure, GCP, OIDC, etc.)
	// to ensure that exec-entrypoint and run can make use of them.
	_ "k8s.io/client-go/plugin/pkg/client/auth"

	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"

	drupalwebservicesv1alpha1 "gitlab.cern.ch/drupal/paas/drupalsite-operator/api/v1alpha1"
	"gitlab.cern.ch/drupal/paas/drupalsite-operator/controllers"

	// +kubebuilder:scaffold:imports
	buildv1 "github.com/openshift/api/build/v1"
	imagev1 "github.com/openshift/api/image/v1"
	routev1 "github.com/openshift/api/route/v1"
	appsv1 "k8s.io/api/apps/v1"
)

var (
	scheme   = runtime.NewScheme()
	setupLog = ctrl.Log.WithName("setup")
)

func init() {
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))

	utilruntime.Must(drupalwebservicesv1alpha1.AddToScheme(scheme))
	// +kubebuilder:scaffold:scheme
	utilruntime.Must(appsv1.AddToScheme(scheme))
	utilruntime.Must(routev1.AddToScheme(scheme))
	utilruntime.Must(imagev1.AddToScheme(scheme))
	utilruntime.Must(buildv1.AddToScheme(scheme))
}

func main() {
	var metricsAddr string
	var enableLeaderElection bool
	var probeAddr string
	flag.StringVar(&metricsAddr, "metrics-bind-address", ":8080", "The address the metric endpoint binds to.")
	flag.StringVar(&probeAddr, "health-probe-bind-address", ":8081", "The address the probe endpoint binds to.")
	flag.BoolVar(&enableLeaderElection, "leader-elect", false,
		"Enable leader election for controller manager. "+
			"Enabling this will ensure there is only one active controller manager.")
	opts := zap.Options{
		Development: true,
	}
	opts.BindFlags(flag.CommandLine)
	ctrl.SetLogger(zap.New(zap.UseFlagOptions(&opts)))
	initEnv()
	flag.Parse()

	mgr, err := ctrl.NewManager(ctrl.GetConfigOrDie(), ctrl.Options{
		Scheme:                 scheme,
		MetricsBindAddress:     metricsAddr,
		Port:                   9443,
		HealthProbeBindAddress: probeAddr,
		LeaderElection:         enableLeaderElection,
		LeaderElectionID:       "78d40201.cern.ch",
	})
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

func initEnv() {
	// <git_url>@<git_ref>
	// Drupal runtime repo containing the dockerfiles and other config data
	// to build the runtime images. After '@' a git ref can be specified (default: "master").
	// Example: "https://gitlab.cern.ch/drupal/paas/drupal-runtime.git@s2i"
	runtimeRepo := strings.Split(getenvOrDie("RUNTIME_REPO"), "@")
	controllers.ImageRecipesRepo = runtimeRepo[0]
	if len(runtimeRepo) > 1 {
		controllers.ImageRecipesRepoRef = runtimeRepo[1]
	} else {
		controllers.ImageRecipesRepoRef = "master"
	}
	controllers.ClusterName = getenvOrDie("CLUSTER_NAME")
	flag.Var(&controllers.RouterShards, "assignable-router-shard", "List of available router shards")

	ImageRecipesRepoDownload := strings.Trim(runtimeRepo[0], ".git") + "/repository/archive.tar?path=configuration&ref=" + controllers.ImageRecipesRepoRef
	directoryName := downloadFile(ImageRecipesRepoDownload, "/tmp/repo.tar")
	configPath := "/tmp/drupal-runtime/"
	createConfigDirectory(configPath)
	untar("/tmp/repo.tar", "/tmp/drupal-runtime")
	renameConfigDirectory(directoryName, "/tmp/drupal-runtime")
}

// getenvOrDie checks for the given variable in the environment, if not exists
func getenvOrDie(name string) string {
	e := os.Getenv(name)
	if e == "" {
		setupLog.Info(name + ": missing environment variable (unset or empty string)")
		os.Exit(1)
	}
	return e
}

// downloadFile downloads from the given URL and writes it to the given filename
func downloadFile(url string, fileName string) string {
	resp, err := http.Get(url)
	if err != nil || resp.StatusCode != 200 {
		setupLog.Error(err, fmt.Sprintf("fetching image recipes repo failed"))
		os.Exit(1)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		setupLog.Info("Received non 200 response code")
		os.Exit(1)
	}
	directoryName := strings.TrimRight(strings.Trim(strings.Split(resp.Header["Content-Disposition"][0], "=")[1], "\""), ".tar")
	//Create a empty file
	file, err := os.Create(fileName)
	if err != nil {
		setupLog.Error(err, fmt.Sprintf("Failed to create file"))
		os.Exit(1)
	}
	defer file.Close()

	//Write the bytes to the file
	_, err = io.Copy(file, resp.Body)
	if err != nil {
		setupLog.Error(err, fmt.Sprintf("Failed to write to a file"))
		os.Exit(1)
	}
	setupLog.Info(fmt.Sprintf("Downloaded the file %s", fileName))
	return directoryName
}

// untar decompress the given tar file to the given target directory
func untar(tarball, target string) {
	reader, err := os.Open(tarball)
	if err != nil {
		setupLog.Error(err, fmt.Sprintf("Failed to open the tar file"))
		os.Exit(1)
	}
	defer reader.Close()
	tarReader := tar.NewReader(reader)

	for {
		header, err := tarReader.Next()
		if err == io.EOF {
			break
		} else if err != nil {
			setupLog.Error(err, fmt.Sprintf("Failed to read the file"))
			os.Exit(1)
		}
		path := filepath.Join(target, header.Name)
		info := header.FileInfo()
		if info.IsDir() {
			if err = os.MkdirAll(path, info.Mode()); err != nil {
				setupLog.Error(err, fmt.Sprintf("Failed to create a directory"))
				os.Exit(1)
			}
			continue
		}

		file, err := os.OpenFile(path, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, info.Mode())
		if err != nil {
			setupLog.Error(err, fmt.Sprintf("Failed to create file"))
			os.Exit(1)
		}
		defer file.Close()
		_, err = io.Copy(file, tarReader)
		if err != nil {
			setupLog.Error(err, fmt.Sprintf("Failed to write to a file"))
			os.Exit(1)
		}
	}
}

// renameConfigDirectory reorganises the configuration files into the appropriate directories after decompressing them
func renameConfigDirectory(directoryName string, path string) {
	moveFile("/tmp/drupal-runtime/"+directoryName+"/configuration/qos-critical", "/tmp/qos-critical")
	moveFile("/tmp/drupal-runtime/"+directoryName+"/configuration/qos-eco", "/tmp/qos-eco")
	moveFile("/tmp/drupal-runtime/"+directoryName+"/configuration/qos-standard", "/tmp/qos-standard")
	removeFileIfExists("/tmp/drupal-runtime")
}

// createConfigDirectory creates the required directories to download the configurations
func createConfigDirectory(configPath string) {
	removeFileIfExists("/tmp/qos-critical")
	removeFileIfExists("/tmp/qos-eco")
	removeFileIfExists("/tmp/qos-standard")
	removeFileIfExists("/tmp/drupal-runtime")
	err := os.MkdirAll("/tmp/drupal-runtime", 0755)
	setupLog.Info(fmt.Sprintf("Creating Directory %s", "/tmp/drupal-runtime"))
	if err != nil {
		setupLog.Error(err, fmt.Sprintf("Failed to create the directory /tmp/drupal-runtime"))
		os.Exit(1)
	}
}

// removeFileIfExists checks if the given file/ directory exists. If it does, it removes it
func removeFileIfExists(path string) {
	_, err := os.Stat(path)
	if !os.IsNotExist(err) {
		err = os.RemoveAll(path)
		if err != nil {
			setupLog.Error(err, fmt.Sprintf("Failed to delete the directory %s", path))
			os.Exit(1)
		}
	}
}

// moveFile checks if the given file/ directory exists. If it does, it removes it
func moveFile(from string, to string) {
	err := os.Rename(from, to)
	if err != nil {
		setupLog.Error(err, fmt.Sprintf("Failed to move the directory from %s to %s", from, to))
		os.Exit(1)
	}
}
