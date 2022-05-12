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

	webservicesv1a1 "gitlab.cern.ch/drupal/paas/drupalsite-operator/api/v1alpha1"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	k8sapierrors "k8s.io/apimachinery/pkg/api/errors"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/controller-runtime/pkg/source"
)

// var (
// 	log = logf.Log.WithName("controller_supported_drupal_versions")
// )

// DrupalSiteDBUpdateReconciler reconciles a DrupalSite object
type DrupalSiteDBUpdateReconciler struct {
	Reconciler
}

const (
	// 24 Hours
	periodicUpDBStCheckTimeOutHours = 24
	timeLayout                      = "Jan 2, 2006 at 3:04pm (UTC)"
)

// +kubebuilder:rbac:groups=drupal.webservices.cern.ch,resources=drupalsites,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=drupal.webservices.cern.ch,resources=drupalsites/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=drupal.webservices.cern.ch,resources=drupalsites/finalizers,verbs=update
// +kubebuilder:rbac:groups=app,resources=deployments,verbs=*

// SetupWithManager adds a manager which watches the resources
func (r *DrupalSiteDBUpdateReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&webservicesv1a1.DrupalSite{}).
		Watches(&source.Kind{Type: &appsv1.Deployment{}}, handler.EnqueueRequestsFromMapFunc(
			// Reconcile every DrupalSite in the project referred to by the Backup
			func(a client.Object) []reconcile.Request {
				log := r.Log.WithValues("Source", "Drupal Deployments event handler", "Namespace", a.GetNamespace())
				_, exists := a.GetLabels()["drupalSite"]
				if exists {
					return fetchDrupalSitesInNamespace(mgr, log, a.GetNamespace())
				}
				return []reconcile.Request{}
			}),
		).
		Complete(r)
}

func (r *DrupalSiteDBUpdateReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	// _ = context.Background()
	log := r.Log.WithValues("Request.Namespace", req.NamespacedName, "Request.Name", req.Name)
	log.V(1).Info("Reconciling request for DrupalSite update")
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

	// 2. Check all conditions and update them if needed
	update := false

	// This is a mechanism to restore a failed update. If we reset the spec to the failsafe, then the Failed conditions are cleared
	if drupalSite.Status.ReleaseID.Failsafe == releaseID(drupalSite) {
		if drupalSite.ConditionTrue("CodeUpdateFailed") {
			update = drupalSite.Status.Conditions.RemoveCondition("CodeUpdateFailed") || update
		}
		if drupalSite.ConditionTrue("DBUpdatesFailed") {
			update = drupalSite.Status.Conditions.RemoveCondition("DBUpdatesFailed") || update
		}
	}

	// Check drupalsite has the right version

	// 1. Checks if the rollout has succeeded
	// 2. Set condition "CodeUpdateFailed" to true if there is an unrecoverable error & rollback

	if drupalSite.ConditionTrue("Ready") && drupalSite.ConditionTrue("Initialized") && !drupalSite.ConditionTrue("CodeUpdateFailed") {
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

		update, requeue, err, errorMessage := r.checkUpdatedDrupalDeployment(ctx, drupalSite, deploymentConfig)
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

		dbUpdatesPending := drupalSite.Status.Conditions.GetCondition("DBUpdatesPending")
		// We first check if `drush updbst` is needed based on DrupalSite status values
		check, err := r.checkIfDBUpdatesAreNeeded(ctx, drupalSite)
		if err != nil {
			handleTransientErr(err, "Failed to parse DBUpdatesLastCheckTimestamp while checking if dbUpdates check is required", "")
		}
		// If the `drush updbst` is needed, we go ahead and run it
		if check {
			statusUpdatedNeeded, reconcileErr := r.dbUpdateNeeded(ctx, drupalSite)
			// 1. Set status condition DBUpdatesPending
			switch {
			case reconcileErr != nil:
				handleNonfatalErr(reconcileErr, "%v while checking if a DB update is needed")
				return r.updateCRStatusOrFailReconcile(ctx, log, drupalSite)
			case statusUpdatedNeeded:
				return r.updateCRStatusOrFailReconcile(ctx, log, drupalSite)
			}
		}
		// We run `drush updb` when the specific conditions match
		if dbUpdatesPending != nil && dbUpdatesPending.Status == corev1.ConditionTrue && !drupalSite.ConditionTrue("DBUpdatesFailed") {
			update, err := r.updateDBSchema(ctx, drupalSite, log)
			if err != nil {
				handleTransientErr(newApplicationError(err, ErrClientK8s), "Failed to update DB Schema", "")
			}
			if update {
				return r.updateCRStatusOrFailReconcile(ctx, log, drupalSite)
			}
		}

		// Update the Failsafe during the first instantiation and after a successful update
		if drupalSite.Status.ReleaseID.Current != drupalSite.Status.ReleaseID.Failsafe && drupalSite.ConditionFalse("DBUpdatesPending") && !drupalSite.ConditionTrue("DBUpdatesFailed") && !drupalSite.ConditionTrue("CodeUpdateFailed") {
			drupalSite.Status.ReleaseID.Failsafe = releaseID(drupalSite)
			return r.updateCRStatusOrFailReconcile(ctx, log, drupalSite)
		}
	}

	if drupalSite.ConditionTrue("CodeUpdateFailed") {
		// Set condition unknown
		if setConditionStatus(drupalSite, "DBUpdatesPending", false, nil, true) {
			return r.updateCRStatusOrFailReconcile(ctx, log, drupalSite)
		}
	}

	// Returning err with Reconcile functions causes a requeue by default following exponential backoff
	// Ref https://gitlab.cern.ch/paas-tools/operators/authz-operator/-/merge_requests/76#note_4501887
	return ctrl.Result{}, requeueFlag
}
