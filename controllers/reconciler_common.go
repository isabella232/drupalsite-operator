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
	"bytes"
	"context"
	"crypto/md5"
	"encoding/hex"
	"fmt"
	"io"
	"reflect"
	"time"

	"github.com/asaskevich/govalidator"
	"github.com/go-logr/logr"
	"github.com/operator-framework/operator-lib/status"
	velerov1 "github.com/vmware-tanzu/velero/pkg/apis/velero/v1"
	webservicesv1a1 "gitlab.cern.ch/drupal/paas/drupalsite-operator/api/v1alpha1"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	k8sapierrors "k8s.io/apimachinery/pkg/api/errors"
	k8sapiresource "k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

// Reconciler reconciles a DrupalSite object
type Reconciler struct {
	client.Client
	Log    logr.Logger
	Scheme *runtime.Scheme
}

type DeploymentConfig struct {
	replicas             int32
	phpResources         corev1.ResourceRequirements
	nginxResources       corev1.ResourceRequirements
	phpExporterResources corev1.ResourceRequirements
	webDAVResources      corev1.ResourceRequirements
	cronResources        corev1.ResourceRequirements
	drupalLogsResources  corev1.ResourceRequirements
}

func setReady(drp *webservicesv1a1.DrupalSite) (update bool) {
	return drp.Status.Conditions.SetCondition(status.Condition{
		Type:   "Ready",
		Status: "True",
	})
}

func setNotReady(drp *webservicesv1a1.DrupalSite, transientErr reconcileError) (update bool) {
	return setConditionStatus(drp, "Ready", false, transientErr, false)
}

func setInitialized(drp *webservicesv1a1.DrupalSite) (update bool) {
	return drp.Status.Conditions.SetCondition(status.Condition{
		Type:   "Initialized",
		Status: "True",
	})
}

func setNotInitialized(drp *webservicesv1a1.DrupalSite) (update bool) {
	return drp.Status.Conditions.SetCondition(status.Condition{
		Type:   "Initialized",
		Status: "False",
	})
}

func setErrorCondition(drp *webservicesv1a1.DrupalSite, err reconcileError) (update bool) {
	return setConditionStatus(drp, "Error", true, err, false)
}

func setConditionStatus(drp *webservicesv1a1.DrupalSite, conditionType status.ConditionType, statusFlag bool, err reconcileError, statusUnknown bool) (update bool) {
	statusStr := func() corev1.ConditionStatus {
		if statusUnknown {
			return corev1.ConditionUnknown
		}
		if statusFlag {
			return corev1.ConditionTrue
		} else {
			return corev1.ConditionFalse
		}
	}
	condition := func() status.Condition {
		if err != nil {
			return status.Condition{
				Type:    conditionType,
				Status:  statusStr(),
				Reason:  status.ConditionReason(err.Unwrap().Error()),
				Message: err.Error(),
			}
		}
		return status.Condition{
			Type:   conditionType,
			Status: statusStr(),
		}
	}
	return drp.Status.Conditions.SetCondition(condition())
}

// setDBUpdatesPending sets the 'DBUpdatesPending' status on the drupalSite object
func setDBUpdatesPending(drp *webservicesv1a1.DrupalSite, value corev1.ConditionStatus) (update bool) {
	return drp.Status.Conditions.SetCondition(status.Condition{
		Type:   "DBUpdatesPending",
		Status: value,
	})
}

// removeDBUpdatesPending removes the 'DBUpdatesPending' status on the drupalSite object
func removeDBUpdatesPending(drp *webservicesv1a1.DrupalSite) (update bool) {
	return drp.Status.Conditions.RemoveCondition("DBUpdatesPending")
}

// nameVersionHash returns a hash using the drupalSite name and version
func nameVersionHash(drp *webservicesv1a1.DrupalSite) string {
	hash := md5.Sum([]byte(drp.Name + releaseID(drp)))
	return hex.EncodeToString(hash[0:7])
}

// resourceList is a k8s API object representing the given amount of memory and CPU resources
func resourceList(memory, cpu string) (corev1.ResourceList, error) {
	memoryQ, err := k8sapiresource.ParseQuantity(memory)
	if err != nil {
		return nil, err
	}
	cpuQ, err := k8sapiresource.ParseQuantity(cpu)
	if err != nil {
		return nil, err
	}
	return corev1.ResourceList{
		"memory": memoryQ,
		"cpu":    cpuQ,
	}, nil
}

