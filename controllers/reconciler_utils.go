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
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"time"

	"github.com/go-logr/logr"
	buildv1 "github.com/openshift/api/build/v1"
	"github.com/operator-framework/operator-lib/status"
	webservicesv1a1 "gitlab.cern.ch/drupal/paas/drupalsite-operator/api/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	k8sapierrors "k8s.io/apimachinery/pkg/api/errors"
	k8sapiresource "k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

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

// setCriticalSiteAnnotation sets the 'isCriticalSite' annotation on the drupalSite object
func setCriticalSiteAnnotation(drp *webservicesv1a1.DrupalSite) (update bool) {
	if len(drp.Annotations) == 0 {
		drp.Annotations = map[string]string{}
	}
	if drp.Annotations["isCriticalSite"] == "true" {
		return false
	}
	drp.Annotations["isCriticalSite"] = "true"
	return true
}

// unsetCriticalSiteAnnotation unsets the 'isCriticalSite' annotation on the drupalSite object
func unsetCriticalSiteAnnotation(drp *webservicesv1a1.DrupalSite) (update bool) {
	if len(drp.Annotations) == 0 {
		drp.Annotations = map[string]string{}
	}
	if drp.Annotations["isCriticalSite"] == "false" {
		return false
	}
	drp.Annotations["isCriticalSite"] = "false"
	return true
}

// updateCRorFailReconcile tries to update the Custom Resource and logs any error
func (r *DrupalSiteReconciler) updateCRorFailReconcile(ctx context.Context, log logr.Logger, drp *webservicesv1a1.DrupalSite) (
	reconcile.Result, error) {
	if err := r.Update(ctx, drp); err != nil {
		if k8sapierrors.IsConflict(err) {
			log.V(4).Info("Object changed while reconciling. Requeuing.")
			return reconcile.Result{Requeue: true}, nil
		}
		log.Error(err, fmt.Sprintf("%v failed to update the application", ErrClientK8s))
		return reconcile.Result{}, err
	}
	return reconcile.Result{}, nil
}

// updateCRStatusOrFailReconcile tries to update the Custom Resource Status and logs any error
func (r *DrupalSiteReconciler) updateCRStatusOrFailReconcile(ctx context.Context, log logr.Logger, drp *webservicesv1a1.DrupalSite) (
	reconcile.Result, error) {
	if err := r.Status().Update(ctx, drp); err != nil {
		if k8sapierrors.IsConflict(err) {
			log.V(4).Info("Object changed while reconciling. Requeuing.")
			return reconcile.Result{Requeue: true}, nil
		}
		log.Error(err, fmt.Sprintf("%v failed to update the application status", ErrClientK8s))
		return reconcile.Result{}, err
	}
	return reconcile.Result{}, nil
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

// ------ POTENTIALLY UNUSED FUNCTIONS -------

// resourceLimit is a k8s API object representing the resource limits given as strings. The requests are defaulted to the limits.
func resourceLimit(memLim, cpuLim string) (corev1.ResourceRequirements, error) {
	lims, err := resourceList(memLim, cpuLim)
	if err != nil {
		return corev1.ResourceRequirements{}, err
	}
	return corev1.ResourceRequirements{Limits: lims}, nil
}

// getPodForVersion fetches the list of the pods for the current deployment and returns the first one from the list
func (r *DrupalSiteReconciler) getPodForVersion(ctx context.Context, d *webservicesv1a1.DrupalSite, releaseID string) (corev1.Pod, reconcileError) {
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

// getSecretDataDecoded fetches the given secret and decodes the data for the given string.
// Returns nil in case the secret isn't found or the data can't be base64-decoded.
func (r *DrupalSiteReconciler) getSecretDataDecoded(ctx context.Context, name, namespace string, keys []string) map[string]string {
	secret := &corev1.Secret{}
	err := r.Get(ctx, types.NamespacedName{Name: name, Namespace: namespace}, secret)
	if err != nil {
		return nil
	}
	data := make(map[string]string, len(keys))
	for _, key := range keys {
		val, err := base64.URLEncoding.DecodeString(string(secret.Data[key]))
		if err != nil {
			return nil
		}
		data[key] = string(val)
	}
	return data
}

// generateWebDAVpassword generates the password for WebDAV
func generateWebDAVpassword() string {
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
