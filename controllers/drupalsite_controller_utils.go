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
	"crypto/md5"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"

	"github.com/go-logr/logr"
	buildv1 "github.com/openshift/api/build/v1"
	pipelinev1 "github.com/tektoncd/pipeline/pkg/apis/pipeline/v1beta1"
	velerov1 "github.com/vmware-tanzu/velero/pkg/apis/velero/v1"
	dbodv1a1 "gitlab.cern.ch/drupal/paas/dbod-operator/api/v1alpha1"
	webservicesv1a1 "gitlab.cern.ch/drupal/paas/drupalsite-operator/api/v1alpha1"
	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	k8sapierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"knative.dev/pkg/apis"
	controllerruntime "sigs.k8s.io/controller-runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
)

// updateCRorFailReconcile tries to update the Custom Resource and logs any error
func (r *DrupalSiteReconciler) updateDrupalProjectConfigCR(ctx context.Context, log logr.Logger, dpc *webservicesv1a1.DrupalProjectConfig) error {
	err := r.Update(ctx, dpc)
	if err != nil {
		if k8sapierrors.IsConflict(err) {
			log.V(4).Info("DrupalProjectConfig changed while reconciling. Requeuing.")
		} else {
			log.Error(err, fmt.Sprintf("%v failed to update the application", ErrClientK8s))
		}
	}
	return err
}

// getBuildStatus gets the build status from one of the builds for a given resources
func (r *DrupalSiteReconciler) getBuildStatus(ctx context.Context, resource string, drp *webservicesv1a1.DrupalSite) (buildv1.BuildPhase, error) {
	buildList := &buildv1.BuildList{}
	buildLabels, err := metav1.LabelSelectorAsSelector(&metav1.LabelSelector{
		MatchLabels: map[string]string{"openshift.io/build-config.name": resource + nameVersionHash(drp)},
	})
	if err != nil {
		return "", newApplicationError(err, ErrFunctionDomain)
	}
	options := client.ListOptions{
		LabelSelector: buildLabels,
		Namespace:     drp.Namespace,
	}
	err = r.List(ctx, buildList, &options)
	if err != nil {
		return "", newApplicationError(err, ErrClientK8s)
	}
	// Check for one more build?
	if len(buildList.Items) > 0 {
		return buildList.Items[len(buildList.Items)-1].Status.Phase, nil
	}
	return "", newApplicationError(err, ErrClientK8s)
}

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

