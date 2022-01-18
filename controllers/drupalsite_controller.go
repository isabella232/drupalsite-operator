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

package controllers

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/asaskevich/govalidator"
	"github.com/go-logr/logr"
	buildv1 "github.com/openshift/api/build/v1"
	imagev1 "github.com/openshift/api/image/v1"
	routev1 "github.com/openshift/api/route/v1"
	velerov1 "github.com/vmware-tanzu/velero/pkg/apis/velero/v1"
	dbodv1a1 "gitlab.cern.ch/drupal/paas/dbod-operator/api/v1alpha1"
	webservicesv1a1 "gitlab.cern.ch/drupal/paas/drupalsite-operator/api/v1alpha1"
	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"

	k8sapierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/controller-runtime/pkg/source"
)

const (
	// finalizerStr string that is going to added to every DrupalSite created
	finalizerStr    = "controller.drupalsite.webservices.cern.ch"
	adminAnnotation = "drupal.cern.ch/admin-custom-edit"
	oidcSecretName  = "oidc-client-secret"
)

var (
	// SiteBuilderImage refers to the sitebuilder image name
	SiteBuilderImage string
	// PhpFpmExporterImage refers to the php-fpm-exporter image name
	PhpFpmExporterImage string
	// WebDAVImage refers to the webdav image name
	WebDAVImage string
	// SMTPHost used by Drupal server pods to send emails
	SMTPHost string
	// VeleroNamespace refers to the namespace of the velero server to create backups
	VeleroNamespace string
	// DefaultD8ReleaseSpec refers to the releaseSpec for Drupal 8 to be defaulted incase it is empty
	DefaultD8ReleaseSpec string
	// DefaultD9ReleaseSpec refers to the releaseSpec for Drupal 8 to be defaulted incase it is empty
	DefaultD9ReleaseSpec string
	// ParallelThreadCount refers to the number of parallel reconciliations done by the Operator
	ParallelThreadCount int
	// EnableTopologySpread refers to enabling avaliability zone scheduling for critical site deployments
	EnableTopologySpread bool
	// ClusterName refers to the name of the cluster the operator is running on
	ClusterName string
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
// +kubebuilder:rbac:groups=drupal.webservices.cern.ch,resources=drupalsiteconfigoverrides,verbs=get;list;watch
// +kubebuilder:rbac:groups=app,resources=deployments,verbs=*
// +kubebuilder:rbac:groups=build.openshift.io,resources=buildconfigs,verbs=*
// +kubebuilder:rbac:groups=build.openshift.io,resources=builds,verbs=get;list;watch
// +kubebuilder:rbac:groups=image.openshift.io,resources=imagestreams,verbs=*
// +kubebuilder:rbac:groups=route.openshift.io,resources=routes,verbs=*
// +kubebuilder:rbac:groups=core,resources=persistentvolumeclaims;services,verbs=*
// +kubebuilder:rbac:groups=batch,resources=jobs,verbs=*
// +kubebuilder:rbac:groups=core,resources=pods,verbs=get;list;watch
// +kubebuilder:rbac:groups=dbod.cern.ch,resources=databases,verbs=*
// +kubebuilder:rbac:groups=dbod.cern.ch,resources=databaseclasses,verbs=get;list;watch;
// +kubebuilder:rbac:groups=webservices.cern.ch,resources=oidcreturnuris,verbs=*
// +kubebuilder:rbac:groups=core,resources=secrets,verbs=*;
// +kubebuilder:rbac:groups=velero.io,resources=backups,verbs=get;list;watch;
// +kubebuilder:rbac:groups=velero.io,resources=schedules,verbs=*;
// +kubebuilder:rbac:groups=rbac.authorization.k8s.io,resources=clusterrolebindings,verbs=get;list;watch;create;
// +kubebuilder:rbac:groups=core,resources=namespaces,verbs=get;list;watch

// SetupWithManager adds a manager which watches the resources
func (r *DrupalSiteReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&webservicesv1a1.DrupalSite{}).
		Owns(&appsv1.Deployment{}).
		Owns(&buildv1.BuildConfig{}).
		Owns(&imagev1.ImageStream{}).
		Owns(&routev1.Route{}).
		Owns(&corev1.PersistentVolumeClaim{}).
		Owns(&corev1.Service{}).
		Owns(&batchv1.Job{}).
		Owns(&dbodv1a1.Database{}).
		Owns(&corev1.ConfigMap{}).
		Owns(&corev1.Secret{}).
		Watches(&source.Kind{Type: &velerov1.Backup{}}, handler.EnqueueRequestsFromMapFunc(
			// Reconcile every DrupalSite in the project referred to by the Backup
			func(a client.Object) []reconcile.Request {
				log := r.Log.WithValues("Source", "Velero Backup event handler", "Namespace", a.GetNamespace())
				projectName, exists := a.GetLabels()["drupal.webservices.cern.ch/project"]
				if exists {
					return fetchDrupalSitesInNamespace(mgr, log, projectName)
				}
				return []reconcile.Request{}
			}),
		).
		Watches(&source.Kind{Type: &corev1.Namespace{}}, handler.EnqueueRequestsFromMapFunc(
			// Reconcile every DrupalSite in a given namespace
			func(a client.Object) []reconcile.Request {
				log := r.Log.WithValues("Source", "Namespace event handler", "Namespace", a.GetName())
				_, exists := a.GetLabels()["drupal.cern.ch/user-project"]
				if exists {
					return fetchDrupalSitesInNamespace(mgr, log, a.GetName())
				}
				return []reconcile.Request{}
			}),
		).
		Watches(&source.Kind{Type: &webservicesv1a1.DrupalSiteConfigOverride{}}, handler.EnqueueRequestsFromMapFunc(
			// Reconcile every DrupalSite in a given namespace
			func(a client.Object) []reconcile.Request {
				req := make([]reconcile.Request, 1)
				// The DrupalSite has the same name as the DrupalSiteConfigOverride
				req[0].Name = a.GetName()
				req[0].Namespace = a.GetNamespace()
				return req
			}),
		).
		Watches(&source.Kind{Type: &webservicesv1a1.DrupalProjectConfig{}}, handler.EnqueueRequestsFromMapFunc(
			// Reconcile every DrupalSite in a given namespace
			func(a client.Object) []reconcile.Request {
				log := r.Log.WithValues("Source", "Namespace event handler", "Namespace", a.GetNamespace())
				return fetchDrupalSitesInNamespace(mgr, log, a.GetNamespace())
			}),
		).
		WithOptions(controller.Options{
			MaxConcurrentReconciles: ParallelThreadCount,
		}).
		Complete(r)
}

