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
	"time"

	"github.com/asaskevich/govalidator"
	"github.com/go-logr/logr"
	"github.com/operator-framework/operator-lib/status"
	webservicesv1a1 "gitlab.cern.ch/drupal/paas/drupalsite-operator/api/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	k8sapierrors "k8s.io/apimachinery/pkg/api/errors"
	k8sapiresource "k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
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

// setUpdateInProgress sets the 'updateInProgress' annotation on the drupalSite object
func setUpdateInProgress(drp *webservicesv1a1.DrupalSite) bool {
	if len(drp.Annotations) == 0 {
		drp.Annotations = map[string]string{}
	}
	if drp.Annotations["updateInProgress"] == "true" {
		return false
	}
	drp.Annotations["updateInProgress"] = "true"
	return true
}

// unsetUpdateInProgress removes the 'updateInProgress' annotation on the drupalSite object
func unsetUpdateInProgress(drp *webservicesv1a1.DrupalSite) bool {
	if len(drp.Annotations) != 0 {
		_, isSet := drp.Annotations["updateInProgress"]
		if isSet {
			delete(drp.Annotations, "updateInProgress")
			return true
		}
		return false
	}
	return false
}

// setDBUpdatesPending sets the 'DBUpdatesPending' status on the drupalSite object
func setDBUpdatesPending(drp *webservicesv1a1.DrupalSite) (update bool) {
	return drp.Status.Conditions.SetCondition(status.Condition{
		Type:   "DBUpdatesPending",
		Status: "True",
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
	webHookUrl := "https://api." + ClusterName + ".okd.cern.ch:443/apis/build.openshift.io/v1/namespaces/" + drp.Namespace + "/buildconfigs/" + "sitebuilder-s2i-" + nameVersionHash(drp) + "/webhooks/" + gitlabTriggerSecret + "/gitlab"
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

// getPodForVersion fetches the list of the pods for the current deployment and returns the first one from the list
func (r *Reconciler) getPodForVersion(ctx context.Context, d *webservicesv1a1.DrupalSite, releaseID string) (corev1.Pod, reconcileError) {
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
