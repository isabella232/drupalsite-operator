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
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/go-logr/logr"
	k8sapierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"github.com/google/go-containerregistry/pkg/crane"

	"gitlab.cern.ch/drupal/paas/drupalsite-operator/api/v1alpha1"
	drupalwebservicesv1alpha1 "gitlab.cern.ch/drupal/paas/drupalsite-operator/api/v1alpha1"
)

const layout = "Jan 2, 2006 at 3:04pm (UTC)"

var (
	log = logf.Log.WithName("controller_supported_drupal_versions")
)

// SupportedDrupalVersionsReconciler reconciles a SupportedDrupalVersions object
type SupportedDrupalVersionsReconciler struct {
	client.Client
	Scheme *runtime.Scheme
	Log    logr.Logger
}

//+kubebuilder:rbac:groups=drupal.webservices.cern.ch,resources=supporteddrupalversions,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=drupal.webservices.cern.ch,resources=supporteddrupalversions/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=drupal.webservices.cern.ch,resources=supporteddrupalversions/finalizers,verbs=update

// SetupWithManager adds a manager which watches the resources
func (r *SupportedDrupalVersionsReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&drupalwebservicesv1alpha1.SupportedDrupalVersions{}).
		Complete(r)
}

// SetupWithManager sets up the controller with the Manager.
func (r *SupportedDrupalVersionsReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := r.Log.WithValues("Request.Namespace", req.NamespacedName, "Request.Name", req.Name)
	log.V(1).Info("Updating SupportedDrupalVersions resource")

	drupalVersionsList := &drupalwebservicesv1alpha1.SupportedDrupalVersionsList{}
	err := r.Client.List(ctx, drupalVersionsList)
	if err != nil {
		return ctrl.Result{}, err
	}

	if len(drupalVersionsList.Items) == 0 {
		err := newApplicationError(errors.New("fetching supported-drupal-versions failed"), ErrSupportedDrupalVersionsNone)
		log.Error(err, "There isn't any SupportedDrupalVersions resource")
		return ctrl.Result{}, err
	}

	// We only expect exactly one SupportedDrupalVersions resource in the cluster
	drupalVersions := drupalVersionsList.Items[0]
	if len(drupalVersionsList.Items) > 1 {
		log.V(1).Info(fmt.Sprintf("Note: We support only 1 SupportedDrupalVersions resource, but currently there are %d", len(drupalVersionsList.Items)))
		for _, item := range drupalVersionsList.Items {
			if item.Name == "supported-drupal-versions" {
				drupalVersions = item
			}
		}
	}

	// Get all registry tags of SiteBuilderImage
	registryTags, err := getRegistryTags(SiteBuilderImage)
	if err != nil {
		log.Error(err, fmt.Sprintf("Failed to get tags of %s", SiteBuilderImage))
		return reconcile.Result{}, err
	}
	// Parse registry tags and keep only v*-RELEASE-* tags
	registryDrupalVersions, err := parseRegistryTags(registryTags)
	if err != nil {
		log.Error(err, fmt.Sprintf("Failed to parse registry tags"))
		return reconcile.Result{}, err
	}
	// Get the most recent tag for each v* version
	registryDrupalVersionsLatestRelease := getLatestRelease(registryDrupalVersions)

	if drupalVersionsEqual(drupalVersions.Status.AvailableVersions, registryDrupalVersionsLatestRelease) && len(drupalVersions.Spec.Blacklist) == 0 {
		return ctrl.Result{RequeueAfter: time.Hour}, nil
	}
	// Update SupportedDrupalVersion resource with the latest release for each v* version
	updateVersions(&drupalVersions, registryDrupalVersionsLatestRelease)

	if err := r.Status().Update(ctx, &drupalVersions); err != nil {
		if err := r.Status().Update(ctx, &drupalVersions); err != nil {
			if k8sapierrors.IsConflict(err) {
				log.V(4).Info("Object changed while reconciling. Requeuing.")
				return reconcile.Result{Requeue: true}, nil
			}
			log.Error(err, fmt.Sprintf("%v failed to update the application status", ErrClientK8s))
			return reconcile.Result{}, err
		}
	}
	return ctrl.Result{RequeueAfter: time.Hour}, nil
}