// fetchDrupalSitesInNamespace feteches all the Drupalsites in a given namespace
func fetchDrupalSitesInNamespace(mgr ctrl.Manager, log logr.Logger, namespace string) []reconcile.Request {
	drupalSiteList := webservicesv1a1.DrupalSiteList{}
	options := client.ListOptions{
		Namespace: namespace,
	}
	err := mgr.GetClient().List(context.TODO(), &drupalSiteList, &options)
	if err != nil {
		log.Error(err, "Couldn't query drupalsites in the namespace")
		return []reconcile.Request{}
	}
	requests := make([]reconcile.Request, len(drupalSiteList.Items))
	for i, drupalSite := range drupalSiteList.Items {
		requests[i].Name = drupalSite.Name
		requests[i].Namespace = drupalSite.Namespace
	}
	return requests
}

func (r *DrupalSiteReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	// _ = context.Background()
	log := r.Log.WithValues("Request.Namespace", req.NamespacedName, "Request.Name", req.Name)
	log.V(1).Info("Reconciling request")
	var requeueFlag error

	// Fetch the DrupalSite instance
	drupalSite := &webservicesv1a1.DrupalSite{}
	err := r.Get(ctx, req.NamespacedName, drupalSite)
	if err != nil {
		if k8sapierrors.IsNotFound(err) {
			// Request object not found, could have been deleted after reconcile request.
			// Owned objects are automatically garbage collected. For additional cleanup logic use finalizers.
			// Return and don't requeue
			log.V(3).Info("DrupalSite resource not found. Ignoring since object must be deleted")
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
		}
		r.updateCRStatusOrFailReconcile(ctx, log, drupalSite)
		if transientErr.Temporary() {
			log.Error(transientErr, fmt.Sprintf(logstrFmt, transientErr.Unwrap()))
			// emitting error because the controller can count it in the error metrics,
			// which we can monitor to notice transient problems affecting the entire infrastructure
			return reconcile.Result{}, err
		}
		log.Error(transientErr, "Permanent error marked as transient! Permanent errors should not bubble up to the reconcile loop.")
		return reconcile.Result{}, nil
	}
	handleNonfatalErr := func(nonfatalErr reconcileError, logstrFmt string, status string) {
		if nonfatalErr.Temporary() {
			log.Error(nonfatalErr, fmt.Sprintf(logstrFmt, nonfatalErr.Unwrap()))
		} else {
			log.Error(nonfatalErr, "Permanent error marked as transient! Permanent errors should not bubble up to the reconcile loop.")
		}
		// emitting error because the controller can count it in the error metrics,
		// which we can monitor to notice transient problems affecting the entire infrastructure
		requeueFlag = nonfatalErr
	}

	// 1. Init: Check if finalizer is set. If not, set it, validate and update CR status

	if update, err := r.ensureSpecFinalizer(ctx, drupalSite, log); err != nil {
		log.Error(err, fmt.Sprintf("%v failed to ensure DrupalSite spec defaults", err.Unwrap()))
		setErrorCondition(drupalSite, err)
		return r.updateCRStatusOrFailReconcile(ctx, log, drupalSite)
	} else if update {
		log.V(3).Info("Initializing DrupalSite Spec")
		return r.updateCRorFailReconcile(ctx, log, drupalSite)
	}
	if err := validateSpec(drupalSite.Spec); err != nil {
		log.Error(err, fmt.Sprintf("%v failed to validate DrupalSite spec", err.Unwrap()))
		setErrorCondition(drupalSite, err)
		return r.updateCRStatusOrFailReconcile(ctx, log, drupalSite)
	}

	// Retrieve the DrupalProjectConfig Resource
	drupalProjectConfig, err := r.GetDrupalProjectConfig(ctx, drupalSite)
	if err != nil {
		log.Error(err, fmt.Sprintf("%v failed to retrieve DrupalProjectConfig", err))
		// Although we log an Error, we will allow the reconcile to continue, as the absence of the resource does not mean DrupalSite cannot be processed
		// Populate resource as nil
		drupalProjectConfig = nil
	}

	// 2. Check all conditions and update if needed
	update := false

	// Set Current version
	if drupalSite.Status.ReleaseID.Current != releaseID(drupalSite) {
		drupalSite.Status.ReleaseID.Current = releaseID(drupalSite)
		update = true || update
	}

	// Check if the drupal site is ready to serve requests
	// We need to check for isDBODProvisioned explicitly here. Because if we don't, the status is put as Ready here considering the pod is running, but later on
	// in the reconcile function, when DBOD provisioning is checked, the status is put as DBODError. There's a slight conflict here
	if r.isDrupalSiteReady(ctx, drupalSite) && r.isDBODProvisioned(ctx, drupalSite) {
		update = setReady(drupalSite) || update
	} else {
		update = setNotReady(drupalSite, nil) || update
	}

	// Check if the site is installed or cloned and mark the condition
	if !drupalSite.ConditionTrue("Initialized") {
		if r.isDrupalSiteInstalled(ctx, drupalSite) || r.isCloneJobCompleted(ctx, drupalSite) {
			update = setInitialized(drupalSite) || update
		} else {
			update = setNotInitialized(drupalSite) || update
		}
	}

	// In situations where there are no db updates, but 'DBUpdatesPending' is set without a 'DBUpdatesFailed' status, remove the 'DBUpdatesPending'
	if drupalSite.ConditionTrue("DBUpdatesPending") && !drupalSite.ConditionTrue("DBUpdatesFailed") {
		sout, err := r.execToServerPodErrOnStderr(ctx, drupalSite, "php-fpm", nil, checkUpdbStatus()...)
		if err != nil {
			return r.updateCRStatusOrFailReconcile(ctx, log, drupalSite)
		}
		if sout == "" {
			update = drupalSite.Status.Conditions.RemoveCondition("DBUpdatesPending") || update
		}
	}

	// After a failed update, to be able to restore the site back to the last running version, the status error fields have to be removed if they are set
	if drupalSite.Status.ReleaseID.Failsafe == releaseID(drupalSite) {
		if drupalSite.ConditionTrue("CodeUpdateFailed") {
			update = drupalSite.Status.Conditions.RemoveCondition("CodeUpdateFailed") || update
		}
		if drupalSite.ConditionTrue("DBUpdatesFailed") {
			update = drupalSite.Status.Conditions.RemoveCondition("DBUpdatesFailed") || update
		}
	}

	// Update status with all the conditions that were checked
	if update {
		return r.updateCRStatusOrFailReconcile(ctx, log, drupalSite)
	}

	// Condition `UpdateNeeded` <- either image not matching `releaseID` or `drush updb` needed
	updateNeeded, reconcileErr := r.updateNeeded(ctx, drupalSite)
	_, isUpdateAnnotationSet := drupalSite.Annotations["updateInProgress"]
	if !isUpdateAnnotationSet && !drupalSite.ConditionTrue("CodeUpdateFailed") && !drupalSite.ConditionTrue("DBUpdatesFailed") {
		switch {
		case reconcileErr != nil:
			handleNonfatalErr(reconcileErr, "%v while checking if an update is needed", "")
		case updateNeeded:
			if setUpdateInProgress(drupalSite) {
				return r.updateCRorFailReconcile(ctx, log, drupalSite)
			}
		}
	}
	// Check if DrupalProjectConfig has not a primary website + This DrupalSite instance is unique -> Become Primary Website
	updateNeeded, reconcileErr = r.proclaimPrimarySiteIfExists(ctx, drupalSite, drupalProjectConfig)
	switch {
	case err != nil:
		log.Error(err, fmt.Sprintf("%v failed to declare this DrupalSite as Primary", reconcileErr.Unwrap()))
		setErrorCondition(drupalSite, reconcileErr)
		return r.updateCRStatusOrFailReconcile(ctx, log, drupalSite)
	case updateNeeded:
		log.Info("Updating DrupalProjectConfig " + drupalProjectConfig.Namespace)
		r.updateDrupalProjectConfigCRorFailReconcile(ctx, log, drupalProjectConfig)
	}
	// Check if current instance is the Primary Drupalsite
	updateNeeded, reconcileErr = r.checkIfPrimaryDrupalsite(ctx, drupalSite, drupalProjectConfig)
	switch {
	case err != nil:
		log.Error(err, fmt.Sprintf("%v failed to validate if DrupalSite is Primary", reconcileErr.Unwrap()))
		setErrorCondition(drupalSite, reconcileErr)
		return r.updateCRStatusOrFailReconcile(ctx, log, drupalSite)
	case updateNeeded:
		return r.updateCRStatusOrFailReconcile(ctx, log, drupalSite)
	}

	// 3. After all conditions have been checked, perform actions relying on the Conditions for information.

	// Deployment replicas and resources
	deploymentConfig, requeue, updateStatus, reconcileErr := r.getDeploymentConfiguration(ctx, drupalSite)
	switch {
	case reconcileErr != nil:
		if reconcileErr.Temporary() {
			return handleTransientErr(reconcileErr, "Failed to calculate deployment configuration: %v", "")
		} else {
			return r.updateCRStatusOrFailReconcile(ctx, log, drupalSite)
		}
	case requeue:
		return reconcile.Result{Requeue: true}, nil
	case updateStatus:
		return r.updateCRStatusOrFailReconcile(ctx, log, drupalSite)
	}

	// Ensure all resources (server deployment is excluded here during updates)
	if transientErrs := r.ensureResources(drupalSite, deploymentConfig, log); transientErrs != nil {
		transientErr := concat(transientErrs)
		return handleTransientErr(transientErr, "%v while ensuring the resources", "Ready")
	}

	// Ensure that the server deployment has the configmap annotations
	requeue, transientErr := r.ensureDeploymentConfigmapHash(ctx, drupalSite, log)
	switch {
	case transientErr != nil:
		return handleTransientErr(transientErr, "%v while ensuring the resources", "Ready")
	case requeue:
		return reconcile.Result{Requeue: true}, nil
	}

	// Set "UpdateNeeded" and perform code update
	// 1. set the Status.ReleaseID.Failsafe
	// 2. ensure updated deployment
	// 3. set condition "CodeUpdateFailed" to true if there is an unrecoverable error & rollback

	if isUpdateAnnotationSet && !drupalSite.ConditionTrue("CodeUpdateFailed") && !drupalSite.ConditionTrue("DBUpdatesPending") {
		update, requeue, err, errorMessage := r.updateDrupalVersion(ctx, drupalSite, deploymentConfig)
		switch {
		case err != nil:
			if err.Temporary() {
				return handleTransientErr(err, errorMessage, "")
			} else {
				// NOTE: If error is permanent, there's nothing more we can do.
				log.Error(err, err.Unwrap().Error())
				return r.updateCRStatusOrFailReconcile(ctx, log, drupalSite)
			}
		case update:
			return r.updateCRStatusOrFailReconcile(ctx, log, drupalSite)
		case requeue:
			return ctrl.Result{Requeue: true}, nil
		}
	}

	// Take db Backup on PVC
	// Put site in maintenance mode
	// Run drush updatedb
	// Remove site from maintenance mode
	// Restore backup in case of a failure

	if isUpdateAnnotationSet && !drupalSite.ConditionTrue("DBUpdatesFailed") && !drupalSite.ConditionTrue("CodeUpdateFailed") {
		if update := r.updateDBSchema(ctx, drupalSite, log); update {
			return r.updateCRStatusOrFailReconcile(ctx, log, drupalSite)
		}
	}

	if unsetUpdateInProgress(drupalSite) {
		return r.updateCRorFailReconcile(ctx, log, drupalSite)
	}

	// 4. Check DBOD has been provisioned and reconcile if needed
	if dbodReady := r.isDBODProvisioned(ctx, drupalSite); !dbodReady {
		if update := setNotReady(drupalSite, newApplicationError(nil, ErrDBOD)); update {
			r.updateCRStatusOrFailReconcile(ctx, log, drupalSite)
		}
		return reconcile.Result{Requeue: true}, nil
	}

	// Update the Failsafe during the first instantiation and after a successful update
	if drupalSite.Status.ReleaseID.Current != drupalSite.Status.ReleaseID.Failsafe && !drupalSite.ConditionTrue("DBUpdatesFailed") && !drupalSite.ConditionTrue("CodeUpdateFailed") {
		drupalSite.Status.ReleaseID.Failsafe = releaseID(drupalSite)
		drupalSite.Status.ServingPodImage = sitebuilderImageRefToUse(drupalSite, releaseID(drupalSite)).Name
		return r.updateCRStatusOrFailReconcile(ctx, log, drupalSite)
	}

	backupList, err := r.checkNewBackups(ctx, drupalSite, log)
	switch {
	case err != nil:
		return ctrl.Result{}, err
	case len(backupList) != 0:
		if backupListUpdateNeeded(backupList, drupalSite.Status.AvailableBackups) {
			drupalSite.Status.AvailableBackups = updateBackupListStatus(backupList)
			return r.updateCRStatusOrFailReconcile(ctx, log, drupalSite)
		}
	}

	// If it's a site with extraConfig Spec, add the gitlab webhook trigger to the Status
	if len(drupalSite.Spec.ExtraConfigurationRepo) > 0 && len(drupalSite.Status.GitlabWebhookURL) == 0 {
		if err := r.addGitlabWebhookToStatus(ctx, drupalSite); err != nil {
			return handleTransientErr(err, "Failed to add GitlabWebhookURL to status: %v", "")
		}
		return r.updateCRStatusOrFailReconcile(ctx, log, drupalSite)
	}

	// Returning err with Reconcile functions causes a requeue by default following exponential backoff
	// Ref https://gitlab.cern.ch/paas-tools/operators/authz-operator/-/merge_requests/76#note_4501887
	return ctrl.Result{}, requeueFlag
}

