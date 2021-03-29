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
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/asaskevich/govalidator"
	"github.com/go-logr/logr"
	buildv1 "github.com/openshift/api/build/v1"
	imagev1 "github.com/openshift/api/image/v1"
	routev1 "github.com/openshift/api/route/v1"
	dbodv1a1 "gitlab.cern.ch/drupal/paas/dbod-operator/go/api/v1alpha1"
	webservicesv1a1 "gitlab.cern.ch/drupal/paas/drupalsite-operator/api/v1alpha1"
	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"

	k8sapierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	controllerruntime "sigs.k8s.io/controller-runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

const (
	// finalizerStr string that is going to added to every DrupalSite created
	finalizerStr          = "controller.drupalsite.webservices.cern.ch"
	productionEnvironment = "production"
	adminAnnotation       = "drupal.cern.ch/admin-custom-edit"
	// REQUEUE_INTERVAL is the standard waiting period when the controller decides to requeue itself after a transient condition has occurred
	REQUEUE_INTERVAL = time.Duration(20 * time.Second)
)

var (
	// ImageRecipesRepo refers to the drupal runtime repo which contains the dockerfiles and other config data to build the images
	// Example: "https://gitlab.cern.ch/drupal/paas/drupal-runtime.git"
	ImageRecipesRepo string
	// ImageRecipesRepoRef refers to the branch (git ref) of the drupal runtime repo which contains the dockerfiles
	// and other config data to build the images
	// Example: "s2i"
	ImageRecipesRepoRef string
	// DefaultDomain is used in the Route's Host field
	DefaultDomain string
)

