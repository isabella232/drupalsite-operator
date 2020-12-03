/*


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

	"github.com/go-logr/logr"
	appsv1 "github.com/openshift/api/apps/v1"
	routev1 "github.com/openshift/api/route/v1"
	"github.com/operator-framework/operator-lib/status"
	webservicescernchv1alpha1 "gitlab.cern.ch/drupal/paas/drupalsite-operator/api/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

// DrupalSiteRequestReconciler reconciles a DrupalSiteRequest object
type DrupalSiteRequestReconciler struct {
	client.Client
	Log    logr.Logger
	Scheme *runtime.Scheme
}

/*
The Reconcile(req ctrl.Request) (ctrl.Result, error) steps
1. read the resource
1. handle deletion
1. ensure finalizer
1. [validate spec]
1. ensure children resources
  - Check if resources are created. Check error and resolve. Check resource spec also for detailed error and report it
*/

// +kubebuilder:rbac:groups=webservices.cern.ch,resources=drupalsiterequests,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=webservices.cern.ch,resources=drupalsiterequests/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=apps.openshift.io,resources=deploymentconfigs,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=core,resources=persistentvolumeclaims,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=core,resources=pods,verbs=get;list;
// +kubebuilder:rbac:groups=route.openshift.io,resources=routes,verbs=get;list;watch;create;update;patch;delete

// Reconcile runs the main reocncile loop
func (r *DrupalSiteRequestReconciler) Reconcile(req ctrl.Request) (ctrl.Result, error) {
	ctx := context.TODO()
	// _ = context.Background()
	log := r.Log.WithValues("Request.Namespace", req.NamespacedName, "Request.Name", req.Name)

	log.Info("Reconciling request")

	// Fetch the DrupalSiteRequest instance
	drupalSiteRequest := &webservicescernchv1alpha1.DrupalSiteRequest{}
	err := r.Get(ctx, req.NamespacedName, drupalSiteRequest)
	if err != nil {
		if errors.IsNotFound(err) {
			// Request object not found, could have been deleted after reconcile request.
			// Owned objects are automatically garbage collected. For additional cleanup logic use finalizers.
			// Return and don't requeue
			log.Info("DrupalSiteRequest resource not found. Ignoring since object must be deleted")
			return ctrl.Result{}, nil
		}
		// Error reading the object - requeue the request.
		log.Error(err, "Failed to get DrupalSiteRequest")
		return ctrl.Result{}, err
	}

	//Handle deletion
	if drupalSiteRequest.GetDeletionTimestamp() != nil {
		// drupalSiteRequest.Status.Phase = "Deleted"
		// r.updateCRStatusorFailReconcile(ctx, log, drupalSiteRequest)
		return r.cleanupDrupalSiteRequest(ctx, log, drupalSiteRequest)
	}

	handleTransientErr := func(transientErr reconcileError, logstrFmt string) (reconcile.Result, error) {
		setNotReady(drupalSiteRequest, transientErr)
		r.updateCRStatusorFailReconcile(ctx, log, drupalSiteRequest)
		if transientErr.Temporary() {
			log.Error(transientErr, fmt.Sprintf(logstrFmt, transientErr.Unwrap()))
			return reconcile.Result{}, transientErr
		}
		log.Error(transientErr, "Permanent error marked as transient! Permanent errors should not bubble up to the reconcile loop.")
		return reconcile.Result{}, nil
	}

	// Init. Check if finalizer is set. If not, set it, validate and update CR status
	if update := ensureSpecFinalizer(drupalSiteRequest); update {
		log.Info("Initializing DrupalSiteRequest Spec")
		return r.updateCRorFailReconcile(ctx, log, drupalSiteRequest)
	}
	if err := validateSpec(drupalSiteRequest.Spec); err != nil {
		log.Error(err, fmt.Sprintf("%v failed to validate DrupalSiteRequest spec", err.Unwrap()))
		setErrorCondition(drupalSiteRequest, err)
		return r.updateCRStatusorFailReconcile(ctx, log, drupalSiteRequest)
	}
	if !drupalSiteRequest.ConditionTrue("Installed") {

		// NOTE: we can put the installation workflow here, because some parts of it will be different than `ensureDependentResources`
		log.Info("Installing DrupalSiteRequest")
		if transientErr := r.ensureInstalled(drupalSiteRequest); transientErr != nil {
			return handleTransientErr(transientErr, "%v while installing the website")
		}
		return r.updateCRStatusorFailReconcile(ctx, log, drupalSiteRequest)
	}

	// maintain
	if transientErr := r.ensureDependentResources(drupalSiteRequest); transientErr != nil {
		return handleTransientErr(transientErr, "%v")
	}

	if update := setReady(drupalSiteRequest); update {
		return r.updateCRStatusorFailReconcile(ctx, log, drupalSiteRequest)
	}

	return ctrl.Result{}, nil
}