// business logic

// isInstallJobCompleted checks if the drush job is successfully completed
func (r *DrupalSiteReconciler) isInstallJobCompleted(ctx context.Context, d *webservicesv1a1.DrupalSite) bool {
	found := &batchv1.Job{}
	jobObject := &batchv1.Job{ObjectMeta: metav1.ObjectMeta{Name: "ensure-site-install-" + d.Name, Namespace: d.Namespace}}
	err := r.Get(ctx, types.NamespacedName{Name: jobObject.Name, Namespace: jobObject.Namespace}, found)
	if err == nil {
		if found.Status.Succeeded != 0 {
			return true
		}
	}
	return false
}

// isCloneJobCompleted checks if the clone job is successfully completed
func (r *DrupalSiteReconciler) isCloneJobCompleted(ctx context.Context, d *webservicesv1a1.DrupalSite) bool {
	cloneJob := &batchv1.Job{}
	err := r.Get(ctx, types.NamespacedName{Name: "clone-" + d.Name, Namespace: d.Namespace}, cloneJob)
	if err != nil {
		return false
	}
	// business logic, ie check "Succeeded"
	return cloneJob.Status.Succeeded != 0
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

// isDrupalSiteInstalled checks if the drupal site is initialized by running drush status command in the PHP pod
func (r *DrupalSiteReconciler) isDrupalSiteInstalled(ctx context.Context, d *webservicesv1a1.DrupalSite) bool {
	if r.isDrupalSiteReady(ctx, d) {
		if _, err := r.execToServerPodErrOnStderr(ctx, d, "php-fpm", nil, checkIfSiteIsInstalled()...); err != nil {
			return false
		}
		return true
	}
	return false
}

// isDBODProvisioned checks if the DBOD has been provisioned by checking the status of DBOD custom resource
func (r *DrupalSiteReconciler) isDBODProvisioned(ctx context.Context, d *webservicesv1a1.DrupalSite) bool {
	database := &dbodv1a1.Database{}
	err := r.Get(ctx, types.NamespacedName{Name: d.Name, Namespace: d.Namespace}, database)
	if err != nil {
		return false
	}
	return len(database.Status.DbodInstance) > 0
}

// databaseSecretName fetches the secret name of the DBOD provisioned secret by checking the status of DBOD custom resource
func databaseSecretName(d *webservicesv1a1.DrupalSite) string {
	return "dbcredentials-" + d.Name
}

// cleanupDrupalSite checks and removes if a finalizer exists on the resource
func (r *DrupalSiteReconciler) cleanupDrupalSite(ctx context.Context, log logr.Logger, drp *webservicesv1a1.DrupalSite) (ctrl.Result, error) {
	log.V(1).Info("Deleting DrupalSite")
	controllerutil.RemoveFinalizer(drp, finalizerStr)
	if err := r.ensureNoBackupSchedule(ctx, drp, log); err != nil {
		return ctrl.Result{}, err
	}
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
func (r *DrupalSiteReconciler) ensureSpecFinalizer(ctx context.Context, drp *webservicesv1a1.DrupalSite, log logr.Logger) (update bool, err reconcileError) {
	if !controllerutil.ContainsFinalizer(drp, finalizerStr) {
		log.V(3).Info("Adding finalizer")
		controllerutil.AddFinalizer(drp, finalizerStr)
		update = true
	}
	if drp.Spec.Configuration.WebDAVPassword == "" {
		drp.Spec.Configuration.WebDAVPassword = generateRandomPassword()
		update = true
	}
	// Validate that CloneFrom is an existing DrupalSite
	if drp.Spec.Configuration.CloneFrom != "" {
		sourceSite := webservicesv1a1.DrupalSite{}
		err := r.Get(ctx, types.NamespacedName{Name: string(drp.Spec.Configuration.CloneFrom), Namespace: drp.Namespace}, &sourceSite)
		switch {
		case k8sapierrors.IsNotFound(err):
			return false, newApplicationError(fmt.Errorf("CloneFrom DrupalSite doesn't exist"), ErrInvalidSpec)
		case err != nil:
			return false, newApplicationError(err, ErrClientK8s)
		}
		// The destination disk size must be at least as large as the source
		if drp.Spec.Configuration.DiskSize < sourceSite.Spec.Configuration.DiskSize {
			drp.Spec.Configuration.DiskSize = sourceSite.Spec.Configuration.DiskSize
		}
	}
	// Initialize 'spec.version.releaseSpec' if empty
	if len(drp.Spec.Version.ReleaseSpec) == 0 {
		if strings.HasPrefix(drp.Spec.Version.Name, "v8") {
			drp.Spec.Version.ReleaseSpec = DefaultD8ReleaseSpec
		} else {
			drp.Spec.Version.ReleaseSpec = DefaultD9ReleaseSpec
		}
		update = true
	}
	return update, nil
}

// getRunningdeployment fetches the running drupal deployment
func (r *DrupalSiteReconciler) getRunningdeployment(ctx context.Context, d *webservicesv1a1.DrupalSite) (*appsv1.Deployment, error) {
	deployment := &appsv1.Deployment{}
	err := r.Get(ctx, types.NamespacedName{Name: d.Name, Namespace: d.Namespace}, deployment)
	return deployment, err
}

// didVersionRollOutSucceed checks if the deployment has rolled out the new pods successfully and the new pods are running
func (r *DrupalSiteReconciler) didVersionRollOutSucceed(ctx context.Context, d *webservicesv1a1.DrupalSite) (requeue bool, err reconcileError) {
	pod, err := r.getPodForVersion(ctx, d, releaseID(d))
	if err != nil && err.Temporary() {
		return false, newApplicationError(err, ErrClientK8s)
	}
	if pod.Status.Phase == corev1.PodFailed || pod.Status.Phase == corev1.PodUnknown {
		return false, newApplicationError(errors.New("pod did not roll out successfully"), ErrDeploymentUpdateFailed)
	}
	if pod.Status.Phase == corev1.PodPending {
		currentTime := time.Now()
		if currentTime.Sub(pod.GetCreationTimestamp().Time).Minutes() < 3 {
			return true, newApplicationError(errors.New("waiting for pod to start"), ErrPodNotRunning)
		}
		return false, newApplicationError(errors.New("pod failed to start after grace period"), ErrDeploymentUpdateFailed)
	}
	return false, nil
}

// UpdateNeeded checks if a code or DB update is required based on the image tag and releaseID in the CR spec and the drush status
func (r *DrupalSiteReconciler) updateNeeded(ctx context.Context, d *webservicesv1a1.DrupalSite) (bool, reconcileError) {
	// Check for an update, only when the site is initialized and ready to prevent checks during an installation/ upgrade
	if d.ConditionTrue("Ready") && d.ConditionTrue("Initialized") {
		deployment, err := r.getRunningdeployment(ctx, d)
		if err != nil {
			return false, newApplicationError(err, ErrClientK8s)
		}
		// Check if image is different, check if current site is ready and installed
		if deployment.Spec.Template.ObjectMeta.Annotations["releaseID"] != releaseID(d) && d.ConditionTrue("Ready") && d.ConditionTrue("Initialized") {
			return true, nil
		}
	}
	return false, nil
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
	// Check status of the S2i buildconfig if the extraConfigurationRepo field is set
	if len(d.Spec.Configuration.ExtraConfigurationRepo) > 0 {
		status, err := r.getBuildStatus(ctx, "sitebuilder-s2i-", d)
		switch {
		case err != nil:
			return newApplicationError(err, ErrClientK8s)
		case status == buildv1.BuildPhaseFailed || status == buildv1.BuildPhaseError:
			return newApplicationError(nil, ErrBuildFailed)
		case status != buildv1.BuildPhaseComplete:
			return newApplicationError(err, ErrTemporary)
		}
	}
	return nil
}

// ensureUpdatedDeployment runs the logic to do the base update for a new Drupal version
// If it returns a reconcileError, if it's a permanent error it will set the condition reason and block retries.
func (r *DrupalSiteReconciler) ensureUpdatedDeployment(ctx context.Context, d *webservicesv1a1.DrupalSite, deploymentConfig DeploymentConfig) (controllerutil.OperationResult, reconcileError) {
	// Update deployment with the new version
	if dbodSecret := databaseSecretName(d); len(dbodSecret) != 0 {
		deploy := &appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Name: d.Name, Namespace: d.Namespace}}
		result, err := ctrl.CreateOrUpdate(ctx, r.Client, deploy, func() error {
			releaseID := releaseID(d)
			return deploymentForDrupalSite(deploy, dbodSecret, d, releaseID, deploymentConfig)
		})
		if err != nil {
			return "", newApplicationError(err, ErrClientK8s)
		}
		return result, nil
	}
	return "", newApplicationError(fmt.Errorf("database secret value empty"), ErrDBOD)
}