type strFlagList []string

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
// +kubebuilder:rbac:groups=build.openshift.io,resources=buildconfigs,verbs=*
// +kubebuilder:rbac:groups=build.openshift.io,resources=builds,verbs=get;list;watch
// +kubebuilder:rbac:groups=image.openshift.io,resources=imagestreams,verbs=*
// +kubebuilder:rbac:groups=route.openshift.io,resources=routes,verbs=*
// +kubebuilder:rbac:groups=core,resources=persistentvolumeclaims;services,verbs=*
// +kubebuilder:rbac:groups=batch,resources=jobs,verbs=*
// +kubebuilder:rbac:groups=core,resources=pods,verbs=get;list;watch
// +kubebuilder:rbac:groups=dbod.cern,resources=dbodregistrations,verbs=*
// +kubebuilder:rbac:groups=dbod.cern,resources=dbodclasses,verbs=get;list;watch;

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
		if k8sapierrors.IsNotFound(err) {
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

	handleTransientErr := func(transientErr reconcileError, logstrFmt string, status string) (reconcile.Result, error) {
		if status == "Ready" {
			setConditionStatus(drupalSite, "Ready", false, transientErr, false)
		} else if status == "UpdateNeeded" {
			setConditionStatus(drupalSite, "UpdateNeeded", false, transientErr, false)
		}
		r.updateCRStatusOrFailReconcile(ctx, log, drupalSite)
		if transientErr.Temporary() {
			log.Error(transientErr, fmt.Sprintf(logstrFmt, transientErr.Unwrap()))
			return reconcile.Result{Requeue: true}, nil
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
		return r.updateCRStatusOrFailReconcile(ctx, log, drupalSite)
	}

	// 2. Check all conditions. No actions performed  here.

	update := false
	// Check if the drupal site is ready to serve requests
	if siteReady := r.isDrupalSiteReady(ctx, drupalSite); siteReady {
		update = setReady(drupalSite) || update
	} else {
		update = setNotReady(drupalSite, nil) || update
	}

	// Check if the site is installed and mark the condition
	if installed := r.isInstallJobCompleted(ctx, drupalSite); installed {
		update = setInstalled(drupalSite) || update
	} else {
		update = setNotInstalled(drupalSite) || update
	}

	// TODO simplify logic by splitting into 2
	// Condition `UpdateNeeded` <- either image not matching `drupalVersion` or `drush updb` needed
	updateNeeded, typeUpdate, reconcileErr := r.updateNeeded(ctx, drupalSite)
	if reconcileErr != nil || updateNeeded {
		if reconcileErr != nil {
			// Do not return in this case, but continue the reconciliation
			update = setConditionStatus(drupalSite, "UpdateNeeded", true, reconcileErr, true) || update
		} else {
			update = setConditionStatus(drupalSite, "UpdateNeeded", true, nil, false) || update
		}
	} else {
		update = setConditionStatus(drupalSite, "UpdateNeeded", false, nil, false) || update
	}

	// Update status with all the conditions that were checked
	if update {
		return r.updateCRStatusOrFailReconcile(ctx, log, drupalSite)
	}

	// 3. After all conditions have been checked, perform actions relying on the Conditions for information.

	// Ensure all resources (server deployment is excluded here during updates)
	if transientErrs := r.ensureResources(drupalSite, log); transientErrs != nil {
		transientErr := concat(transientErrs)
		setNotReady(drupalSite, transientErr)
		return handleTransientErr(transientErr, "%v while ensuring the resources", "Ready")
	}

	// Set "UpdateNeeded" and perform code update
	// 1. set the Status.previousDrupalVersion
	// 2. wait for Builds to be ready
	// 3. ensure updated deployment
	// 4. set condition "CodeUpdatingFailed" to true if there is an unrecoverable error & rollback

	if drupalSite.ConditionTrue("UpdateNeeded") && typeUpdate == "CodeUpdate" {
		// wait for Builds to succeed
		if err := r.checkBuildstatusForUpdate(ctx, drupalSite); err != nil {
			if err.Temporary() {
				// try to reconcile after a few seconds like half a minute or so
				return handleTransientErr(err, "%v while building images for the new Drupal version", "UpdateNeeded")
			} else {
				err.Wrap("%v: Failed to update version " + drupalSite.Spec.DrupalVersion)
				setConditionStatus(drupalSite, "CodeUpdatingFailed", true, err, false)
				return r.updateCRStatusOrFailReconcile(ctx, log, drupalSite)
			}
		}

		if err := r.ensureUpdatedDeployment(ctx, drupalSite); err != nil {
			if err.Temporary() {
				return handleTransientErr(err, "%v while deploying the updated Drupal images of version", "UpdateNeeded")
			} else {
				err.Wrap("%v: Failed to update version " + drupalSite.Spec.DrupalVersion)
				//rollback here
				if err := r.rollBackCodeUpdate(ctx, drupalSite, newApplicationError(nil, ErrDeploymentUpdateFailed)); err != nil {
					return ctrl.Result{}, nil
				}
			}
		}

		setConditionStatus(drupalSite, "CodeUpdatingFailed", false, nil, false)
		setConditionStatus(drupalSite, "UpdateNeeded", false, nil, false)
		return r.updateCRStatusOrFailReconcile(ctx, log, drupalSite)
	}

	// Put site in maintenance mode
	// Take db Backup on PVC
	// Run drush updatedb
	// Restore backup if backup fails
	// Remove site from maintenance mode

	if drupalSite.ConditionTrue("UpdateNeeded") && typeUpdate == "DBUpdate" {
		// Enable maintenance mode
		if _, err := r.execToServerPodErrOnStderr(ctx, drupalSite, "php-fpm", nil, enableSiteMaintenanceModeCommandForDrupalSite()...); err != nil {
			setConditionStatus(drupalSite, "DBUpdatingFailed", true, newApplicationError(err, ErrPodExec), false)
			return r.updateCRStatusOrFailReconcile(ctx, log, drupalSite)
		}

		// Take backup
		backupFileName := "db_backup_" + drupalSite.Spec.DrupalVersion + time.Now().Local().Format("02-01-2006")
		if _, err := r.execToServerPodErrOnStderr(ctx, drupalSite, "php-fpm", nil, takeBackup(backupFileName)...); err != nil {
			setConditionStatus(drupalSite, "DBUpdatingFailed", true, newApplicationError(err, ErrPodExec), false)
			return r.updateCRStatusOrFailReconcile(ctx, log, drupalSite)
		}

		// Run updb
		sout, err := r.execToServerPodErrOnStderr(ctx, drupalSite, "php-fpm", nil, runUpDBCommand()...)
		if err != nil {
			setConditionStatus(drupalSite, "DBUpdatingFailed", true, newApplicationError(err, ErrPodExec), false)
			return r.updateCRStatusOrFailReconcile(ctx, log, drupalSite)
		}
		// Check if update succeeded
		if sout != "" {
			setConditionStatus(drupalSite, "UpdateNeeded", false, nil, false)
			return r.updateCRStatusOrFailReconcile(ctx, log, drupalSite)
		}

		// If error roll back
		err = r.rollBackDBUpdate(ctx, drupalSite, newApplicationError(nil, ErrDBUpdateFailed), backupFileName)
		if err != nil {
			setConditionStatus(drupalSite, "DBUpdatingFailed", true, newApplicationError(err, ErrDBUpdateFailed), false)
			return r.updateCRStatusOrFailReconcile(ctx, log, drupalSite)
		}

		// disable site maintenance mode
		if _, err := r.execToServerPodErrOnStderr(ctx, drupalSite, "php-fpm", nil, disableSiteMaintenanceModeCommandForDrupalSite()...); err != nil {
			setConditionStatus(drupalSite, "DBUpdatingFailed", true, newApplicationError(err, ErrPodExec), false)
			return r.updateCRStatusOrFailReconcile(ctx, log, drupalSite)
		}
	}

	// 4. Check DBOD has been provisioned and reconcile if needed
	if dbodReady := r.isDBODProvisioned(ctx, drupalSite); !dbodReady {
		if update := setNotReady(drupalSite, newApplicationError(nil, ErrDBOD)); update {
			r.updateCRStatusOrFailReconcile(ctx, log, drupalSite)
		}
		return reconcile.Result{Requeue: true, RequeueAfter: REQUEUE_INTERVAL}, nil
	}

	if drupalSite.Status.PreviousDrupalVersion != drupalSite.Spec.DrupalVersion {
		drupalSite.Status.PreviousDrupalVersion = drupalSite.Spec.DrupalVersion
		return r.updateCRStatusOrFailReconcile(ctx, log, drupalSite)
	}
	return ctrl.Result{}, nil
}

// business logic

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
	DefaultDomain = getenvOrDie("DEFAULT_DOMAIN", log)

	ImageRecipesRepoDownload := strings.Trim(runtimeRepo[0], ".git") + "/repository/archive.tar?path=configuration&ref=" + ImageRecipesRepoRef
	directoryName := downloadFile(ImageRecipesRepoDownload, "/tmp/repo.tar", log)
	configPath := "/tmp/drupal-runtime/"
	createConfigDirectory(configPath, log)
	untar("/tmp/repo.tar", "/tmp/drupal-runtime", log)
	renameConfigDirectory(directoryName, "/tmp/drupal-runtime", log)
}