// isEasystartTaskRunCompleted checks if the easystart taskRun is successfully completed
func (r *DrupalSiteReconciler) isEasystartTaskRunCompleted(ctx context.Context, d *webservicesv1a1.DrupalSite) bool {
	easystartTaskRun := &pipelinev1.TaskRun{}
	err := r.Get(ctx, types.NamespacedName{Name: "easystart-" + d.Name, Namespace: d.Namespace}, easystartTaskRun)
	if err != nil {
		return false
	}
	// business logic, ie check "Succeeded"
	return easystartTaskRun.Status.GetCondition(apis.ConditionSucceeded).IsTrue()
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
// It also removes the site from the DrupalProjectConfig in case it was the primary site.
func (r *DrupalSiteReconciler) cleanupDrupalSite(ctx context.Context, log logr.Logger, drp *webservicesv1a1.DrupalSite, dpc *webservicesv1a1.DrupalProjectConfig) (ctrl.Result, error) {
	log.V(1).Info("Deleting DrupalSite")

	// Remove site from DrupalProjectConfig if it was the primary site
	if dpc != nil && dpc.Spec.PrimarySiteName == drp.Name {
		dpc.Spec.PrimarySiteName = ""
		if err := r.updateDrupalProjectConfigCR(ctx, log, dpc); err != nil {
			return ctrl.Result{}, err
		}
	}

	controllerutil.RemoveFinalizer(drp, finalizerStr)
	if err := r.ensureNoBackupSchedule(ctx, drp, log); err != nil {
		return ctrl.Result{}, err
	}
	return r.updateCRorFailReconcile(ctx, log, drp)
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
	// Set default value for DiskSize to 2000Mi
	if drp.Spec.Configuration.CloneFrom == "" && drp.Spec.Configuration.DiskSize == "" {
		drp.Spec.Configuration.DiskSize = "2000Mi"
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
		// The extraConfigurationRepo should be set in the clone site if defined in the source
		if sourceSite.Spec.Configuration.ExtraConfigurationRepo != "" && drp.Spec.Configuration.ExtraConfigurationRepo == "" {
			drp.Spec.Configuration.ExtraConfigurationRepo = sourceSite.Spec.Configuration.ExtraConfigurationRepo
		}
	}
	// Initialize 'spec.version.releaseSpec' if empty
	if len(drp.Spec.Version.ReleaseSpec) == 0 {
		switch {
		case strings.HasPrefix(drp.Spec.Version.Name, "v8"):
			drp.Spec.Version.ReleaseSpec = DefaultD8ReleaseSpec
		case strings.HasPrefix(drp.Spec.Version.Name, "v9.3-1"):
			drp.Spec.Version.ReleaseSpec = DefaultD93ReleaseSpec
		case strings.HasPrefix(drp.Spec.Version.Name, "v9.3-2"):
			drp.Spec.Version.ReleaseSpec = DefaultD932ReleaseSpec
		default:
			log.V(3).Info("Cannot set default ReleaseSpec for version " + drp.Spec.Version.Name)
		}
		update = true
	}
	return update, nil
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

//checkIfPrimaryDrupalSite updates the status of the current Drupalsite to show if it is the primary site according to the DrupalProjectConfig
func (r *DrupalSiteReconciler) checkIfPrimaryDrupalsite(ctx context.Context, drp *webservicesv1a1.DrupalSite, dpc *webservicesv1a1.DrupalProjectConfig) bool {
	if dpc == nil {
		return false
	}
	// We get the first DrupalProjectConfig in the Namespace, only one is expected per cluster!
	if drp.Name == dpc.Spec.PrimarySiteName && !drp.Status.IsPrimary {
		drp.Status.IsPrimary = true
		return true
	} else if drp.Name != dpc.Spec.PrimarySiteName && drp.Status.IsPrimary {
		drp.Status.IsPrimary = false
		return true
	}
	return false
}

func (r *DrupalSiteReconciler) getDeployConfigmap(ctx context.Context, d *webservicesv1a1.DrupalSite) (deploy appsv1.Deployment,
	cmPhp corev1.ConfigMap, cmNginxGlobal corev1.ConfigMap, cmSettings corev1.ConfigMap, cmPhpCli corev1.ConfigMap, err error) {
	err = r.Get(ctx, types.NamespacedName{Name: d.Name, Namespace: d.Namespace}, &deploy)
	if err != nil {
		return
	}
	err = r.Get(ctx, types.NamespacedName{Name: "php-fpm-" + d.Name, Namespace: d.Namespace}, &cmPhp)
	if err != nil {
		return
	}
	err = r.Get(ctx, types.NamespacedName{Name: "nginx-global-" + d.Name, Namespace: d.Namespace}, &cmNginxGlobal)
	if err != nil {
		return
	}
	err = r.Get(ctx, types.NamespacedName{Name: "site-settings-" + d.Name, Namespace: d.Namespace}, &cmSettings)
	if err != nil {
		return
	}
	err = r.Get(ctx, types.NamespacedName{Name: "php-cli-config-" + d.Name, Namespace: d.Namespace}, &cmSettings)
	return
}

// ensureDeploymentConfigmapHash ensures that the deployment has annotations with the content of each configmap.
// If the content of the configmaps changes, this will ensure that the deployemnt rolls out.
func (r *DrupalSiteReconciler) ensureDeploymentConfigmapHash(ctx context.Context, d *webservicesv1a1.DrupalSite, log logr.Logger) (requeue bool, transientErr reconcileError) {
	deploy, cmPhp, cmNginxGlobal, cmSettings, cmPhpCli, err := r.getDeployConfigmap(ctx, d)
	switch {
	case k8sapierrors.IsNotFound(err):
		return false, nil
	case err != nil:
		return false, newApplicationError(err, ErrClientK8s)
	}
	updateDeploymentAnnotations := func(deploy *appsv1.Deployment, d *webservicesv1a1.DrupalSite) error {
		hashPhp := md5.Sum([]byte(createKeyValuePairs(cmPhp.Data)))
		hashNginxGlobal := md5.Sum([]byte(createKeyValuePairs(cmNginxGlobal.Data)))
		hashSettings := md5.Sum([]byte(createKeyValuePairs(cmSettings.Data)))
		hashPhpCli := md5.Sum([]byte(createKeyValuePairs(cmPhpCli.Data)))

		deploy.Spec.Template.ObjectMeta.Annotations["phpfpm-configmap/hash"] = hex.EncodeToString(hashPhp[:])
		deploy.Spec.Template.ObjectMeta.Annotations["nginx-configmap/hash"] = hex.EncodeToString(hashNginxGlobal[:])
		deploy.Spec.Template.ObjectMeta.Annotations["settings.php-configmap/hash"] = hex.EncodeToString(hashSettings[:])
		deploy.Spec.Template.ObjectMeta.Annotations["php-cli-configmap/hash"] = hex.EncodeToString(hashPhpCli[:])
		return nil
	}
	_, err = controllerruntime.CreateOrUpdate(ctx, r.Client, &deploy, func() error {
		return updateDeploymentAnnotations(&deploy, d)
	})
	switch {
	case k8sapierrors.IsConflict(err):
		log.V(4).Info("Server deployment changed while reconciling. Requeuing.")
		return true, nil
	case err != nil:
		return false, newApplicationError(fmt.Errorf("failed to annotate deployment with configmap hashes: %w", err), ErrClientK8s)
	}
	return false, nil
}

// checkNewBackups returns the list of velero backups that exist for a given site
func (r *DrupalSiteReconciler) checkNewBackups(ctx context.Context, d *webservicesv1a1.DrupalSite, log logr.Logger) (backups []webservicesv1a1.Backup, reconcileErr reconcileError) {
	backupList := velerov1.BackupList{}
	backups = make([]webservicesv1a1.Backup, 0)
	hash := md5.Sum([]byte(d.Namespace))
	backupLabels, err := metav1.LabelSelectorAsSelector(&metav1.LabelSelector{
		MatchLabels: map[string]string{"drupal.webservices.cern.ch/projectHash": hex.EncodeToString(hash[:])},
	})
	if err != nil {
		reconcileErr = newApplicationError(err, ErrFunctionDomain)
		return
	}
	options := client.ListOptions{
		LabelSelector: backupLabels,
		Namespace:     VeleroNamespace,
	}
	err = r.List(ctx, &backupList, &options)
	switch {
	case err != nil:
		reconcileErr = newApplicationError(err, ErrClientK8s)
	case len(backupList.Items) == 0:
		log.V(3).Info("No backup found with given labels " + backupLabels.String())
	default:
		for i := range backupList.Items {
			if backupList.Items[i].Status.Phase == velerov1.BackupPhaseCompleted {
				backups = append(backups, webservicesv1a1.Backup{BackupName: backupList.Items[i].Name, Date: backupList.Items[i].Status.CompletionTimestamp, Expires: backupList.Items[i].Status.Expiration, DrupalSiteName: backupList.Items[i].Annotations["drupal.webservices.cern.ch/drupalSite"]})
			}
		}
	}
	return
}