// updateDrupalVersion updates the drupal version of the running site to the modified value in the spec
// 1. It first ensures the deployment is updated
// 2. Checks if the rollout has succeeded
// 3. If the rollout succeeds, cache is reloaded on the new version
// 4. If there is any temporary failure at any point, the process is repeated again after a timeout
// 5. If there is a permanent unrecoverable error, the deployment is rolled back to the previous version
// using the 'Failsafe' on the status and a 'CodeUpdateFailed' status is set on the CR
func (r *DrupalSiteReconciler) updateDrupalVersion(ctx context.Context, d *webservicesv1a1.DrupalSite, deploymentConfig DeploymentConfig) (update bool, requeue bool, err reconcileError, errorMessage string) {
	// Ensure the new deployment is rolledout
	result, err := r.ensureUpdatedDeployment(ctx, d, deploymentConfig)
	if err != nil {
		return false, false, err, "%v while deploying the updated Drupal images of version"
	}

	// Check the result of deployment update using ctrl.CreateOrUpdate
	// If unchanged proceed to check if deployment succeeded, else reconcile
	if result == controllerutil.OperationResultNone {
		// Check if deployment has rolled out
		requeue, err := r.didVersionRollOutSucceed(ctx, d)
		switch {
		case err != nil:
			if err.Temporary() {
				// Temporary error while checking for version roll out
				return false, false, err, "Temporary error while checking for version roll out"
				// return false, true, nil, ""
			} else {
				setConditionStatus(d, "CodeUpdateFailed", true, err, false)
				err.Wrap("%v: Failed to update version " + releaseID(d))
				rollBackErr := r.rollBackCodeUpdate(ctx, d, deploymentConfig)
				if rollBackErr != nil {
					return false, false, rollBackErr, "Error while rolling back version"
				}
				return true, false, nil, ""
			}
		case requeue:
			// Waiting for pod to start
			return false, true, nil, ""
		}
	} else {
		// If result doesn't return "unchanged" reconcile
		return false, true, nil, ""
	}

	// Do a drush cr after the new deployment is rolled out. Try it a second time, in case of a failure during the first
	sout, stderr := r.execToServerPodErrOnStderr(ctx, d, "php-fpm", nil, cacheReload()...)
	if stderr != nil {
		sout, stderr = r.execToServerPodErrOnStderr(ctx, d, "php-fpm", nil, cacheReload()...)
		if stderr != nil {
			return true, false, nil, ""
		}
	}
	if sout != "" {
		r.rollBackCodeUpdate(ctx, d, deploymentConfig)
		setConditionStatus(d, "CodeUpdateFailed", true, newApplicationError(nil, errors.New("Error clearing cache")), false)
		return true, false, nil, ""
	}

	// When code updating set to false and everything runs fine, remove the status
	if d.ConditionTrue("CodeUpdateFailed") {
		d.Status.Conditions.RemoveCondition("CodeUpdateFailed")
		return true, false, nil, ""
	}
	return false, false, nil, ""
}