// resourceRequestLimit is a k8s API object representing the resource requests and limits given as strings
func ResourceRequestLimit(memReq, cpuReq, memLim, cpuLim string) (corev1.ResourceRequirements, error) {
	reqs, err := resourceList(memReq, cpuReq)
	if err != nil {
		return corev1.ResourceRequirements{}, err
	}
	lims, err := resourceList(memLim, cpuLim)
	if err != nil {
		return corev1.ResourceRequirements{}, err
	}
	return corev1.ResourceRequirements{
		Requests: reqs,
		Limits:   lims,
	}, nil
}

// reqLimDict returns the resource requests and limits for a given QoS class and container.
// TODO: this should be part of operator configuration, read from a YAML file with format
// defaultResources:
//   critical:
//     phpFpm:
//       resources:
//         # normal K8s req/lim
//     nginx:
//       # ...
//   standard:
//     # ...
//   eco:
//     # ...
func reqLimDict(container string, qosClass webservicesv1a1.QoSClass) (corev1.ResourceRequirements, error) {
	switch container {
	case "php-fpm":
		if qosClass == webservicesv1a1.QoSCritical {
			return ResourceRequestLimit("2500Mi", "1000m", "3Gi", "5000m")
		}
		if qosClass == webservicesv1a1.QoSTest {
			// Test sites should request much fewer resources, but they can still afford to consume more if available (low QoS)
			return ResourceRequestLimit("100Mi", "50m", "500Mi", "900m")
		}
		return ResourceRequestLimit("200Mi", "90m", "500Mi", "2000m")
	case "nginx":
		if qosClass == webservicesv1a1.QoSCritical {
			// We haven't seen any Nginx bottlenecks with critical sites so far
			return ResourceRequestLimit("20Mi", "60m", "50Mi", "1500m")
		}
		if qosClass == webservicesv1a1.QoSTest {
			return ResourceRequestLimit("5Mi", "20m", "20Mi", "400m")
		}
		return ResourceRequestLimit("10Mi", "30m", "20Mi", "700m")
	case "php-fpm-exporter":
		return ResourceRequestLimit("15Mi", "4m", "25Mi", "40m")
	case "webdav":
		// Webdav has very few requests (low QoS) anyway, so there's no need to change for test sites so far
		// WebDAV workloads are very bursty and they need a lot of CPU to process, therefore giving very high spread
		return ResourceRequestLimit("10Mi", "20m", "100Mi", "500m")
	case "cron":
		return ResourceRequestLimit("10Mi", "10m", "20Mi", "80m")
	case "drupal-logs":
		return ResourceRequestLimit("10Mi", "4m", "15Mi", "15m")
	}
	return corev1.ResourceRequirements{
		Requests: corev1.ResourceList{},
		Limits:   corev1.ResourceList{},
	}, newApplicationError(fmt.Errorf("undefined keys for the reqLimDict function"), ErrFunctionDomain)
}

// generateRandomPassword generates a random password of length 10 by creating a hash of the current time
func generateRandomPassword() string {
	hash := md5.Sum([]byte(time.Now().String()))
	return hex.EncodeToString(hash[:])[0:10]
}

func createKeyValuePairs(m map[string]string) string {
	b := new(bytes.Buffer)
	for key, value := range m {
		fmt.Fprintf(b, "%s=\"%s\"\n", key, value)
	}
	return b.String()
}

// checkIfEnvVarExists checks if a given EnvVar array has the specific variable present or not
func checkIfEnvVarExists(envVarArray []corev1.EnvVar, envVarName string) (flag bool) {
	for _, item := range envVarArray {
		if item.Name == envVarName {
			return true
		}
	}
	return false
}

// checkIfEnvFromSourceExists checks if a given EnvFromSource array has the specific source variable present or not
func checkIfEnvFromSourceExists(envFromSourceArray []corev1.EnvFromSource, envVarName string) (flag bool) {
	for _, item := range envFromSourceArray {
		if item.SecretRef != nil && item.SecretRef.Name == envVarName {
			return true
		}
	}
	return false
}

// generateScheduleName generates a schedule name for the site by making sure the max length of it is 63 characters.
// the schedule name is added as label to velero backups and labels need to abide by RFC 1123
// https://kubernetes.io/docs/concepts/overview/working-with-objects/names/#dns-label-names
func generateScheduleName(namespace string, siteName string) string {
	if len(namespace) > 57 {
		namespace = namespace[0:57]
	}
	siteNameHash := md5.Sum([]byte(siteName))
	return namespace + "-" + hex.EncodeToString(siteNameHash[:])[0:4]
}