// isInstallJobCompleted checks if the drush job is successfully completed
func (r *DrupalSiteReconciler) isInstallJobCompleted(ctx context.Context, d *webservicesv1a1.DrupalSite) bool {
	found := &batchv1.Job{}
	jobObject := &batchv1.Job{ObjectMeta: metav1.ObjectMeta{Name: "site-install-" + d.Name, Namespace: d.Namespace}}
	err := r.Get(ctx, types.NamespacedName{Name: jobObject.Name, Namespace: jobObject.Namespace}, found)
	if err == nil {
		if found.Status.Succeeded != 0 {
			return true
		}
	}
	return false
}

// isDrupalSiteReady checks if the drupal site is to ready to serve requests by checking the status of Nginx & PHP pods
func (r *DrupalSiteReconciler) isDrupalSiteReady(ctx context.Context, d *webservicesv1a1.DrupalSite) bool {
	deployment := &appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Name: d.Name, Namespace: d.Namespace}}
	err1 := r.Get(ctx, types.NamespacedName{Name: deployment.Name, Namespace: deployment.Namespace}, deployment)
	if err1 == nil {
		// Change the implementation here
		if deployment.Status.ReadyReplicas != 0 {
			return true
		}
	}
	return false
}

// isDBODProvisioned checks if the DBOD has been provisioned by checking the status of DBOD custom resource
func (r *DrupalSiteReconciler) isDBODProvisioned(ctx context.Context, d *webservicesv1a1.DrupalSite) bool {
	return len(r.getDBODProvisionedSecret(ctx, d)) > 0
}