// updateDBSchema updates the drupal schema of the running site after a version update
// 1. Checks if there is any DB tables to be updated
// 2. If nothing, exit
// 3. If error while checking, set status reconcile
// 4. If any updates pending, set 'DBUpdatesPending' in the status, take DB backup, run 'drush updb',
// 5. If there is a permanent unrecoverable error, restore the DB using the backup and set 'DBUpdateFailed' status
// 6. If no error, remove the 'DBUpdatesPending' status and continue
func (r *DrupalSiteReconciler) updateDBSchema(ctx context.Context, d *webservicesv1a1.DrupalSite, log logr.Logger) (update bool) {
	sout, err := r.execToServerPodErrOnStderr(ctx, d, "php-fpm", nil, checkUpdbStatus()...)
	if err != nil {
		return true
	}
	if sout != "" {
		// Set DBUpdatesPending status
		if setDBUpdatesPending(d) {
			return true
		}

		// Take backup
		backupFileName := "db_backup_update_rollback.sql"
		// We set Backup on "Drupal-data" so the DB backup is stored on the PV of the website
		if _, err := r.execToServerPodErrOnStderr(ctx, d, "php-fpm", nil, takeBackup("/drupal-data/"+backupFileName)...); err != nil {
			setConditionStatus(d, "DBUpdatesFailed", true, newApplicationError(err, ErrPodExec), false)
			return true
		}

		// Run updb
		// The updb scripts, puts the site in maintenance mode, runs updb and removes the site from maintenance mode
		_, err = r.execToServerPodErrOnStderr(ctx, d, "php-fpm", nil, runUpDBCommand()...)
		if err != nil {
			err = r.rollBackDBUpdate(ctx, d, backupFileName)
			if err != nil {
				setConditionStatus(d, "DBUpdatesFailed", true, newApplicationError(err, ErrDBUpdateFailed), false)
				return true
			}
			setConditionStatus(d, "DBUpdatesFailed", true, newApplicationError(err, ErrDBUpdateFailed), false)
			return true
		}
	}
	// DB update successful, remove conditions
	if d.ConditionTrue("DBUpdatesPending") {
		d.Status.Conditions.RemoveCondition("DBUpdatesPending")
		if d.ConditionTrue("DBUpdatesFailed") {
			d.Status.Conditions.RemoveCondition("DBUpdatesFailed")
		}
		return true
	}
	return false
}