// getGracePeriodMinutesForPodToStartDuringUpgrade returns the time in minutes to wait for the new version of Drupal pod to start during version upgrade
func getGracePeriodMinutesForPodToStartDuringUpgrade(d *webservicesv1a1.DrupalSite) float64 {
	return 10 // 10minutes
}

// fetchDrupalSitesInNamespace fetches all the Drupalsites in a given namespace
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

// getenvOrDie checks for the given variable in the environm
// addGitlabWebhookToStatus adds the Gitlab webhook URL for the s2i (extraconfig) buildconfig to the DrupalSite status
// by querying the K8s API for API Server & Gitlab webhook trigger secret value
func addGitlabWebhookToStatus(ctx context.Context, drp *webservicesv1a1.DrupalSite) bool {
	// Fetch the gitlab webhook trigger secret value
	gitlabTriggerSecret := "gitlab-trigger-secret-" + drp.Name
	webHookUrl := "https://api." + ClusterName + ".okd.cern.ch:443/apis/build.openshift.io/v1/namespaces/" + drp.Namespace + "/buildconfigs/" + "sitebuilder-s2i-" + drp.Name + "/webhooks/" + gitlabTriggerSecret + "/gitlab"
	if drp.Status.GitlabWebhookURL != webHookUrl {
		drp.Status.GitlabWebhookURL = webHookUrl
		return true
	}
	return false
}

//validateSpec validates the spec against the DrupalSiteSpec definition
func validateSpec(drpSpec webservicesv1a1.DrupalSiteSpec) reconcileError {
	_, err := govalidator.ValidateStruct(drpSpec)
	if err != nil {
		return newApplicationError(err, ErrInvalidSpec)
	}
	return nil
}

// labelsForDrupalSite returns the labels for selecting the resources
// belonging to the given drupalSite CR name.
func labelsForDrupalSite(name string) map[string]string {
	return map[string]string{"drupalSite": name}
}

// releaseID is the image tag to use depending on the version and releaseSpec
func releaseID(d *webservicesv1a1.DrupalSite) string {
	return d.Spec.Version.Name + "-" + d.Spec.Version.ReleaseSpec
}

// sitebuilderImageRefToUse returns which base image to use, depending on whether the field `ExtraConfigurationRepo` is set.
// If yes, the S2I buildconfig will be used; sitebuilderImageRefToUse returns the output of imageStreamForDrupalSiteBuilderS2I().
// Otherwise, returns the sitebuilder base
func sitebuilderImageRefToUse(d *webservicesv1a1.DrupalSite, releaseID string) corev1.ObjectReference {
	if len(d.Spec.Configuration.ExtraConfigurationRepo) > 0 {
		return corev1.ObjectReference{
			Kind: "ImageStreamTag",
			Name: "image-registry.openshift-image-registry.svc:5000/" + d.Namespace + "/sitebuilder-s2i-" + d.Name + ":" + releaseID,
		}
	}
	return corev1.ObjectReference{
		Kind: "DockerImage",
		Name: SiteBuilderImage + ":" + releaseID,
	}
}

// addOwnerRefToObject appends the desired OwnerReference to the object
func addOwnerRefToObject(obj metav1.Object, ownerRef metav1.OwnerReference) {
	// If Owner already in object, we ignore
	for _, o := range obj.GetOwnerReferences() {
		if o.UID == ownerRef.UID {
			return
		}
	}
	obj.SetOwnerReferences(append(obj.GetOwnerReferences(), ownerRef))
}

// asOwner returns an OwnerReference set as the memcached CR
func asOwner(d *webservicesv1a1.DrupalSite) metav1.OwnerReference {
	trueVar := true
	return metav1.OwnerReference{
		APIVersion: d.APIVersion,
		Kind:       d.Kind,
		Name:       d.Name,
		UID:        d.UID,
		Controller: &trueVar,
	}
}

// siteInstallJobForDrupalSite outputs the command needed for jobForDrupalSiteDrush
func siteInstallJobForDrupalSite() []string {
	// return []string{"sh", "-c", "echo"}
	return []string{"/operations/ensure-site-install.sh"}
}

// enableSiteMaintenanceModeCommandForDrupalSite outputs the command needed to enable maintenance mode
func enableSiteMaintenanceModeCommandForDrupalSite() []string {
	return []string{"/operations/enable-maintenance-mode.sh"}
}

