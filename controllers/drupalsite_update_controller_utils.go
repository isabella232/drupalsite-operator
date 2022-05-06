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
	"time"

	"github.com/go-logr/logr"
	webservicesv1a1 "gitlab.cern.ch/drupal/paas/drupalsite-operator/api/v1alpha1"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
)

// UpdateNeeded checks if a DB update is required based on the image tag and releaseID in the CR spec.
// Only safe to call `if d.ConditionTrue("Ready") && d.ConditionTrue("Initialized")`
func (r *DrupalSiteDBUpdateReconciler) codeUpdateNeeded(ctx context.Context, d *webservicesv1a1.DrupalSite) (bool, reconcileError) {
	deployment, err := r.getRunningdeployment(ctx, d)
	if err != nil {
		return false, newApplicationError(err, ErrClientK8s)
	}
	// Check if image is different, check if current site is ready and installed
	// Also check if failSafe and Current are different. If they are different, it means the deployment hasn't rolled out
	if deployment.Spec.Template.ObjectMeta.Annotations["releaseID"] != releaseID(d) || (len(d.Status.ReleaseID.Failsafe) > 0 && d.Status.ReleaseID.Failsafe != d.Status.ReleaseID.Current) {
		return true, nil
	}
	return false, nil
}

// dbUpdateNeeded checks updbst to see if DB updates are needed
// If there is an error, the return value is false
func (r *DrupalSiteDBUpdateReconciler) dbUpdateNeeded(ctx context.Context, d *webservicesv1a1.DrupalSite) (updatedNeeded bool, requeue bool, reconcileErr reconcileError) {
	if len(d.Annotations["drupal.cern.ch/DBUpdatesLastCheckTimestamp"]) > 0 {
		lastCheckedTime, _ := time.Parse(layout, d.Annotations["drupal.cern.ch/DBUpdatesLastCheckTimestamp"])
		// If the last check happened < errorUpDBStCheckTimeOutMinutes ago we skip checking
		if time.Since(lastCheckedTime).Minutes() < errorUpDBStCheckTimeOutMinutes {
			return false, false, nil
		}
		// If the errorUpDBStCheckTimeOutMinutes < last check > periodicUpDBStCheckTimeOutHours, we check if the Status condition DBUpdatesNeeded is set to not status: Unknown. In this case, we skip
		if errorUpDBStCheckTimeOutMinutes < time.Since(lastCheckedTime).Minutes() && time.Since(lastCheckedTime).Hours() > periodicUpDBStCheckTimeOutHours && d.Status.Conditions.GetCondition("DBUpdatesNeeded").Status != corev1.ConditionUnknown {
			return false, false, nil
		}
	}

	sout, err := r.execToServerPodErrOnStderr(ctx, d, "php-fpm", nil, checkUpdbStatus()...)
	if err != nil {
		// When exec fails, we need to return false. Else it affects the other operations on the controller
		// Returning true will also make local tests fails as execToPod is not possible to emulate
		return false, false, newApplicationError(err, ErrPodExec)
	}
	// Update "updbstcheck" timestamp
	if d.Annotations == nil {
		d.Annotations = map[string]string{}
	}
	// Update "drupal.cern.ch/DBUpdatesLastCheckTimestamp" annotation value
	d.Annotations["drupal.cern.ch/DBUpdatesLastCheckTimestamp"] = time.Now().Format(layout)
	// DB table updates needed
	if sout != "" {
		return true, true, nil
	}
	// No db table updates needed
	return false, true, nil
}

// getRunningdeployment fetches the running drupal deployment
func (r *DrupalSiteDBUpdateReconciler) getRunningdeployment(ctx context.Context, d *webservicesv1a1.DrupalSite) (*appsv1.Deployment, error) {
	deployment := &appsv1.Deployment{}
	err := r.Get(ctx, types.NamespacedName{Name: d.Name, Namespace: d.Namespace}, deployment)
	return deployment, err
}

// ensureUpdatedDeployment runs the logic to do the base update for a new Drupal version
// If it returns a reconcileError, if it's a permanent error it will set the condition reason and block retries.
func (r *DrupalSiteDBUpdateReconciler) ensureUpdatedDeployment(ctx context.Context, d *webservicesv1a1.DrupalSite, deploymentConfig DeploymentConfig) (controllerutil.OperationResult, reconcileError) {
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

// checkUpdatedDrupalDeployment checks if the Drupal deployment has successfully updated to the specified version
// 1. Checks if the rollout has succeeded
// 2. If the rollout succeeds, cache is reloaded on the new version
// 3. If there is any temporary failure at any point, the process is repeated again after a timeout
// 4. If there is a permanent unrecoverable error, the deployment is rolled back to the previous version
// using the 'Failsafe' on the status and a 'CodeUpdateFailed' status is set on the CR
func (r *DrupalSiteDBUpdateReconciler) checkUpdatedDrupalDeployment(ctx context.Context, d *webservicesv1a1.DrupalSite, deploymentConfig DeploymentConfig) (update bool, requeue bool, err reconcileError, errorMessage string) {
	// Check if deployment has rolled out
	requeueNeeded, err := r.checkVersionRolloutSuccess(ctx, d)
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
	case requeueNeeded:
		// Waiting for pod to start
		return false, true, nil, ""
	}

	// When code updating set to false and everything runs fine, remove the status
	if d.ConditionTrue("CodeUpdateFailed") {
		d.Status.Conditions.RemoveCondition("CodeUpdateFailed")
		return true, false, nil, ""
	}
	return false, false, nil, ""
}