// rollBackCodeUpdate rolls back the code update process to the previous version when it is called
// It restores the deployment's image to the value of the 'FailsafeDrupalVersion' field on the status
func (r *DrupalSiteReconciler) rollBackCodeUpdate(ctx context.Context, d *webservicesv1a1.DrupalSite, deploymentConfig DeploymentConfig) reconcileError {
	// Restore the server deployment
	if dbodSecret := databaseSecretName(d); len(dbodSecret) != 0 {
		deploy := &appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Name: d.Name, Namespace: d.Namespace}}
		_, err := ctrl.CreateOrUpdate(ctx, r.Client, deploy, func() error {
			return deploymentForDrupalSite(deploy, dbodSecret, d, d.Status.ReleaseID.Failsafe, deploymentConfig)
		})
		if err != nil {
			return newApplicationError(err, ErrClientK8s)
		}
	}
	return nil
}

// rollBackDBUpdate rolls back the DB update process to the previous version of the database from the backup
func (r *DrupalSiteReconciler) rollBackDBUpdate(ctx context.Context, d *webservicesv1a1.DrupalSite, backupFileName string) reconcileError {
	// Restore the database backup
	if _, err := r.execToServerPodErrOnStderr(ctx, d, "php-fpm", nil, restoreBackup(backupFileName)...); err != nil {
		return newApplicationError(err, ErrPodExec)
	}
	return nil
}