// disableSiteMaintenanceModeCommandForDrupalSite outputs the command needed to disable maintenance mode
func disableSiteMaintenanceModeCommandForDrupalSite() []string {
	return []string{"/operations/disable-maintenance-mode.sh"}
}

// checkUpdbStatus outputs the command needed to check if a database update is required
func checkUpdbStatus() []string {
	return []string{"/operations/check-updb-status.sh"}
}

// runUpDBCommand outputs the command needed to update the database in drupal
func runUpDBCommand() []string {
	return []string{"/operations/run-updb.sh"}
}

// takeBackup outputs the command need to take the database backup to a given filename
func takeBackup(filepath string) []string {
	return []string{"/operations/database-backup.sh", "-p", filepath}
}

// restoreBackup outputs the command need to restore the database backup from a given filename
func restoreBackup(filename string) []string {
	return []string{"/operations/database-restore.sh", "-f", filename}
}

// cloneSource outputs the command need to clone a drupal site
func cloneSource(filepath string) []string {
	return []string{"/operations/clone.sh", "-p", filepath}
}

// encryptBasicAuthPassword encrypts a password for basic authentication
// Since we are using SabreDAV, the specific format to follow: https://sabre.io/dav/authentication/#using-the-file-backend
func encryptBasicAuthPassword(password string) string {
	webdavHashPrefix := webDAVDefaultLogin + ":SabreDAV:"
	hashedPassword := md5.Sum([]byte(webdavHashPrefix + password))
	return webdavHashPrefix + hex.EncodeToString(hashedPassword[:])
}

// checkIfSiteIsInstalled outputs the command to check if a site is initialized or not
func checkIfSiteIsInstalled() []string {
	return []string{"/operations/check-if-installed.sh"}
}

// cacheReload outputs the command to reload cache on the drupalSite
func cacheReload() []string {
	return []string{"/operations/clear-cache.sh"}
}

// syncDrupalFilesToEmptydir outputs the command to sync the files from /app to the emptyDir
func syncDrupalFilesToEmptydir() []string {
	return []string{"/operations/sync-drupal-emptydir.sh"}
}

// tailDrupalLogs outputs the command to tail the drupal log file
func tailDrupalLogs() []string {
	return []string{"/operations/tail-drupal-logs.sh"}
}

// customProbe outputs the command to check the /user/login
func customProbe(probe string) []string {
	return []string{"/operations/probe-site.sh", "-p", probe}
}

// startupProbe outputs the command to check the /_site/_php-fpm-status
func startupProbe() []string {
	return []string{"/operations/startup-probe-site.sh"}
}

// backupListUpdateNeeded tells whether two arrays of velero Backups elements are the same or not.
// A nil argument is equivalent to an empty slice.
func backupListUpdateNeeded(veleroBackupsList []velerov1.Backup, statusBackupsList []webservicesv1a1.Backup) bool {
	if len(veleroBackupsList) != len(statusBackupsList) {
		return true
	}
	for i, v := range veleroBackupsList {
		if v.Name != statusBackupsList[i].BackupName {
			return true
		}
	}
	return false
}

// expectedDeploymentReplicas calculates expected replicas of deployment
func expectedDeploymentReplicas(currentnamespace *corev1.Namespace, qosClass webservicesv1a1.QoSClass) (int32, error) {
	_, isBlockedTimestampAnnotationSet := currentnamespace.Annotations["blocked.webservices.cern.ch/blocked-timestamp"]
	_, isBlockedReasonAnnotationSet := currentnamespace.Annotations["blocked.webservices.cern.ch/reason"]
	blocked := isBlockedTimestampAnnotationSet && isBlockedReasonAnnotationSet
	notBlocked := !isBlockedTimestampAnnotationSet && !isBlockedReasonAnnotationSet
	switch {
	case !blocked && !notBlocked:
		return 0, fmt.Errorf("both annotations blocked.webservices.cern.ch/blocked-timestamp and blocked.webservices.cern.ch/reason should be added/removed to block/unblock")
	case blocked:
		return 0, nil
	default:
		if qosClass == webservicesv1a1.QoSCritical {
			return 3, nil
		}
		return 1, nil
	}
}