// checkVersionRolloutSuccess checks if the deployment has rolled out the new pods successfully and the new pods are running
func (r *DrupalSiteDBUpdateReconciler) checkVersionRolloutSuccess(ctx context.Context, d *webservicesv1a1.DrupalSite) (requeue bool, reconcileErr reconcileError) {
	// check first if replicas == updatedReplicas on the deployment
	deploy := &appsv1.Deployment{}
	fetchError := r.Get(ctx, types.NamespacedName{Name: d.Name, Namespace: d.Namespace}, deploy)
	if fetchError != nil {
		return false, newApplicationError(fetchError, ErrClientK8s)
	}

	deploymentStable := deploy.Status.Replicas == deploy.Status.UpdatedReplicas
	if !deploymentStable {
		// we know that rollout is still ongoing
		// here we identify the condition: did it start? are we past the grace period? and return the appropriate error / condition
		pod, err := r.getPodForVersion(ctx, d, releaseID(d))
		if err != nil && err.Temporary() {
			return false, newApplicationError(err, ErrClientK8s)
		}
		if pod.Status.Phase == corev1.PodFailed || pod.Status.Phase == corev1.PodUnknown {
			return false, newApplicationError(errors.New("pod did not roll out successfully"), ErrDeploymentUpdateFailed)
		}
		if pod.Status.Phase == corev1.PodPending {
			currentTime := time.Now()
			if currentTime.Sub(pod.GetCreationTimestamp().Time).Minutes() < getGracePeriodMinutesForPodToStartDuringUpgrade(d) {
				return true, newApplicationError(errors.New("waiting for pod to start"), ErrPodNotRunning)
			}
			return false, newApplicationError(errors.New("pod failed to start after grace period"), ErrDeploymentUpdateFailed)
		}
		return
	}

	return true, nil
}

// rollBackCodeUpdate rolls back the code update process to the previous version when it is called
// It restores the deployment's image to the value of the 'FailsafeDrupalVersion' field on the status
func (r *DrupalSiteDBUpdateReconciler) rollBackCodeUpdate(ctx context.Context, d *webservicesv1a1.DrupalSite, deploymentConfig DeploymentConfig) reconcileError {
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

// updateDBSchema updates the drupal schema of the running site after a version update
// 1. Takes Backup of the current database state
// 2. Then runs `drush updb -y` to update the database schema
// 3. If there is a permanent unrecoverable error, DBUpdatesFailed status is set with the error
// 4. If temporary error (i.e ErrClientK8s), error is propagated & tried again
// 5. If no error remove the 'DBUpdatesPending' status and continue
func (r *DrupalSiteDBUpdateReconciler) updateDBSchema(ctx context.Context, d *webservicesv1a1.DrupalSite, log logr.Logger) (update bool, err error) {
	// Take backup
	backupFileName := "db_backup_update_rollback.sql"
	// We set Backup on "Drupal-data" so the DB backup is stored on the PV of the website
	// Handle transient error with switch statements when err == ErrCLientK8s
	if _, err := r.execToServerPodErrOnStderr(ctx, d, "php-fpm", nil, takeBackup("/drupal-data/"+backupFileName)...); err != nil {
		if err == ErrClientK8s {
			return false, err
		}
		setConditionStatus(d, "DBUpdatesFailed", true, newApplicationError(err, ErrPodExec), false)
		return true, nil
	}

	// Run updb
	// The updb scripts, puts the site in maintenance mode, runs updb and removes the site from maintenance mode
	if _, err := r.execToServerPodErrOnStderr(ctx, d, "php-fpm", nil, runUpDBCommand()...); err != nil {
		if err != ErrClientK8s {
			return false, err
		}
		// Removing rollBackDBUpdate as we broken sites to keep up with updating
		// We let the site administrators to rectify the problem manually
		setConditionStatus(d, "DBUpdatesFailed", true, newApplicationError(err, ErrDBUpdateFailed), false)
		return true, nil
	}
	// DB update successful, remove conditions
	update = d.Status.Conditions.RemoveCondition("DBUpdatesFailed") || update
	return true, nil
}

// getPodForVersion fetches the list of the pods for the current deployment and returns the first one from the list
func (r *DrupalSiteDBUpdateReconciler) getPodForVersion(ctx context.Context, d *webservicesv1a1.DrupalSite, releaseID string) (corev1.Pod, reconcileError) {
	podList := corev1.PodList{}
	podLabels, err := metav1.LabelSelectorAsSelector(&metav1.LabelSelector{
		MatchLabels: map[string]string{"drupalSite": d.Name, "app": "drupal"},
	})
	if err != nil {
		return corev1.Pod{}, newApplicationError(err, ErrFunctionDomain)
	}
	options := client.ListOptions{
		LabelSelector: podLabels,
		Namespace:     d.Namespace,
	}
	err = r.List(ctx, &podList, &options)
	switch {
	case err != nil:
		return corev1.Pod{}, newApplicationError(err, ErrClientK8s)
	case len(podList.Items) == 0:
		return corev1.Pod{}, newApplicationError(fmt.Errorf("No pod found with given labels: %s", podLabels), ErrTemporary)
	}
	for _, v := range podList.Items {
		if v.Annotations["releaseID"] == releaseID {
			return v, nil
		}
	}
	// iterate through the list and return the first pod that has the status condition ready
	return corev1.Pod{}, newApplicationError(err, ErrClientK8s)
}
