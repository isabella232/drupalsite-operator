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
	"fmt"
	"reflect"

	buildv1 "github.com/openshift/api/build/v1"
	imagev1 "github.com/openshift/api/image/v1"
	routev1 "github.com/openshift/api/route/v1"
	pipelinev1 "github.com/tektoncd/pipeline/pkg/apis/pipeline/v1beta1"
	velerov1 "github.com/vmware-tanzu/velero/pkg/apis/velero/v1"
	dbodv1a1 "gitlab.cern.ch/drupal/paas/dbod-operator/api/v1alpha1"
	webservicesv1a1 "gitlab.cern.ch/drupal/paas/drupalsite-operator/api/v1alpha1"
	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"

	k8sapierrors "k8s.io/apimachinery/pkg/api/errors"
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
	finalizerStr         = "controller.drupalsite.webservices.cern.ch"
	debugAnnotation      = "debug"
	adminPauseAnnotation = "admin-pause-reconcile"
	oidcSecretName       = "oidc-client-secret"
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
	// DefaultD8ReleaseSpec refers to the releaseSpec for Drupal v8.9-2 to be defaulted incase it is empty
	DefaultD8ReleaseSpec string
	// DefaultD93ReleaseSpec refers to the releaseSpec for Drupal v9.3-1 to be defaulted incase it is empty
	DefaultD93ReleaseSpec string
	// DefaultD932ReleaseSpec refers to the releaseSpec for Drupal v9.3-2 to be defaulted incase it is empty
	DefaultD932ReleaseSpec string
	// ParallelThreadCount refers to the number of parallel reconciliations done by the Operator
	ParallelThreadCount int
	// EnableTopologySpread refers to enabling avaliability zone scheduling for critical site deployments
	EnableTopologySpread bool
	// ClusterName refers to the name of the cluster the operator is running on
	ClusterName string
	// EasystartBackupName refers to the name of the easystart backup
	EasystartBackupName string
)

// DrupalSiteReconciler reconciles a DrupalSite object
type DrupalSiteReconciler struct {
	Reconciler
}