// containerExists checks if a container exists on the deployment
// if it doesn't exists, it adds it
func containerExists(name string, currentobject *appsv1.Deployment) {
	containerExists := false
	for _, container := range currentobject.Spec.Template.Spec.Containers {
		if container.Name == name {
			containerExists = true
			break
		}
	}
	if !containerExists {
		currentobject.Spec.Template.Spec.Containers = append(currentobject.Spec.Template.Spec.Containers, corev1.Container{Name: name})
	}
}

// getDeploymentConfiguration precalculates all the configuration that the server deployment needs, including:
// pod replicas, resource req/lim
// NOTE: this includes the default resource limits for PHP
func (r *Reconciler) getDeploymentConfiguration(ctx context.Context, drupalSite *webservicesv1a1.DrupalSite) (config DeploymentConfig, requeue bool, updateStatus bool, reconcileErr reconcileError) {
	config = DeploymentConfig{}
	requeue = false
	updateStatus = false

	// Get replicas
	namespace := &corev1.Namespace{}
	if err := r.Get(ctx, types.NamespacedName{Name: drupalSite.Namespace}, namespace); err != nil {
		switch {
		case k8sapierrors.IsNotFound(err):
			return DeploymentConfig{}, true, false, nil
		default:
			return DeploymentConfig{}, false, false, newApplicationError(err, ErrClientK8s)
		}
	}
	replicas, err := expectedDeploymentReplicas(namespace, drupalSite.Spec.QoSClass)
	if err != nil {
		return DeploymentConfig{}, false, false, newApplicationError(err, ErrInvalidSpec)
	}
	if drupalSite.Status.ExpectedDeploymentReplicas == nil || *drupalSite.Status.ExpectedDeploymentReplicas != replicas {
		drupalSite.Status.ExpectedDeploymentReplicas = &replicas
		updateStatus = true
	}

	nginxResources, err := reqLimDict("nginx", drupalSite.Spec.QoSClass)
	if err != nil {
		reconcileErr = newApplicationError(err, ErrFunctionDomain)
	}
	phpExporterResources, err := reqLimDict("php-fpm-exporter", drupalSite.Spec.QoSClass)
	if err != nil {
		reconcileErr = newApplicationError(err, ErrFunctionDomain)
	}
	phpResources, err := reqLimDict("php-fpm", drupalSite.Spec.QoSClass)
	if err != nil {
		reconcileErr = newApplicationError(err, ErrFunctionDomain)
	}
	//TODO: Check best resource consumption
	webDAVResources, err := reqLimDict("webdav", drupalSite.Spec.QoSClass)
	if err != nil {
		reconcileErr = newApplicationError(err, ErrFunctionDomain)
	}
	cronResources, err := reqLimDict("cron", drupalSite.Spec.QoSClass)
	drupalLogsResources, err := reqLimDict("drupal-logs", drupalSite.Spec.QoSClass)
	if err != nil {
		reconcileErr = newApplicationError(err, ErrFunctionDomain)
	}
	if reconcileErr != nil {
		return
	}

	// Get config override (currently only PHP resources)

	configOverride, reconcileErr := r.getConfigOverride(ctx, drupalSite)
	if reconcileErr != nil {
		return
	}
	if configOverride != nil {
		if !reflect.DeepEqual(configOverride.Php.Resources, corev1.ResourceRequirements{}) {
			phpResources = configOverride.Php.Resources
		}
		if !reflect.DeepEqual(configOverride.Nginx.Resources, corev1.ResourceRequirements{}) {
			nginxResources = configOverride.Nginx.Resources
		}
		if !reflect.DeepEqual(configOverride.Webdav.Resources, corev1.ResourceRequirements{}) {
			webDAVResources = configOverride.Webdav.Resources
		}
		if !reflect.DeepEqual(configOverride.PhpExporter.Resources, corev1.ResourceRequirements{}) {
			phpExporterResources = configOverride.PhpExporter.Resources
		}
		if !reflect.DeepEqual(configOverride.Cron.Resources, corev1.ResourceRequirements{}) {
			cronResources = configOverride.Cron.Resources
		}
		if !reflect.DeepEqual(configOverride.DrupalLogs.Resources, corev1.ResourceRequirements{}) {
			drupalLogsResources = configOverride.DrupalLogs.Resources
		}
	}

	config = DeploymentConfig{replicas: replicas,
		phpResources: phpResources, nginxResources: nginxResources, phpExporterResources: phpExporterResources, webDAVResources: webDAVResources, cronResources: cronResources, drupalLogsResources: drupalLogsResources,
	}
	return
}