// getDBODProvisionedSecret fetches the secret name of the DBOD provisioned secret by checking the status of DBOD custom resource
func (r *DrupalSiteReconciler) getDBODProvisionedSecret(ctx context.Context, d *webservicesv1a1.DrupalSite) string {
	// TODO maybe change this during update
	// TODO instead of checking checking status to fetch the DbCredentialsSecret, use the 'registrationLabels` to filter and get the name of the secret
	dbodCR := &dbodv1a1.DBODRegistration{ObjectMeta: metav1.ObjectMeta{Name: d.Name, Namespace: d.Namespace}}
	err1 := r.Get(ctx, types.NamespacedName{Name: dbodCR.Name, Namespace: dbodCR.Namespace}, dbodCR)
	if err1 == nil {
		return dbodCR.Status.DbCredentialsSecret
	}
	return ""
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

//validateSpec validates the spec against the DrupalSiteSpec definition
func validateSpec(drpSpec webservicesv1a1.DrupalSiteSpec) reconcileError {
	_, err := govalidator.ValidateStruct(drpSpec)
	if err != nil {
		return newApplicationError(err, ErrInvalidSpec)
	}
	return nil
}

// ensureSpecFinalizer ensures that the spec is valid, adding extra info if necessary, and that the finalizer is there,
// then returns if it needs to be updated.
func ensureSpecFinalizer(drp *webservicesv1a1.DrupalSite, log logr.Logger) (update bool) {
	if !controllerutil.ContainsFinalizer(drp, finalizerStr) {
		log.Info("Adding finalizer")
		controllerutil.AddFinalizer(drp, finalizerStr)
		update = true
	}
	if drp.Spec.SiteURL == "" {
		if drp.Spec.Environment.Name == productionEnvironment {
			drp.Spec.SiteURL = drp.Namespace + "." + DefaultDomain

		}
		drp.Spec.SiteURL = drp.Spec.Environment.Name + "-" + drp.Namespace + "." + DefaultDomain
	}
	return
}

// getRunningdeployment fetches the running drupal deployment
func (r *DrupalSiteReconciler) getRunningdeployment(ctx context.Context, d *webservicesv1a1.DrupalSite) (*appsv1.Deployment, error) {
	deployment := &appsv1.Deployment{}
	err := r.Get(ctx, types.NamespacedName{Name: d.Name, Namespace: d.Namespace}, deployment)
	return deployment, err
}

// checkGivenImageIsInNginxImageStreamTagItems fetches the running nginx imagestream tag items for a given tag and checks if the given image is part of it or not
func (r *DrupalSiteReconciler) isGivenImageInNginxImageStreamTagItems(ctx context.Context, d *webservicesv1a1.DrupalSite, givenImage string) (bool, error) {
	imageStream := &imagev1.ImageStream{}
	err := r.Get(ctx, types.NamespacedName{Name: "nginx-" + d.Name, Namespace: d.Namespace}, imageStream)
	if err != nil || len(imageStream.Status.Tags) > 0 {
		tagList := imageStream.Status.Tags
		for t := range tagList {
			if tagList[t].Tag == d.Spec.DrupalVersion {
				for i := range tagList[t].Items {
					if tagList[t].Items[i].DockerImageReference == givenImage {
						return true, nil
					}
				}
			}
		}
		return false, nil
	}
	return false, ErrClientK8s
}

// didRollOutSucceed checks if the deployment has rolled out the new pods successfully and the new pods are running
func (r *DrupalSiteReconciler) didRollOutSucceed(ctx context.Context, d *webservicesv1a1.DrupalSite) (bool, error) {
	pod, err := r.getRunningPod(ctx, d)
	if err != nil {
		return false, err
	}
	if pod.Status.Phase != corev1.PodRunning || pod.Annotations["drupalVersion"] != d.Spec.DrupalVersion {
		return false, errors.New("Pod did not roll out successfully")
	}
	return true, nil
}

// UpdateNeeded checks if a code or DB update is required based on the image tag and drupalVersion in the CR spec and the drush status
func (r *DrupalSiteReconciler) updateNeeded(ctx context.Context, d *webservicesv1a1.DrupalSite) (bool, string, reconcileError) {
	deployment, err := r.getRunningdeployment(ctx, d)
	if err != nil {
		return false, "", newApplicationError(err, ErrClientK8s)
	}
	// If the deployment has a different image tag, then update needed
	// NOTE: a more robust check could be done with Drush inside the pod
	if len(deployment.Spec.Template.Spec.Containers) > 0 {
		image := deployment.Spec.Template.Spec.Containers[0].Image
		if len(image) < 2 {
			return false, "", newApplicationError(errors.New("server deployment image doesn't have a version tag"), ErrInvalidSpec)
		}
		imageInTag := d.Spec.DrupalVersion == strings.Split(image, ":")[2]
		// if err != nil {
		// 	return false, "", newApplicationError(errors.New("cannot check if the given image is part of the nginx imagestream tag items  "), ErrClientK8s)
		// }
		// Check if image is different, check if current site is ready and installed
		if !imageInTag && d.ConditionTrue("Ready") && d.ConditionTrue("Installed") {
			return true, "CodeUpdate", nil
		}
	}
	// If drush updb-status needs update, then a database update needed
	sout, err := r.execToServerPodErrOnStderr(ctx, d, "php-fpm", nil, checkUpdbStatus()...)
	if err != nil {
		return false, "", newApplicationError(err, ErrPodExec)
	}
	if sout != "" {
		return true, "DBUpdate", nil
	}
	return false, "", nil
}

// GetDeploymentCondition returns the condition with the provided type.
func GetDeploymentCondition(status appsv1.DeploymentStatus, condType appsv1.DeploymentConditionType) *appsv1.DeploymentCondition {
	for i := range status.Conditions {
		c := status.Conditions[i]
		if c.Type == condType {
			return &c
		}
	}
	return nil
}

func (r *DrupalSiteReconciler) checkBuildstatusForUpdate(ctx context.Context, d *webservicesv1a1.DrupalSite) reconcileError {
	// Check status of the PHP buildconfig
	// TODO: check if the build is in error if the s2i build isn't ready yet (maybe it self-heals)
	status, err := r.getBuildStatus(ctx, "php", d)
	switch {
	case err != nil:
		return newApplicationError(err, ErrClientK8s)
	case status == buildv1.BuildPhaseFailed || status == buildv1.BuildPhaseError:
		return r.rollBackCodeUpdate(ctx, d, newApplicationError(nil, ErrBuildFailed))
	case status != buildv1.BuildPhaseComplete:
		return newApplicationError(err, ErrTemporary)
	}
	// Progress only when build is successfull

	// Check status of the Nginx buildconfig
	status, err = r.getBuildStatus(ctx, "nginx", d)
	switch {
	case err != nil:
		return newApplicationError(err, ErrClientK8s)
	case status == "Failed" || status == "Error":
		return r.rollBackCodeUpdate(ctx, d, newApplicationError(nil, ErrBuildFailed))
	case status != buildv1.BuildPhaseComplete:
		return newApplicationError(err, ErrTemporary)
	}
	return nil
}

// ensureUpdatedDeployment runs the logic to do the base update for a new Drupal version
// If it returns a reconcileError, if it's a permanent error it will set the condition reason and block retries.
func (r *DrupalSiteReconciler) ensureUpdatedDeployment(ctx context.Context, d *webservicesv1a1.DrupalSite) reconcileError {

	// Update deployment with the new version
	if dbodSecret := r.getDBODProvisionedSecret(ctx, d); len(dbodSecret) != 0 {
		deploy := &appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Name: d.Name, Namespace: d.Namespace}}
		_, err := controllerruntime.CreateOrUpdate(ctx, r.Client, deploy, func() error {
			return deploymentForDrupalSite(deploy, dbodSecret, d, d.Spec.DrupalVersion)
		})
		if err != nil {
			return newApplicationError(err, ErrClientK8s)
		}
	}
	// Check if deployment has rolled out
	deployment, err := r.getRunningdeployment(ctx, d)
	if err != nil {
		return newApplicationError(err, ErrClientK8s)
	}

	if GetDeploymentCondition(deployment.Status, appsv1.DeploymentReplicaFailure) != nil {
		if GetDeploymentCondition(deployment.Status, appsv1.DeploymentReplicaFailure).Status == corev1.ConditionTrue {
			return r.rollBackCodeUpdate(ctx, d, newApplicationError(nil, ErrBuildFailed))
		}
	}

	rollout, err := r.didRollOutSucceed(ctx, d)
	if rollout != true || err != nil {
		return newApplicationError(err, ErrTemporary)
	}

	return nil
}