// SetupWithManager adds a manager which watches the resources
func (r *DrupalSiteRequestReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&webservicescernchv1alpha1.DrupalSiteRequest{}).
		Owns(&appsv1.DeploymentConfig{}).
		Owns(&corev1.Service{}).
		Owns(&corev1.PersistentVolumeClaim{}).
		Owns(&routev1.Route{}).
		Complete(r)
}

// Add watches for other resources controller.Watch check API Use filters with labels to pick up the resource
// Structure
// Fetch the CR
// Add clean up and delete https://gitlab.cern.ch:8443/paas-tools/operators/authz-operator/blob/24717af14e0792a416168eaea99dd4bbd9b83b9b/pkg/controller/applicationregistration/applicationregistration_controller.go#L117-118
// Actions on initialization
// Actions on maintaining https://gitlab.cern.ch:8443/paas-tools/operators/authz-operator/blob/24717af14e0792a416168eaea99dd4bbd9b83b9b/pkg/controller/applicationregistration/applicationregistration_controller.go#L152

// Status - creation complete
// Transient Error Reconcile Error

// cleanupDrupalSiteRequest checks and removes if a finalizer exists on the resource
func (r *DrupalSiteRequestReconciler) cleanupDrupalSiteRequest(ctx context.Context, log logr.Logger, app *webservicescernchv1alpha1.DrupalSiteRequest) (ctrl.Result, error) {
	// finalizer: dependentResources
	// 1. check if such resources exist
	//   - delete them
	//   - reconcile
	// 1. if not, delete the finalizer key manually and let Kubernetes delete the resource cleanly
	// TODO
	log.Info("Deleting DrupalSiteRequest")
	remainingFinalizers := app.GetFinalizers()
	for i, finalizer := range remainingFinalizers {
		if finalizer == finalizerStr {
			remainingFinalizers = append(remainingFinalizers[:i], remainingFinalizers[i+1:]...)
			break
		}
	}
	app.SetFinalizers(remainingFinalizers)
	return r.updateCRorFailReconcile(ctx, log, app)
}

func setReady(drp *webservicescernchv1alpha1.DrupalSiteRequest) (update bool) {
	return drp.Status.Conditions.SetCondition(status.Condition{
		Type:   "Ready",
		Status: "True",
	})
}
func setNotReady(drp *webservicescernchv1alpha1.DrupalSiteRequest, transientErr reconcileError) (update bool) {
	return drp.Status.Conditions.SetCondition(status.Condition{
		Type:    "Ready",
		Status:  "False",
		Reason:  status.ConditionReason(transientErr.Unwrap().Error()),
		Message: transientErr.Error(),
	})
}
func setErrorCondition(drp *webservicescernchv1alpha1.DrupalSiteRequest, err reconcileError) (update bool) {
	return drp.Status.Conditions.SetCondition(status.Condition{
		Type:    "Error",
		Status:  "True",
		Reason:  status.ConditionReason(err.Unwrap().Error()),
		Message: err.Error(),
	})
}