// updateCRorFailReconcile tries to update the Custom Resource and logs any error
func (r *Reconciler) updateCRorFailReconcile(ctx context.Context, log logr.Logger, drp *webservicesv1a1.DrupalSite) (
	reconcile.Result, error) {
	if err := r.Update(ctx, drp); err != nil {
		if k8sapierrors.IsConflict(err) {
			log.V(4).Info("DrupalSite changed while reconciling. Requeuing.")
			return reconcile.Result{Requeue: true}, nil
		}
		log.Error(err, fmt.Sprintf("%v failed to update the application", ErrClientK8s))
		return reconcile.Result{}, err
	}
	return reconcile.Result{}, nil
}

// updateCRStatusOrFailReconcile tries to update the Custom Resource Status and logs any error
func (r *Reconciler) updateCRStatusOrFailReconcile(ctx context.Context, log logr.Logger, drp *webservicesv1a1.DrupalSite) (
	reconcile.Result, error) {
	if err := r.Status().Update(ctx, drp); err != nil {
		if k8sapierrors.IsConflict(err) {
			log.V(4).Info("DrupalSite.Status changed while reconciling. Requeuing.")
			return reconcile.Result{Requeue: true}, nil
		}
		log.Error(err, fmt.Sprintf("%v failed to update the application status", ErrClientK8s))
		return reconcile.Result{}, err
	}
	return reconcile.Result{}, nil
}

// execToServerPod executes a command to the first running server pod of the Drupal site.
//
// Commands are interpreted similar to how kubectl does it, eg to do "drush cr" either of these will work:
// - "drush", "cr"
// - "sh", "-c", "drush cr"
// The last syntax allows passing an entire bash script as a string.
//
// Example:
// ````
//	sout, serr, err := r.execToServerPod(ctx, drp, "php-fpm", nil, "sh", "-c", "drush version; ls")
//	sout, serr, err := r.execToServerPod(ctx, drp, "php-fpm", nil, "drush", "version")
//	if err != nil {
//		log.Error(err, "Error while exec into pod")
//	}
//	log.Info("EXEC", "stdout", sout, "stderr", serr)
// ````
func (r *Reconciler) execToServerPod(ctx context.Context, d *webservicesv1a1.DrupalSite, containerName string, stdin io.Reader, command ...string) (stdout string, stderr string, err error) {
	pod, err := r.getRunningPodForVersion(ctx, d, releaseID(d))
	if err != nil {
		return "", "", err
	}
	return execToPodThroughAPI(containerName, pod.Name, d.Namespace, stdin, command...)
}

// getRunningPodForVersion fetches the list of the running pods for the current deployment and returns the first one from the list
func (r *Reconciler) getRunningPodForVersion(ctx context.Context, d *webservicesv1a1.DrupalSite, releaseID string) (corev1.Pod, reconcileError) {
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
			if v.Status.Phase == corev1.PodRunning {
				return v, nil
			} else {
				return v, newApplicationError(err, ErrPodNotRunning)
			}
		}
	}
	// iterate through the list and return the first pod that has the status condition ready
	return corev1.Pod{}, newApplicationError(err, ErrClientK8s)
}

// execToServerPodErrOnStder works like `execToServerPod`, but puts the contents of stderr in the error, if not empty
// If any error while trying to exec, the function returns a ErrClientK8s
func (r *Reconciler) execToServerPodErrOnStderr(ctx context.Context, d *webservicesv1a1.DrupalSite, containerName string, stdin io.Reader, command ...string) (stdout string, err error) {
	stdout, stderr, err := r.execToServerPod(ctx, d, containerName, stdin, command...)
	if err != nil {
		log.Error(err, fmt.Sprintf("%v failed to run exec", ErrClientK8s))
		return "", ErrClientK8s
	}
	if stderr != "" {
		return "", fmt.Errorf("STDERR: %s", stderr)
	}
	return stdout, nil
}

func (r *Reconciler) getConfigOverride(ctx context.Context, drp *webservicesv1a1.DrupalSite) (*webservicesv1a1.DrupalSiteConfigOverrideSpec, reconcileError) {
	configOverride := &webservicesv1a1.DrupalSiteConfigOverride{}
	err := r.Get(ctx, types.NamespacedName{Name: drp.Name, Namespace: drp.Namespace}, configOverride)
	switch {
	case k8sapierrors.IsNotFound(err):
		return nil, nil
	case err != nil:
		return nil, newApplicationError(err, ErrClientK8s)
	}
	return &configOverride.Spec, nil
}
