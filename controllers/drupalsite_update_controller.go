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

	k8sapierrors "k8s.io/apimachinery/pkg/api/errors"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

// var (
// 	log = logf.Log.WithName("controller_supported_drupal_versions")
// )

// DrupalSiteUpdateReconciler reconciles a DrupalSite object
type DrupalSiteUpdateReconciler struct {
	Reconciler
}

// +kubebuilder:rbac:groups=drupal.webservices.cern.ch,resources=drupalsites,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=drupal.webservices.cern.ch,resources=drupalsites/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=drupal.webservices.cern.ch,resources=drupalsites/finalizers,verbs=update

// SetupWithManager adds a manager which watches the resources
func (r *DrupalSiteUpdateReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&webservicesv1a1.DrupalSite{}).
		Complete(r)
}

func (r *DrupalSiteUpdateReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
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

	// After a failed update, to be able to restore the site back to the last running version, the status error fields have to be removed if they are set
	if drupalSite.Status.ReleaseID.Failsafe == releaseID(drupalSite) {
		if drupalSite.ConditionTrue("CodeUpdateFailed") {
			update = drupalSite.Status.Conditions.RemoveCondition("CodeUpdateFailed") || update
		}
		if drupalSite.ConditionTrue("DBUpdatesFailed") {
			update = drupalSite.Status.Conditions.RemoveCondition("DBUpdatesFailed") || update
		}
	}

	// 2.1 Set conditions related to update

	// Check for updates after all resources are ensured. Else, this blocks the other logic like ensure resources, blocking sites when the controller can not exec/ run updb
	// Condition `UpdateNeeded` <- either image not matching `releaseID` or `drush updb` needed
	// Check for an update, only when the site is initialized and ready to prevent checks during an installation/ upgrade
	dbUpdateNeeded := false
	if drupalSite.ConditionTrue("Ready") && drupalSite.ConditionTrue("Initialized") && !drupalSite.ConditionTrue("CodeUpdateFailed") {
		var reconcileErr reconcileError
		// Check for db updates only when codeUpdateNeeded is not inProgress
		dbUpdateNeeded, reconcileErr = r.dbUpdateNeeded(ctx, drupalSite)
		if reconcileErr != nil {
			handleNonfatalErr(reconcileErr, "%v while checking if a DB update is needed")
		}
		// 1. Set status condition DBUpdatesPending
		switch {
		case dbUpdateNeeded:
			if setDBUpdatesPending(drupalSite) {
				return r.updateCRStatusOrFailReconcile(ctx, log, drupalSite)
			}
		case !dbUpdateNeeded:
			if removeDBUpdatesPending(drupalSite) {
				return r.updateCRStatusOrFailReconcile(ctx, log, drupalSite)
			}
		}
	}
	if drupalSite.ConditionTrue("CodeUpdateFailed") {
		// Set condition unknown
		if setConditionStatus(drupalSite, "DBUpdatesPending", false, nil, true) {
			return r.updateCRStatusOrFailReconcile(ctx, log, drupalSite)
		}
	}

	// Take db Backup on PVC
	// Put site in maintenance mode
	// Run drush updatedb
	// Remove site from maintenance mode
	// Restore backup in case of a failure

	if dbUpdateNeeded && !drupalSite.ConditionTrue("DBUpdatesFailed") && !drupalSite.ConditionTrue("CodeUpdateFailed") {
		if update := r.updateDBSchema(ctx, drupalSite, log); update {
			return r.updateCRStatusOrFailReconcile(ctx, log, drupalSite)
		}
	}

	// Returning err with Reconcile functions causes a requeue by default following exponential backoff
	// Ref https://gitlab.cern.ch/paas-tools/operators/authz-operator/-/merge_requests/76#note_4501887
	return ctrl.Result{}, requeueFlag
}