// updateVersions updates the status with the latest versions
func updateVersions(drupalVersion *drupalwebservicesv1alpha1.SupportedDrupalVersions, versionsLatestRelease map[string]string) {
	drupalVersion.Status.AvailableVersions = nil
	for versionName, releaseSpec := range versionsLatestRelease {
		if !find(drupalVersion.Spec.Blacklist, versionName) {
			drupalVersion.Status.AvailableVersions = append(drupalVersion.Status.AvailableVersions, drupalwebservicesv1alpha1.DrupalVersion{
				Name: versionName,
				ReleaseSpec: drupalwebservicesv1alpha1.ReleaseSpec{
					LatestReleaseSpec: releaseSpec,
				},
			})
		}
	}
}

// drupalVersionsEqual compares the status of SupportedDrupalVersions resource with
// the versions from SiteBuilderImage
func drupalVersionsEqual(availableVersions []v1alpha1.DrupalVersion, versionsLatestRelease map[string]string) bool {
	if len(versionsLatestRelease) != len(availableVersions) {
		return false
	}
	availableVersionNames := []string{}
	for _, availableVersion := range availableVersions {
		availableVersionNames = append(availableVersionNames, availableVersion.Name)
	}
	for versionName, releaseSpec := range versionsLatestRelease {
		if !find(availableVersionNames, versionName) {
			return false
		}
		for _, availableVersion := range availableVersions {
			if versionName == availableVersion.Name {
				if releaseSpec != availableVersion.ReleaseSpec.LatestReleaseSpec {
					return false
				}
			}
		}
	}
	return true
}

// getRegistryTags returns the tags of SiteBuilderImage
func getRegistryTags(siteBuilderImage string) ([]string, error) {
	tags, err := crane.ListTags(siteBuilderImage)
	if err != nil {
		return tags, err
	}

	return tags, nil
}

// parseRegistryTags parses the tags that match the pattern ^(v.*)-(RELEASE.*)$
func parseRegistryTags(tags []string) (map[string][]string, error) {
	versions := make(map[string][]string)
	re, err := regexp.Compile("^(v.*)-(RELEASE.*)$")
	if err != nil {
		return versions, err
	}

	for _, tag := range tags {
		version := re.FindStringSubmatch(tag)
		if version != nil {
			versionName := version[1]
			versionReleaseSpec := version[2]
			if obj, exists := versions[versionName]; exists {
				versions[versionName] = append(obj, versionReleaseSpec)
			} else {
				versions[versionName] = []string{versionReleaseSpec}
			}
		}
	}
	return versions, nil
}

// getLatestRelease filters the latest release for each version
func getLatestRelease(versions map[string][]string) map[string]string {
	versionsLatestRelease := make(map[string]string)
	re := regexp.MustCompile("^RELEASE-(.*)$")
	for versionName, releaseSpecs := range versions {
		var releaseSpecsTime []time.Time
		for _, releaseSpec := range releaseSpecs {
			timestampString := re.FindStringSubmatch(releaseSpec)[1]
			timestampTime, _ := time.Parse(layout, timestampString)
			releaseSpecsTime = append(releaseSpecsTime, timestampTime)
		}
		sort.Slice(releaseSpecsTime, func(i, j int) bool {
			return releaseSpecsTime[i].After(releaseSpecsTime[j])
		})
		versionsLatestRelease[versionName] = strings.Join([]string{"RELEASE", releaseSpecsTime[0].Format(layout)}, "-")
	}

	return versionsLatestRelease
}

// Checks if a string exists in a slice
func find(slice []string, val string) bool {
	for _, item := range slice {
		if item == val {
			return true
		}
	}
	return false
}