// getenvOrDie checks for the given variable in the environment, if not exists
func getenvOrDie(name string, log logr.Logger) string {
	e := os.Getenv(name)
	if e == "" {
		log.V(1).Info(name + ": missing environment variable (unset or empty string)")
		os.Exit(1)
	}
	return e
}

// addGitlabWebhookToStatus adds the Gitlab webhook URL for the s2i (extraconfig) buildconfig to the DrupalSite status
// by querying the K8s API for API Server & Gitlab webhook trigger secret value
func (r *DrupalSiteReconciler) addGitlabWebhookToStatus(ctx context.Context, d *webservicesv1a1.DrupalSite) reconcileError {
	// Fetch the gitlab webhook trigger secret value
	gitlabTriggerSecret := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: "gitlab-trigger-secret-" + d.Name, Namespace: d.Namespace}}
	err := r.Get(ctx, types.NamespacedName{Name: gitlabTriggerSecret.Name, Namespace: gitlabTriggerSecret.Namespace}, gitlabTriggerSecret)
	if err != nil {
		return newApplicationError(errors.New("fetching gitlabTriggerSecret failed"), ErrClientK8s)
	}
	d.Status.GitlabWebhookURL = "https://api." + ClusterName + ".okd.cern.ch:443/apis/build.openshift.io/v1/namespaces/" + d.Namespace + "/buildconfigs/" + "sitebuilder-s2i-" + nameVersionHash(d) + "/webhooks/" + gitlabTriggerSecret.Name + "/gitlab"
	return nil
}

