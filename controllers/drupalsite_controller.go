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

package controllers

import (
	"archive/tar"
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/go-logr/logr"
	buildv1 "github.com/openshift/api/build/v1"
	imagev1 "github.com/openshift/api/image/v1"
	routev1 "github.com/openshift/api/route/v1"
	"github.com/operator-framework/operator-lib/status"
	dbodv1a1 "gitlab.cern.ch/drupal/paas/dbod-operator/go/api/v1alpha1"
	webservicesv1a1 "gitlab.cern.ch/drupal/paas/drupalsite-operator/api/v1alpha1"
	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

// DrupalSiteReconciler reconciles a DrupalSite object
type DrupalSiteReconciler struct {
	client.Client
	Log    logr.Logger
	Scheme *runtime.Scheme
}

// +kubebuilder:rbac:groups=drupal.webservices.cern.ch,resources=drupalsites,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=drupal.webservices.cern.ch,resources=drupalsites/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=drupal.webservices.cern.ch,resources=drupalsites/finalizers,verbs=update
// +kubebuilder:rbac:groups=app,resources=deployments,verbs=*
// +kubebuilder:rbac:groups=build.openshift.io,resources=buildconfig,verbs=*
// +kubebuilder:rbac:groups=image.openshift.io,resources=imagestream,verbs=*
// +kubebuilder:rbac:groups=route.openshift.io,resources=routes,verbs=*
// +kubebuilder:rbac:groups=core,resources=persistentvolumeclaims;services,verbs=*
// +kubebuilder:rbac:groups=batch,resources=jobs,verbs=*
// +kubebuilder:rbac:groups=core,resources=pods,verbs=get;list;
// +kubebuilder:rbac:groups=dbod.cern,resources=dbodregistrations,verbs=*
// +kubebuilder:rbac:groups=dbod.cern,resources=dbodclasses,verbs=get,list,watch

func (r *DrupalSiteReconciler) initEnv() {
	log := r.Log
	log.Info("Initializing environment")
	// <git_url>@<git_ref>
	// Drupal runtime repo containing the dockerfiles and other config data
	// to build the runtime images. After '@' a git ref can be specified (default: "master").
	// Example: "https://gitlab.cern.ch/drupal/paas/drupal-runtime.git@s2i"
	runtimeRepo := strings.Split(getenvOrDie("RUNTIME_REPO", log), "@")
	ImageRecipesRepo = runtimeRepo[0]
	if len(runtimeRepo) > 1 {
		ImageRecipesRepoRef = runtimeRepo[1]
	} else {
		ImageRecipesRepoRef = "master"
	}
	ClusterName = getenvOrDie("CLUSTER_NAME", log)

	ImageRecipesRepoDownload := strings.Trim(runtimeRepo[0], ".git") + "/repository/archive.tar?path=configuration&ref=" + ImageRecipesRepoRef
	directoryName := downloadFile(ImageRecipesRepoDownload, "/tmp/repo.tar", log)
	configPath := "/tmp/drupal-runtime/"
	createConfigDirectory(configPath, log)
	untar("/tmp/repo.tar", "/tmp/drupal-runtime", log)
	renameConfigDirectory(directoryName, "/tmp/drupal-runtime", log)
}

// SetupWithManager adds a manager which watches the resources
func (r *DrupalSiteReconciler) SetupWithManager(mgr ctrl.Manager) error {
	r.initEnv()
	return ctrl.NewControllerManagedBy(mgr).
		For(&webservicesv1a1.DrupalSite{}).
		Owns(&appsv1.Deployment{}).
		Owns(&buildv1.BuildConfig{}).
		Owns(&imagev1.ImageStream{}).
		Owns(&routev1.Route{}).
		Owns(&corev1.PersistentVolumeClaim{}).
		Owns(&corev1.Service{}).
		Owns(&batchv1.Job{}).
		Owns(&dbodv1a1.DBODRegistration{}).
		Complete(r)
}

func (r *DrupalSiteReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	// _ = context.Background()
	log := r.Log.WithValues("Request.Namespace", req.NamespacedName, "Request.Name", req.Name)

	log.Info("Reconciling request")

	// Fetch the DrupalSite instance
	drupalSite := &webservicesv1a1.DrupalSite{}
	err := r.Get(ctx, req.NamespacedName, drupalSite)
	if err != nil {
		if errors.IsNotFound(err) {
			// Request object not found, could have been deleted after reconcile request.
			// Owned objects are automatically garbage collected. For additional cleanup logic use finalizers.
			// Return and don't requeue
			log.Info("DrupalSite resource not found. Ignoring since object must be deleted")
			return ctrl.Result{}, nil
		}
		// Error reading the object - requeue the request.
		log.Error(err, "Failed to get DrupalSite")
		return ctrl.Result{}, err
	}

	//Handle deletion
	if drupalSite.GetDeletionTimestamp() != nil {
		if controllerutil.ContainsFinalizer(drupalSite, finalizerStr) {
			return r.cleanupDrupalSite(ctx, log, drupalSite)
		}
		return ctrl.Result{}, nil
	}

	handleTransientErr := func(transientErr reconcileError, logstrFmt string) (reconcile.Result, error) {
		setNotReady(drupalSite, transientErr)
		r.updateCRStatusorFailReconcile(ctx, log, drupalSite)
		if transientErr.Temporary() {
			log.Error(transientErr, fmt.Sprintf(logstrFmt, transientErr.Unwrap()))
			return reconcile.Result{}, transientErr
		}
		log.Error(transientErr, "Permanent error marked as transient! Permanent errors should not bubble up to the reconcile loop.")
		return reconcile.Result{}, nil
	}

	// Init. Check if finalizer is set. If not, set it, validate and update CR status
	if update := ensureSpecFinalizer(drupalSite, log); update {
		log.Info("Initializing DrupalSite Spec")
		return r.updateCRorFailReconcile(ctx, log, drupalSite)
	}
	if err := validateSpec(drupalSite.Spec); err != nil {
		log.Error(err, fmt.Sprintf("%v failed to validate DrupalSite spec", err.Unwrap()))
		setErrorCondition(drupalSite, err)
		return r.updateCRStatusorFailReconcile(ctx, log, drupalSite)
	}

	// Ensure installed - Installed status
	// Create route

	// Ensure all primary resources
	if transientErrs := r.ensureResources(drupalSite, log); transientErrs != nil {
		transientErr := concat(transientErrs)
		setNotReady(drupalSite, transientErr)
		return handleTransientErr(transientErr, "%v while creating the resources")
	}

	// Check if DBOD has been provisioned
	if dbodReady := r.isDBODProvisioned(ctx, drupalSite); !dbodReady {
		return reconcile.Result{Requeue: true}, nil
	}

	// Check if the drupal site is ready to serve requests
	if siteReady := r.isDrupalSiteReady(ctx, drupalSite); siteReady {
		if update := setReady(drupalSite); update {
			return r.updateCRStatusorFailReconcile(ctx, log, drupalSite)
		}
	}

	// Check if the site is installed and mark the condition
	if installed := r.isInstallJobCompleted(ctx, drupalSite); installed {
		if update := setInstalled(drupalSite); update {
			return r.updateCRStatusorFailReconcile(ctx, log, drupalSite)
		}
	} else {
		if update := setNotInstalled(drupalSite); update {
			return r.updateCRStatusorFailReconcile(ctx, log, drupalSite)
		}
	}

	// If the installed status and ready status is true, create the route
	if drupalSite.ConditionTrue("Installed") && drupalSite.ConditionTrue("Ready") {
		if transientErr := r.ensureIngressResources(drupalSite, log); transientErr != nil {
			return handleTransientErr(transientErr, "%v while creating route")
		}
		return r.updateCRorFailReconcile(ctx, log, drupalSite)
	}

	return ctrl.Result{}, nil
}

// cleanupDrupalSite checks and removes if a finalizer exists on the resource
func (r *DrupalSiteReconciler) cleanupDrupalSite(ctx context.Context, log logr.Logger, drp *webservicesv1a1.DrupalSite) (ctrl.Result, error) {
	// finalizer: dependentResources
	// 1. check if such resources exist
	//   - delete them
	//   - reconcile
	// 1. if not, delete the finalizer key manually and let Kubernetes delete the resource cleanly
	// TODO
	log.Info("Deleting DrupalSite")
	controllerutil.RemoveFinalizer(drp, finalizerStr)
	return r.updateCRorFailReconcile(ctx, log, drp)
}

func setReady(drp *webservicesv1a1.DrupalSite) (update bool) {
	return drp.Status.Conditions.SetCondition(status.Condition{
		Type:   "Ready",
		Status: "True",
	})
}
func setNotReady(drp *webservicesv1a1.DrupalSite, transientErr reconcileError) (update bool) {
	return drp.Status.Conditions.SetCondition(status.Condition{
		Type:    "Ready",
		Status:  "False",
		Reason:  status.ConditionReason(transientErr.Unwrap().Error()),
		Message: transientErr.Error(),
	})
}
func setInstalled(drp *webservicesv1a1.DrupalSite) (update bool) {
	return drp.Status.Conditions.SetCondition(status.Condition{
		Type:   "Installed",
		Status: "True",
	})
}
func setNotInstalled(drp *webservicesv1a1.DrupalSite) (update bool) {
	return drp.Status.Conditions.SetCondition(status.Condition{
		Type:   "Installed",
		Status: "False",
	})
}
func setErrorCondition(drp *webservicesv1a1.DrupalSite, err reconcileError) (update bool) {
	return drp.Status.Conditions.SetCondition(status.Condition{
		Type:    "Error",
		Status:  "True",
		Reason:  status.ConditionReason(err.Unwrap().Error()),
		Message: err.Error(),
	})
}

// getenvOrDie checks for the given variable in the environment, if not exists
func getenvOrDie(name string, log logr.Logger) string {
	e := os.Getenv(name)
	if e == "" {
		log.Info(name + ": missing environment variable (unset or empty string)")
		os.Exit(1)
	}
	return e
}

// downloadFile downloads from the given URL and writes it to the given filename
func downloadFile(url string, fileName string, log logr.Logger) string {
	resp, err := http.Get(url)
	if err != nil || resp.StatusCode != 200 {
		log.Error(err, fmt.Sprintf("fetching image recipes repo failed"))
		os.Exit(1)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		log.Info("Received non 200 response code")
		os.Exit(1)
	}
	directoryName := strings.TrimRight(strings.Trim(strings.Split(resp.Header["Content-Disposition"][0], "=")[1], "\""), ".tar")
	//Create a empty file
	file, err := os.Create(fileName)
	if err != nil {
		log.Error(err, fmt.Sprintf("Failed to create file"))
		os.Exit(1)
	}
	defer file.Close()

	//Write the bytes to the file
	_, err = io.Copy(file, resp.Body)
	if err != nil {
		log.Error(err, fmt.Sprintf("Failed to write to a file"))
		os.Exit(1)
	}
	log.Info(fmt.Sprintf("Downloaded the file %s", fileName))
	return directoryName
}

// untar decompress the given tar file to the given target directory
func untar(tarball, target string, log logr.Logger) {
	reader, err := os.Open(tarball)
	if err != nil {
		log.Error(err, fmt.Sprintf("Failed to open the tar file"))
		os.Exit(1)
	}
	defer reader.Close()
	tarReader := tar.NewReader(reader)

	for {
		header, err := tarReader.Next()
		if err == io.EOF {
			break
		} else if err != nil {
			log.Error(err, fmt.Sprintf("Failed to read the file"))
			os.Exit(1)
		}
		path := filepath.Join(target, header.Name)
		info := header.FileInfo()
		if info.IsDir() {
			if err = os.MkdirAll(path, info.Mode()); err != nil {
				log.Error(err, fmt.Sprintf("Failed to create a directory"))
				os.Exit(1)
			}
			continue
		}

		file, err := os.OpenFile(path, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, info.Mode())
		if err != nil {
			log.Error(err, fmt.Sprintf("Failed to create file"))
			os.Exit(1)
		}
		defer file.Close()
		_, err = io.Copy(file, tarReader)
		if err != nil {
			log.Error(err, fmt.Sprintf("Failed to write to a file"))
			os.Exit(1)
		}
	}
}

// renameConfigDirectory reorganises the configuration files into the appropriate directories after decompressing them
func renameConfigDirectory(directoryName string, path string, log logr.Logger) {
	moveFile("/tmp/drupal-runtime/"+directoryName+"/configuration/qos-critical", "/tmp/qos-critical", log)
	moveFile("/tmp/drupal-runtime/"+directoryName+"/configuration/qos-eco", "/tmp/qos-eco", log)
	moveFile("/tmp/drupal-runtime/"+directoryName+"/configuration/qos-standard", "/tmp/qos-standard", log)
	removeFileIfExists("/tmp/drupal-runtime", log)
}

// createConfigDirectory creates the required directories to download the configurations
func createConfigDirectory(configPath string, log logr.Logger) {
	removeFileIfExists("/tmp/qos-critical", log)
	removeFileIfExists("/tmp/qos-eco", log)
	removeFileIfExists("/tmp/qos-standard", log)
	removeFileIfExists("/tmp/drupal-runtime", log)
	err := os.MkdirAll("/tmp/drupal-runtime", 0755)
	log.Info(fmt.Sprintf("Creating Directory %s", "/tmp/drupal-runtime"))
	if err != nil {
		log.Error(err, fmt.Sprintf("Failed to create the directory /tmp/drupal-runtime"))
		os.Exit(1)
	}
}

// removeFileIfExists checks if the given file/ directory exists. If it does, it removes it
func removeFileIfExists(path string, log logr.Logger) {
	_, err := os.Stat(path)
	if !os.IsNotExist(err) {
		err = os.RemoveAll(path)
		if err != nil {
			log.Error(err, fmt.Sprintf("Failed to delete the directory %s", path))
			os.Exit(1)
		}
	}
}

// moveFile checks if the given file/ directory exists. If it does, it removes it
func moveFile(from string, to string, log logr.Logger) {
	err := os.Rename(from, to)
	if err != nil {
		log.Error(err, fmt.Sprintf("Failed to move the directory from %s to %s", from, to))
		os.Exit(1)
	}
}
