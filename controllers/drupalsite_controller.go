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
	"context"
	"fmt"

	"github.com/go-logr/logr"
	appsv1 "github.com/openshift/api/apps/v1"
	buildv1 "github.com/openshift/api/build/v1"
	imagev1 "github.com/openshift/api/image/v1"
	routev1 "github.com/openshift/api/route/v1"
	"github.com/operator-framework/operator-lib/status"
	webservicesv1a1 "gitlab.cern.ch/drupal/paas/drupalsite-operator/api/v1alpha1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
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

// +kubebuilder:rbac:groups=apps.openshift.io,resources=deploymentconfigs,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=core,resources=persistentvolumeclaims,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=core,resources=pods,verbs=get;list;
// +kubebuilder:rbac:groups=route.openshift.io,resources=routes,verbs=get;list;watch;create;update;patch;delete

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
		// drupalSite.Status.Phase = "Deleted"
		// r.updateCRStatusorFailReconcile(ctx, log, drupalSite)
		return r.cleanupDrupalSite(ctx, log, drupalSite)
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
	if update := ensureSpecFinalizer(drupalSite); update {
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
	if transientErr := r.ensureResources(drupalSite, log); transientErr != nil {
		setNotReady(drupalSite, transientErr)
		return handleTransientErr(transientErr, "%v while creating the resources")
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

// SetupWithManager adds a manager which watches the resources
func (r *DrupalSiteReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&webservicesv1a1.DrupalSite{}).
		Owns(&buildv1.BuildConfig{}).
		Owns(&imagev1.ImageStream{}).
		Owns(&appsv1.DeploymentConfig{}).
		Owns(&corev1.Service{}).
		Owns(&corev1.PersistentVolumeClaim{}).
		Owns(&routev1.Route{}).
		Owns(&batchv1.Job{}).
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

// cleanupDrupalSite checks and removes if a finalizer exists on the resource
func (r *DrupalSiteReconciler) cleanupDrupalSite(ctx context.Context, log logr.Logger, app *webservicesv1a1.DrupalSite) (ctrl.Result, error) {
	// finalizer: dependentResources
	// 1. check if such resources exist
	//   - delete them
	//   - reconcile
	// 1. if not, delete the finalizer key manually and let Kubernetes delete the resource cleanly
	// TODO
	log.Info("Deleting DrupalSite")
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