// +kubebuilder:rbac:groups=drupal.webservices.cern.ch,resources=drupalsites,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=drupal.webservices.cern.ch,resources=drupalsites/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=drupal.webservices.cern.ch,resources=drupalsites/finalizers,verbs=update
// +kubebuilder:rbac:groups=drupal.webservices.cern.ch,resources=drupalsiteconfigoverrides,verbs=get;list;watch
// +kubebuilder:rbac:groups=drupal.webservices.cern.ch,resources=drupalprojectconfigs,verbs=get;list;watch;update;patch
// +kubebuilder:rbac:groups=drupal.webservices.cern.ch,resources=drupalprojectconfigs/status,verbs=get;update;patch
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
// +kubebuilder:rbac:groups=tekton.dev,resources=taskruns,verbs=get;list;watch;create;delete

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
		Owns(&pipelinev1.TaskRun{}).
		Watches(&source.Kind{Type: &velerov1.Backup{}}, handler.EnqueueRequestsFromMapFunc(
			// Reconcile every DrupalSite in the project referred to by the Backup
			func(a client.Object) []reconcile.Request {
				log := r.Log.WithValues("Source", "Velero Backup event handler", "Namespace", a.GetNamespace())
				projectName, exists := a.GetAnnotations()["drupal.webservices.cern.ch/project"]
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
	// Fetch the DrupalProjectConfig Resource
	drupalProjectConfig, err := r.GetDrupalProjectConfig(ctx, drupalSite)
	if err != nil {
		log.Error(err, fmt.Sprintf("%v failed to retrieve DrupalProjectConfig", err))
		// Although we log an Error, we will allow the reconcile to continue, as the absence of the resource does not mean DrupalSite cannot be processed
		// Populate resource as nil
		drupalProjectConfig = nil
	}

	//Handle deletion
	if drupalSite.GetDeletionTimestamp() != nil {
		if controllerutil.ContainsFinalizer(drupalSite, finalizerStr) {
			return r.cleanupDrupalSite(ctx, log, drupalSite, drupalProjectConfig)
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
	// Log and schedule a new reconciliation
	handleNonfatalErr := func(nonfatalErr reconcileError, logstrFmt string) {
		if nonfatalErr == nil {
			return
		}
		if nonfatalErr.Temporary() {
			log.Error(nonfatalErr, fmt.Sprintf(logstrFmt, nonfatalErr.Unwrap()))
		} else {
			log.Error(nonfatalErr, "Permanent error marked as transient! Permanent errors should not bubble up to the reconcile loop.")
		}
		// emitting error because the controller can count it in the error metrics,
		// which we can monitor to notice transient problems affecting the entire infrastructure
		requeueFlag = nonfatalErr
	}

	// 0. Skip reconciliation if admin-pause annotation is present

	// This is a "short-circuit" mechanism to forcibly stop reconciling misbehaving websites
	if _, isAdminPauseAnnotationPresent := drupalSite.GetAnnotations()[adminPauseAnnotation]; isAdminPauseAnnotationPresent {
		return ctrl.Result{}, nil
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

	// 2. Check all conditions and update them if needed
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

	// Check if the site is installed, cloned or easystart and mark the condition
	if !drupalSite.ConditionTrue("Initialized") {
		if r.isDrupalSiteInstalled(ctx, drupalSite) || r.isCloneJobCompleted(ctx, drupalSite) || r.isEasystartTaskRunCompleted(ctx, drupalSite) {
			update = setInitialized(drupalSite) || update
		} else {
			update = setNotInitialized(drupalSite) || update
		}
	}

	// If it's a site with extraConfig Spec, add the gitlab webhook trigger to the Status
	// The URL is dependent on BuildConfig name, which is based on nameVersionHash() function. Therefore it needs to be updated when there is a ReleaseID update
	// For consistency, we update the field on every reconcile
	if len(drupalSite.Spec.Configuration.ExtraConfigurationRepo) > 0 {
		update = addGitlabWebhookToStatus(ctx, drupalSite) || update
	}

	// Check if current instance is the Primary Drupalsite and update Status
	update = r.checkIfPrimaryDrupalsite(ctx, drupalSite, drupalProjectConfig) || update

	// Update status with all the conditions that were checked
	if update {
		return r.updateCRStatusOrFailReconcile(ctx, log, drupalSite)
	}

	// Check if DrupalProjectConfig has not a primary website + This DrupalSite instance is unique -> Become Primary Website
	updateProjectConfig, reconcileErr := r.proclaimPrimarySiteIfExists(ctx, drupalSite, drupalProjectConfig)
	switch {
	case err != nil:
		log.Error(err, fmt.Sprintf("%v failed to declare this DrupalSite as Primary", reconcileErr.Unwrap()))
		setErrorCondition(drupalSite, reconcileErr)
		return r.updateCRStatusOrFailReconcile(ctx, log, drupalSite)
	case updateProjectConfig:
		log.Info("Proclaiming this site as primary in DrupalProjectConfig")
		r.checkIfPrimaryDrupalsite(ctx, drupalSite, drupalProjectConfig)
		if err = r.updateDrupalProjectConfigCR(ctx, log, drupalProjectConfig); err != nil {
			handleNonfatalErr(newApplicationError(err, ErrClientK8s), "%v while updating DrupalProjectConfig")
		}
	}

	backupList, err := r.checkNewBackups(ctx, drupalSite, log)
	switch {
	case err != nil:
		log.Error(err, fmt.Sprintf("%v failed to check for new backups", reconcileErr.Unwrap()))
		return ctrl.Result{}, err
	// DeepEqual returns false when one of the slice is empty
	case len(backupList) != len(drupalSite.Status.AvailableBackups) && !reflect.DeepEqual(backupList, drupalSite.Status.AvailableBackups):
		drupalSite.Status.AvailableBackups = backupList
		return r.updateCRStatusOrFailReconcile(ctx, log, drupalSite)
	}

	// 2.1 Set conditions related to update

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

	log.V(3).Info("Ensured all resources are present.")

	// 4. Check DBOD has been provisioned and reconcile if needed

	if dbodReady := r.isDBODProvisioned(ctx, drupalSite); !dbodReady {
		if update := setNotReady(drupalSite, newApplicationError(nil, ErrDBOD)); update {
			r.updateCRStatusOrFailReconcile(ctx, log, drupalSite)
		}
		return reconcile.Result{Requeue: true}, nil
	}

	// Update the ServingPodImage if different
	if drupalSite.Status.ServingPodImage != sitebuilderImageRefToUse(drupalSite, releaseID(drupalSite)).Name {
		deploy := &appsv1.Deployment{}
		fetchError := r.Get(ctx, types.NamespacedName{Name: drupalSite.Name, Namespace: drupalSite.Namespace}, deploy)
		if fetchError != nil {
			// return false, newApplicationError(fetchError, ErrClientK8s)
			return handleTransientErr(newApplicationError(fetchError, ErrClientK8s), "Failed to fetch deployment object", "")
		}
		if deploy.Status.Replicas == deploy.Status.UpdatedReplicas {
			drupalSite.Status.ServingPodImage = sitebuilderImageRefToUse(drupalSite, releaseID(drupalSite)).Name
		}
		return r.updateCRStatusOrFailReconcile(ctx, log, drupalSite)
	}

	// Returning err with Reconcile functions causes a requeue by default following exponential backoff
	// Ref https://gitlab.cern.ch/paas-tools/operators/authz-operator/-/merge_requests/76#note_4501887
	return ctrl.Result{}, requeueFlag
}