// GetDrupalProjectConfig gets the DrupalProjectConfig for a Project
func (r *DrupalSiteReconciler) GetDrupalProjectConfig(ctx context.Context, drp *webservicesv1a1.DrupalSite) (*webservicesv1a1.DrupalProjectConfig, reconcileError) {
	// Fetch the DrupalProjectConfigList on the Namespace
	drupalProjectConfigList := &webservicesv1a1.DrupalProjectConfigList{}
	if err := r.List(ctx, drupalProjectConfigList, &client.ListOptions{Namespace: drp.Namespace}); err != nil {
		return nil, newApplicationError(errors.New("fetching drupalProjectConfigList failed"), ErrClientK8s)
	}
	if len(drupalProjectConfigList.Items) == 0 {
		r.Log.Info("Warning: Project " + drp.Namespace + " does not contain any DrupalProjectConfig!")
		return nil, nil
	}
	// We get the first DrupalProjectConfig in the Namespace, only one is expected per project!
	return &drupalProjectConfigList.Items[0], nil
}

// proclaimPrimarySiteIfExists will check for Drupalsites in a project, if only one DrupalSite is in place then we consider that primary exists and can be set on the DrupalProjectConfig, otherwise nothing to do as there is no clear Primary site
func (r *DrupalSiteReconciler) proclaimPrimarySiteIfExists(ctx context.Context, drp *webservicesv1a1.DrupalSite, dpc *webservicesv1a1.DrupalProjectConfig) (update bool, reconcileError reconcileError) {
	update = false
	if dpc == nil {
		return
	}
	// Check how many DrupalSites are in place on the project
	drupalSiteList := &webservicesv1a1.DrupalSiteList{}
	if err := r.List(ctx, drupalSiteList, &client.ListOptions{Namespace: drp.Namespace}); err != nil {
		reconcileError = newApplicationError(errors.New("fetching drupalSiteList failed"), ErrClientK8s)
		return
	}
	if len(drupalSiteList.Items) > 1 {
		// Nothing to do in case there's more than one DrupalSite in the project
		return
	}

	if dpc.Spec.PrimarySiteName == "" {
		dpc.Spec.PrimarySiteName = drp.Name
		r.Log.Info("Project" + dpc.Namespace + "contains only 1 drupalsite\"" + drp.Name + "\", which is considered the primary production site")
		update = true
		return
	}
	return
}

//checkIfPrimaryDrupalsite checks if current DrupalSite is primary or not in the project
func (r *DrupalSiteReconciler) checkIfPrimaryDrupalsite(ctx context.Context, drp *webservicesv1a1.DrupalSite, dpc *webservicesv1a1.DrupalProjectConfig) (update bool, reconcileErr reconcileError) {
	update = false
	if dpc == nil {
		return
	}
	// We get the first DrupalProjectConfig in the Namespace, only one is expected per cluster!
	if drp.Name == dpc.Spec.PrimarySiteName && !drp.Status.IsPrimary {
		update = true
		drp.Status.IsPrimary = true
		return
	} else if drp.Name != dpc.Spec.PrimarySiteName && drp.Status.IsPrimary {
		update = true
		drp.Status.IsPrimary = false
		return
	}
	return
}