// rollBackCodeUpdate rolls back the code update process to the previous version when it is called.
// It restores the deployment's image and unsets condition "UpdateNeeded"
// If successful, it returns a permanent error to block any update retries.
func (r *DrupalSiteReconciler) rollBackCodeUpdate(ctx context.Context, d *webservicesv1a1.DrupalSite, err reconcileError) reconcileError {
	// Restore the server deployment
	if dbodSecret := r.getDBODProvisionedSecret(ctx, d); len(dbodSecret) != 0 {
		deploy := &appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Name: d.Name, Namespace: d.Namespace}}
		_, err := controllerruntime.CreateOrUpdate(ctx, r.Client, deploy, func() error {
			return deploymentForDrupalSite(deploy, dbodSecret, d, d.Status.PreviousDrupalVersion)
		})
		if err != nil {
			return newApplicationError(err, ErrClientK8s)
		}
	}

	d.Spec.DrupalVersion = d.Status.PreviousDrupalVersion
	// Make error permanent
	// if err.Temporary() {
	// 	err = newApplicationError(err, ErrPermanent)
	// }
	setConditionStatus(d, "CodeUpdatingFailed", true, err, false)
	setConditionStatus(d, "UpdateNeeded", false, nil, false)
	return nil
}

// rollBackDBUpdate rolls back the DB update process to the previous version of the database from the backup.
// It restores the deployment's image and unsets condition "UpdateNeeded"
// If successful, it returns a permanent error to block any update retries.
func (r *DrupalSiteReconciler) rollBackDBUpdate(ctx context.Context, d *webservicesv1a1.DrupalSite, err reconcileError, backupFileName string) reconcileError {
	// Restore the database backup
	if _, err := r.execToServerPodErrOnStderr(ctx, d, "php-fpm", nil, restoreBackup(backupFileName)...); err != nil {
		return newApplicationError(err, ErrPodExec)
	}
	setConditionStatus(d, "DBUpdatingFailed", true, err, false)
	setConditionStatus(d, "UpdateNeeded", false, nil, false)
	return nil
}

// func (r *DrupalSiteReconciler) runDBUpdate(ctx context.Context, d *webservicesv1a1.DrupalSite) reconcileError {
// 	sout, err := r.execToServerPodErrOnStderr(ctx, d, "php-fpm", nil, runUpDBCommand()...)
// 	if err != nil {
// 		return newApplicationError(err, ErrPodExec)
// 	}
// 	if sout != "" {
// 		return nil
// 	}
// 	return
// }

// CODE BELOW WILL GO soon -------------

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
