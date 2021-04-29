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
	"io"
	"io/ioutil"
	"net/url"
	"os/exec"
	"path"
	"strconv"

	"github.com/go-logr/logr"
	buildv1 "github.com/openshift/api/build/v1"
	imagev1 "github.com/openshift/api/image/v1"
	routev1 "github.com/openshift/api/route/v1"

	dbodv1a1 "gitlab.cern.ch/drupal/paas/dbod-operator/api/v1alpha1"
	webservicesv1a1 "gitlab.cern.ch/drupal/paas/drupalsite-operator/api/v1alpha1"
	authz "gitlab.cern.ch/paas-tools/operators/authz-operator/api/v1alpha1"
	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	k8sapierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/utils/pointer"
	controllerruntime "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

var (
	// BuildResources are the resource requests/limits for the image builds. Set during initEnv()
	BuildResources corev1.ResourceRequirements
)

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
func (r *DrupalSiteReconciler) execToServerPod(ctx context.Context, d *webservicesv1a1.DrupalSite, containerName string, stdin io.Reader, command ...string) (stdout string, stderr string, err error) {
	pod, err := r.getRunningPod(ctx, d)
	if err != nil {
		return "", "", err
	}
	return execToPodThroughAPI(containerName, pod.Name, d.Namespace, stdin, command...)
}

// getRunningPod fetches the list of the running pods for the current deployment and returns the first one from the list
func (r *DrupalSiteReconciler) getRunningPod(ctx context.Context, d *webservicesv1a1.DrupalSite) (corev1.Pod, error) {
	podList := corev1.PodList{}
	podLabels, err := metav1.LabelSelectorAsSelector(&metav1.LabelSelector{
		MatchLabels: map[string]string{"drupalSite": d.Name, "app": "drupal"},
	})
	if err != nil {
		return corev1.Pod{}, err
	}
	options := client.ListOptions{
		LabelSelector: podLabels,
		Namespace:     d.Namespace,
	}
	err = r.List(ctx, &podList, &options)
	if err != nil {
		return corev1.Pod{}, err
	}
	if len(podList.Items) == 0 {
		return corev1.Pod{}, errors.New("Can't find pod with these labels")
	}
	for _, v := range podList.Items {
		if v.Status.Phase == corev1.PodRunning && v.Annotations["drupalVersion"] == d.Spec.DrupalVersion {
			return v, nil
		}
	}
	// iterate through the list and return the first pod that has the status condition ready
	return corev1.Pod{}, errors.New("Can't find a pod that is running")
}

// execToServerPodErrOnStder works like `execToServerPod`, but puts the contents of stderr in the error, if not empty
func (r *DrupalSiteReconciler) execToServerPodErrOnStderr(ctx context.Context, d *webservicesv1a1.DrupalSite, containerName string, stdin io.Reader, command ...string) (stdout string, err error) {
	stdout, stderr, err := r.execToServerPod(ctx, d, containerName, stdin, command...)
	if err != nil || stderr != "" {
		return "", fmt.Errorf("STDERR: %s \n%w", stderr, err)
	}
	return stdout, nil
}

/*
ensureResources ensures the presence of all the resources that the DrupalSite needs to serve content.
This includes BuildConfigs/ImageStreams, DB, PVC, PHP/Nginx deployment + service, site install job, Routes.
*/
func (r *DrupalSiteReconciler) ensureResources(drp *webservicesv1a1.DrupalSite, log logr.Logger) (transientErrs []reconcileError) {
	ctx := context.TODO()

	// 1. BuildConfigs and ImageStreams

	if len(drp.Spec.Environment.ExtraConfigRepo) > 0 {
		if transientErr := r.ensureResourceX(ctx, drp, "is_s2i", log); transientErr != nil {
			transientErrs = append(transientErrs, transientErr.Wrap("%v: for S2I SiteBuilder ImageStream"))
		}
		if transientErr := r.ensureResourceX(ctx, drp, "bc_s2i", log); transientErr != nil {
			transientErrs = append(transientErrs, transientErr.Wrap("%v: for S2I SiteBuilder BuildConfig"))
		}
	}
	// 2. Data layer

	if transientErr := r.ensureResourceX(ctx, drp, "pvc_drupal", log); transientErr != nil {
		transientErrs = append(transientErrs, transientErr.Wrap("%v: for Drupal PVC"))
	}
	if transientErr := r.ensureResourceX(ctx, drp, "dbod_cr", log); transientErr != nil {
		transientErrs = append(transientErrs, transientErr.Wrap("%v: for DBOD resource"))
	}
	if transientErr := r.ensureResourceX(ctx, drp, "webdav_secret", log); transientErr != nil {
		transientErrs = append(transientErrs, transientErr.Wrap("%v: for WebDAV Secret"))
	}

	// 3. Serving layer

	if transientErr := r.ensureResourceX(ctx, drp, "cm_php", log); transientErr != nil {
		transientErrs = append(transientErrs, transientErr.Wrap("%v: for PHP-FPM CM"))
	}
	if transientErr := r.ensureResourceX(ctx, drp, "cm_nginx", log); transientErr != nil {
		transientErrs = append(transientErrs, transientErr.Wrap("%v: for Nginx CM"))
	}
	if transientErr := r.ensureResourceX(ctx, drp, "cm_settings", log); transientErr != nil {
		transientErrs = append(transientErrs, transientErr.Wrap("%v: for settings.php CM"))
	}
	if r.isDBODProvisioned(ctx, drp) {
		if transientErr := r.ensureResourceX(ctx, drp, "deploy_drupal", log); transientErr != nil {
			transientErrs = append(transientErrs, transientErr.Wrap("%v: for Drupal DC"))
		}
	}
	if transientErr := r.ensureResourceX(ctx, drp, "svc_nginx", log); transientErr != nil {
		transientErrs = append(transientErrs, transientErr.Wrap("%v: for Nginx SVC"))
	}
	if r.isDBODProvisioned(ctx, drp) {
		switch {
		case drp.Spec.InitCloneFrom == "":
			if transientErr := r.ensureResourceX(ctx, drp, "site_install_job", log); transientErr != nil {
				transientErrs = append(transientErrs, transientErr.Wrap("%v: for site install Job"))
			}
		case drp.Spec.InitCloneFrom != "":
			if transientErr := r.ensureResourceX(ctx, drp, "clone_job", log); transientErr != nil {
				transientErrs = append(transientErrs, transientErr.Wrap("%v: for clone Job"))
			}
		}
	}

	// 4. Ingress

	if drp.ConditionTrue("Initialized") && drp.ConditionTrue("Ready") && drp.Spec.Publish {
		if transientErr := r.ensureResourceX(ctx, drp, "webdav_route", log); transientErr != nil {
			transientErrs = append(transientErrs, transientErr.Wrap("%v: for WebDAV Route"))
		}
		if transientErr := r.ensureResourceX(ctx, drp, "route", log); transientErr != nil {
			transientErrs = append(transientErrs, transientErr.Wrap("%v: for Route"))
		}
		if transientErr := r.ensureResourceX(ctx, drp, "oidc_return_uri", log); transientErr != nil {
			transientErrs = append(transientErrs, transientErr.Wrap("%v: for OidcReturnURI"))
		}
	} else {
		if transientErr := r.ensureNoWebDAVRoute(ctx, drp, log); transientErr != nil {
			transientErrs = append(transientErrs, transientErr.Wrap("%v: while deleting the WebDAV Route"))
		}
		if transientErr := r.ensureNoRoute(ctx, drp, log); transientErr != nil {
			transientErrs = append(transientErrs, transientErr.Wrap("%v: while deleting the Route"))
		}
		if transientErr := r.ensureNoReturnURI(ctx, drp, log); transientErr != nil {
			transientErrs = append(transientErrs, transientErr.Wrap("%v: while deleting the OidcReturnURI"))
		}
	}
	return transientErrs
}

/*
ensureResourceX ensure the requested resource is created, with the following valid values
	- pvc_drupal: PersistentVolume for the drupalsite
	- site_install_job: Kubernetes Job for the drush site-install
	- clone_job: Kubernetes Job for cloning a drupal site
	- is_base: ImageStream for sitebuilder-base
	- is_s2i: ImageStream for S2I sitebuilder
	- bc_s2i: BuildConfig for S2I sitebuilder
	- deploy_drupal: Deployment for Nginx & PHP-FPM
	- svc_nginx: Service for Nginx
	- cm_php: ConfigMap for PHP-FPM
	- cm_nginx: ConfigMap for Nginx
	- cm_settings: ConfigMap for `settings.php`
	- route: Route for the drupalsite
	- oidc_return_uri: Redirection URI for OIDC
	- dbod_cr: DBOD custom resource to establish database & respective connection for the drupalsite
	- webdav_secret: Secret with credential for WebDAV
	- webdav_route: Route for WebDAV
*/
func (r *DrupalSiteReconciler) ensureResourceX(ctx context.Context, d *webservicesv1a1.DrupalSite, resType string, log logr.Logger) (transientErr reconcileError) {
	switch resType {
	case "is_s2i":
		is := &imagev1.ImageStream{ObjectMeta: metav1.ObjectMeta{Name: "site-builder-s2i-" + d.Name, Namespace: d.Namespace}}
		_, err := controllerruntime.CreateOrUpdate(ctx, r.Client, is, func() error {
			return imageStreamForDrupalSiteBuilderS2I(is, d)
		})
		if err != nil {
			log.Error(err, "Failed to ensure Resource", "Kind", is.TypeMeta.Kind, "Resource.Namespace", is.Namespace, "Resource.Name", is.Name)
			return newApplicationError(err, ErrClientK8s)
		}
		return nil
	case "bc_s2i":
		bc := &buildv1.BuildConfig{ObjectMeta: metav1.ObjectMeta{Name: "site-builder-s2i-" + nameVersionHash(d), Namespace: d.Namespace}}
		// We don't really benefit from udating here, because of https://docs.openshift.com/container-platform/4.6/builds/triggering-builds-build-hooks.html#builds-configuration-change-triggers_triggering-builds-build-hooks
		_, err := controllerruntime.CreateOrUpdate(ctx, r.Client, bc, func() error {
			return buildConfigForDrupalSiteBuilderS2I(bc, d)
		})
		if err != nil {
			log.Error(err, "Failed to ensure Resource", "Kind", bc.TypeMeta.Kind, "Resource.Namespace", bc.Namespace, "Resource.Name", bc.Name)
			return newApplicationError(err, ErrClientK8s)
		}
		return nil
	case "webdav_secret":
		webdav_secret := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: "webdav-secret-" + d.Name, Namespace: d.Namespace}}
		_, err := controllerruntime.CreateOrUpdate(ctx, r.Client, webdav_secret, func() error {
			log.Info("Ensuring Resource", "Kind", webdav_secret.TypeMeta.Kind, "Resource.Namespace", webdav_secret.Namespace, "Resource.Name", webdav_secret.Name)
			return secretForWebDAV(webdav_secret, d)
		})
		if err != nil {
			log.Error(err, "Failed to ensure Resource", "Kind", webdav_secret.TypeMeta.Kind, "Resource.Namespace", webdav_secret.Namespace, "Resource.Name", webdav_secret.Name)
			return newApplicationError(err, ErrClientK8s)
		}
		return nil
	case "deploy_drupal":
		deploy := &appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Name: d.Name, Namespace: d.Namespace}}
		err := r.Get(ctx, types.NamespacedName{Name: deploy.Name, Namespace: deploy.Namespace}, deploy)

		// Check if a deployment exists & if any of the given conditions satisfy
		// Add comment
		if err == nil && (d.ConditionTrue("UpdateNeeded") || d.ConditionTrue("CodeUpdatingFailed") || d.ConditionTrue("DBUpdatingFailed")) {
			return nil
		}
		if databaseSecret := databaseSecretName(d); len(databaseSecret) != 0 {
			deploy := &appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Name: d.Name, Namespace: d.Namespace}}
			_, err := controllerruntime.CreateOrUpdate(ctx, r.Client, deploy, func() error {
				return deploymentForDrupalSite(deploy, databaseSecret, d, d.Spec.DrupalVersion)
			})
			if err != nil {
				log.Error(err, "Failed to ensure Resource", "Kind", deploy.TypeMeta.Kind, "Resource.Namespace", deploy.Namespace, "Resource.Name", deploy.Name)
				return newApplicationError(err, ErrClientK8s)
			}
		}
		return nil
	case "svc_nginx":
		svc := &corev1.Service{ObjectMeta: metav1.ObjectMeta{Name: d.Name, Namespace: d.Namespace}}
		_, err := controllerruntime.CreateOrUpdate(ctx, r.Client, svc, func() error {
			return serviceForDrupalSite(svc, d)
		})
		if err != nil {
			log.Error(err, "Failed to ensure Resource", "Kind", svc.TypeMeta.Kind, "Resource.Namespace", svc.Namespace, "Resource.Name", svc.Name)
			return newApplicationError(err, ErrClientK8s)
		}
		return nil
	case "pvc_drupal":
		pvc := &corev1.PersistentVolumeClaim{ObjectMeta: metav1.ObjectMeta{Name: "pv-claim-" + d.Name, Namespace: d.Namespace}}
		_, err := controllerruntime.CreateOrUpdate(ctx, r.Client, pvc, func() error {
			return persistentVolumeClaimForDrupalSite(pvc, d)
		})
		if err != nil {
			log.Error(err, "Failed to ensure Resource", "Kind", pvc.TypeMeta.Kind, "Resource.Namespace", pvc.Namespace, "Resource.Name", pvc.Name)
			return newApplicationError(err, ErrClientK8s)
		}
		return nil
	case "route":
		route := &routev1.Route{ObjectMeta: metav1.ObjectMeta{Name: d.Name, Namespace: d.Namespace}}
		_, err := controllerruntime.CreateOrUpdate(ctx, r.Client, route, func() error {
			return routeForDrupalSite(route, d)
		})
		if err != nil {
			log.Error(err, "Failed to ensure Resource", "Kind", route.TypeMeta.Kind, "Resource.Namespace", route.Namespace, "Resource.Name", route.Name)
			return newApplicationError(err, ErrClientK8s)
		}
		return nil
	case "oidc_return_uri":
		OidcReturnURI := &authz.OidcReturnURI{ObjectMeta: metav1.ObjectMeta{Name: d.Name, Namespace: d.Namespace}}
		_, err := controllerruntime.CreateOrUpdate(ctx, r.Client, OidcReturnURI, func() error {
			log.Info("Ensuring Resource", "Kind", OidcReturnURI.TypeMeta.Kind, "Resource.Namespace", OidcReturnURI.Namespace, "Resource.Name", OidcReturnURI.Name)
			return newOidcReturnURI(OidcReturnURI, d)
		})
		if err != nil {
			log.Error(err, "Failed to ensure Resource", "Kind", OidcReturnURI.TypeMeta.Kind, "Resource.Namespace", OidcReturnURI.Namespace, "Resource.Name", OidcReturnURI.Name)
		}
		return nil
	case "webdav_route":
		webdav_route := &routev1.Route{ObjectMeta: metav1.ObjectMeta{Name: "webdav-" + d.Name, Namespace: d.Namespace}}
		_, err := controllerruntime.CreateOrUpdate(ctx, r.Client, webdav_route, func() error {
			log.Info("Ensuring Resource", "Kind", webdav_route.TypeMeta.Kind, "Resource.Namespace", webdav_route.Namespace, "Resource.Name", webdav_route.Name)
			return routeForWebDAV(webdav_route, d)
		})
		if err != nil {
			log.Error(err, "Failed to ensure Resource", "Kind", webdav_route.TypeMeta.Kind, "Resource.Namespace", webdav_route.Namespace, "Resource.Name", webdav_route.Name)
			return newApplicationError(err, ErrClientK8s)
		}
		return nil
	case "site_install_job":
		databaseSecretName := databaseSecretName(d)
		if len(databaseSecretName) == 0 {
			return nil
		}
		job := &batchv1.Job{ObjectMeta: metav1.ObjectMeta{Name: "site-install-" + d.Name, Namespace: d.Namespace}}
		_, err := controllerruntime.CreateOrUpdate(ctx, r.Client, job, func() error {
			return jobForDrupalSiteDrush(job, databaseSecretName, d)
		})
		if err != nil {
			log.Error(err, "Failed to ensure Resource", "Kind", job.TypeMeta.Kind, "Resource.Namespace", job.Namespace, "Resource.Name", job.Name)
			return newApplicationError(err, ErrClientK8s)
		}
		return nil
	case "clone_job":
		if databaseSecret := databaseSecretName(d); len(databaseSecret) != 0 {
			job := &batchv1.Job{ObjectMeta: metav1.ObjectMeta{Name: "clone-" + d.Name, Namespace: d.Namespace}}
			_, err := controllerruntime.CreateOrUpdate(ctx, r.Client, job, func() error {
				log.Info("Ensuring Resource", "Kind", job.TypeMeta.Kind, "Resource.Namespace", job.Namespace, "Resource.Name", job.Name)
				return jobForDrupalSiteClone(job, databaseSecret, d)
			})
			if err != nil {
				log.Error(err, "Failed to ensure Resource", "Kind", job.TypeMeta.Kind, "Resource.Namespace", job.Namespace, "Resource.Name", job.Name)
				return newApplicationError(err, ErrClientK8s)
			}
		}
		return nil
	case "cm_php":
		cm := &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "php-fpm-" + d.Name, Namespace: d.Namespace}}
		_, err := controllerruntime.CreateOrUpdate(ctx, r.Client, cm, func() error {
			return updateConfigMapForPHPFPM(ctx, cm, d, r.Client)
		})
		if err != nil {
			log.Error(err, "Failed to ensure Resource", "Kind", cm.TypeMeta.Kind, "Resource.Namespace", cm.Namespace, "Resource.Name", cm.Name)
			return newApplicationError(err, ErrClientK8s)
		}
		return nil
	case "cm_nginx":
		cm := &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "nginx-" + d.Name, Namespace: d.Namespace}}
		_, err := controllerruntime.CreateOrUpdate(ctx, r.Client, cm, func() error {
			return updateConfigMapForNginx(ctx, cm, d, r.Client)
		})
		if err != nil {
			log.Error(err, "Failed to ensure Resource", "Kind", cm.TypeMeta.Kind, "Resource.Namespace", cm.Namespace, "Resource.Name", cm.Name)
			return newApplicationError(err, ErrClientK8s)
		}
		return nil
	case "cm_settings":
		cm := &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "site-settings-" + d.Name, Namespace: d.Namespace}}
		_, err := controllerruntime.CreateOrUpdate(ctx, r.Client, cm, func() error {
			return updateConfigMapForSiteSettings(ctx, cm, d)
		})
		if err != nil {
			log.Error(err, "Failed to ensure Resource", "Kind", cm.TypeMeta.Kind, "Resource.Namespace", cm.Namespace, "Resource.Name", cm.Name)
			return newApplicationError(err, ErrClientK8s)
		}
		return nil
	case "dbod_cr":
		dbod := &dbodv1a1.Database{ObjectMeta: metav1.ObjectMeta{Name: d.Name, Namespace: d.Namespace}}
		_, err := controllerruntime.CreateOrUpdate(ctx, r.Client, dbod, func() error {
			return dbodForDrupalSite(dbod, d)
		})
		if err != nil {
			log.Error(err, "Failed to ensure Resource", "Kind", dbod.TypeMeta.Kind, "Resource.Namespace", dbod.Namespace, "Resource.Name", dbod.Name)
			return newApplicationError(err, ErrClientK8s)
		}
		return nil
	default:
		return newApplicationError(nil, ErrFunctionDomain)
	}
}

func (r *DrupalSiteReconciler) ensureNoRoute(ctx context.Context, d *webservicesv1a1.DrupalSite, log logr.Logger) (transientErr reconcileError) {
	route := &routev1.Route{ObjectMeta: metav1.ObjectMeta{Name: d.Name, Namespace: d.Namespace}}
	if err := r.Get(ctx, types.NamespacedName{Name: route.Name, Namespace: route.Namespace}, route); err != nil {
		switch {
		case k8sapierrors.IsNotFound(err):
			return nil
		default:
			return newApplicationError(err, ErrClientK8s)
		}
	}
	if err := r.Delete(ctx, route); err != nil {
		return newApplicationError(err, ErrClientK8s)
	}
	return nil
}

func (r *DrupalSiteReconciler) ensureNoWebDAVRoute(ctx context.Context, d *webservicesv1a1.DrupalSite, log logr.Logger) (transientErr reconcileError) {
	webdav_route := &routev1.Route{ObjectMeta: metav1.ObjectMeta{Name: d.Name, Namespace: d.Namespace}}
	if err := r.Get(ctx, types.NamespacedName{Name: webdav_route.Name, Namespace: webdav_route.Namespace}, webdav_route); err != nil {
		switch {
		case k8sapierrors.IsNotFound(err):
			return nil
		default:
			return newApplicationError(err, ErrClientK8s)
		}
	}
	if err := r.Delete(ctx, webdav_route); err != nil {
		return newApplicationError(err, ErrClientK8s)
	}
	return nil
}

func (r *DrupalSiteReconciler) ensureNoReturnURI(ctx context.Context, d *webservicesv1a1.DrupalSite, log logr.Logger) (transientErr reconcileError) {
	oidc_return_uri := &authz.OidcReturnURI{}
	if err := r.Get(ctx, types.NamespacedName{Name: oidc_return_uri.Name, Namespace: oidc_return_uri.Namespace}, oidc_return_uri); err != nil {
		switch {
		case k8sapierrors.IsNotFound(err):
			return nil
		default:
			return newApplicationError(err, ErrClientK8s)
		}
	}
	if err := r.Delete(ctx, oidc_return_uri); err != nil {
		return newApplicationError(err, ErrClientK8s)
	}
	return nil
}

// labelsForDrupalSite returns the labels for selecting the resources
// belonging to the given drupalSite CR name.
func labelsForDrupalSite(name string) map[string]string {
	return map[string]string{"drupalSite": name}
}

// releasedImageTag is the image tag to use, depending on the `ReleaseChannel` cmdline arg:
// If `ReleaseChannel` is set, appends `-${ReleaseChannel}` to the Drupal version.
func releasedImageTag(drupalVersion string) string {
	if ReleaseChannel != "" {
		return drupalVersion + "-" + ReleaseChannel
	}
	return drupalVersion
}

// baseImageReferenceToUse returns which base image to use, depending on whether the field `environment.ExtraConfigRepo` is set.
// If yes, the S2I buildconfig will be used; baseImageReferenceToUse returns the output of imageStreamForDrupalSiteBuilderS2I().
// Otherwise, returns the sitebuilder base
func baseImageReferenceToUse(d *webservicesv1a1.DrupalSite, drupalVersion string) corev1.ObjectReference {
	if len(d.Spec.Environment.ExtraConfigRepo) > 0 {
		return corev1.ObjectReference{
			Kind: "ImageStreamTag",
			Name: "site-builder-s2i-" + d.Name + ":" + releasedImageTag(drupalVersion),
		}
	}
	return corev1.ObjectReference{
		Kind: "DockerImage",
		Name: "gitlab-registry.cern.ch/drupal/paas/drupal-runtime/site-builder-base:" + releasedImageTag(drupalVersion),
	}
}

// imageStreamForDrupalSiteBuilderS2I returns a ImageStream object for Drupal SiteBuilder S2I
func imageStreamForDrupalSiteBuilderS2I(currentobject *imagev1.ImageStream, d *webservicesv1a1.DrupalSite) error {
	if currentobject.CreationTimestamp.IsZero() {
		addOwnerRefToObject(currentobject, asOwner(d))
		currentobject.Labels = map[string]string{}
		ls := labelsForDrupalSite(d.Name)
		ls["app"] = "site-builder"
		for k, v := range ls {
			currentobject.Labels[k] = v
		}
		currentobject.Spec.LookupPolicy.Local = true
	}
	return nil
}

// buildConfigForDrupalSiteBuilderS2I returns a BuildConfig object for Drupal SiteBuilder S2I
func buildConfigForDrupalSiteBuilderS2I(currentobject *buildv1.BuildConfig, d *webservicesv1a1.DrupalSite) error {
	if currentobject.CreationTimestamp.IsZero() {
		addOwnerRefToObject(currentobject, asOwner(d))
		currentobject.Labels = map[string]string{}
		ls := labelsForDrupalSite(d.Name)
		ls["app"] = "site-builder"
		for k, v := range ls {
			currentobject.Labels[k] = v
		}
		currentobject.Spec = buildv1.BuildConfigSpec{
			CommonSpec: buildv1.CommonSpec{
				Resources:                 BuildResources,
				CompletionDeadlineSeconds: pointer.Int64Ptr(1200),
				Source: buildv1.BuildSource{
					Git: &buildv1.GitBuildSource{
						// TODO: support branches https://gitlab.cern.ch/drupal/paas/drupalsite-operator/-/issues/28
						Ref: "master",
						URI: d.Spec.Environment.ExtraConfigRepo,
					},
				},
				Strategy: buildv1.BuildStrategy{
					SourceStrategy: &buildv1.SourceBuildStrategy{
						From: corev1.ObjectReference{
							Kind: "DockerImage",
							Name: "gitlab-registry.cern.ch/drupal/paas/drupal-runtime/site-builder-base:" + releasedImageTag(d.Spec.DrupalVersion),
						},
					},
				},
				Output: buildv1.BuildOutput{
					To: &corev1.ObjectReference{
						Kind: "ImageStreamTag",
						Name: "site-builder-s2i-" + d.Name + ":" + releasedImageTag(d.Spec.DrupalVersion),
					},
				},
			},
			Triggers: []buildv1.BuildTriggerPolicy{
				{
					Type: buildv1.ConfigChangeBuildTriggerType,
				},
			},
		}
	}
	return nil
}

// dbodForDrupalSite returns a DBOD resource for the the Drupal Site
func dbodForDrupalSite(currentobject *dbodv1a1.Database, d *webservicesv1a1.DrupalSite) error {
	if currentobject.CreationTimestamp.IsZero() {
		addOwnerRefToObject(currentobject, asOwner(d))
		dbID := md5.Sum([]byte(d.Namespace + "-" + d.Name))
		currentobject.Spec = dbodv1a1.DatabaseSpec{
			DatabaseClass: string(d.Spec.Environment.DatabaseClass),
			DbName:        hex.EncodeToString(dbID[1:10]),
			DbUser:        hex.EncodeToString(dbID[1:10]),
			ExtraLabels: map[string]string{
				"drupalSite": d.Name,
			},
		}
	}
	// Enforce only the drupalsite labels on the resource on every iteration
	if currentobject.Labels == nil {
		currentobject.Labels = map[string]string{}
	}
	ls := labelsForDrupalSite(d.Name)
	ls["app"] = "dbod"
	for k, v := range ls {
		currentobject.Labels[k] = v
	}
	return nil
}

// deploymentForDrupalSite defines the server runtime deployment of a DrupalSite
func deploymentForDrupalSite(currentobject *appsv1.Deployment, databaseSecret string, d *webservicesv1a1.DrupalSite, drupalVersion string) error {
	nginxResources, err := resourceRequestLimit("10Mi", "20m", "20Mi", "500m")
	if err != nil {
		return newApplicationError(err, ErrFunctionDomain)
	}
	drupallogsResources, err := resourceRequestLimit("10Mi", "1m", "15Mi", "5m")
	if err != nil {
		return newApplicationError(err, ErrFunctionDomain)
	}
	phpfpmResources, err := resourceRequestLimit("100Mi", "60m", "270Mi", "1800m")
	if err != nil {
		return newApplicationError(err, ErrFunctionDomain)
	}

	if currentobject.CreationTimestamp.IsZero() {
		addOwnerRefToObject(currentobject, asOwner(d))
		currentobject.Annotations = map[string]string{}
		currentobject.Annotations["alpha.image.policy.openshift.io/resolve-names"] = "*"
		currentobject.Spec.Template.ObjectMeta.Annotations = map[string]string{
			"php-configmap-version":   "1",
			"nginx-configmap-version": "1",
		}
		currentobject.Spec.Template.Spec.Containers = []corev1.Container{{Name: "nginx"}, {Name: "php-fpm"}, {Name: "drupal-logs"}}
		currentobject.Labels = map[string]string{}

		// This annotation is required to trigger new rollout, when the imagestream gets updated with a new image for the given tag. Without this, deployments might start running with
		// a wrong image built from a different build, that is left out on the node
		// NOTE: Removing this annotation temporarily, as it is causing indefinite rollouts with some sites
		// ref: https://gitlab.cern.ch/drupal/paas/drupalsite-operator/-/issues/54
		// currentobject.Annotations["image.openshift.io/triggers"] = "[{\"from\":{\"kind\":\"ImageStreamTag\",\"name\":\"nginx-" + d.Name + ":" + drupalVersion + "\",\"namespace\":\"" + d.Namespace + "\"},\"fieldPath\":\"spec.template.spec.containers[?(@.name==\\\"nginx\\\")].image\",\"pause\":\"false\"}]"
		ls := labelsForDrupalSite(d.Name)
		ls["app"] = "drupal"
		for k, v := range ls {
			currentobject.Labels[k] = v
		}

		currentobject.Spec.Template.Spec.ShareProcessNamespace = pointer.BoolPtr(true)

		currentobject.Spec.Replicas = pointer.Int32Ptr(1)
		currentobject.Spec.Selector = &metav1.LabelSelector{
			MatchLabels: ls,
		}
		currentobject.Spec.Template.ObjectMeta.Labels = ls

		if _, bool := d.Annotations["nodeSelectorLabel"]; bool {
			if _, bool = d.Annotations["nodeSelectorValue"]; bool {
				currentobject.Spec.Template.Spec.NodeSelector = map[string]string{
					d.Annotations["nodeSelectorLabel"]: d.Annotations["nodeSelectorValue"],
				}
			}
		}

		currentobject.Spec.Template.Spec.Volumes = []corev1.Volume{
			{
				Name: "drupal-directory-" + d.Name,
				VolumeSource: corev1.VolumeSource{
					PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{
						ClaimName: "pv-claim-" + d.Name,
					},
				}},
			{
				Name: "php-config-volume",
				VolumeSource: corev1.VolumeSource{
					ConfigMap: &corev1.ConfigMapVolumeSource{
						LocalObjectReference: corev1.LocalObjectReference{
							Name: "php-fpm-" + d.Name,
						},
					},
				},
			},
			{
				Name: "nginx-config-volume",
				VolumeSource: corev1.VolumeSource{
					ConfigMap: &corev1.ConfigMapVolumeSource{
						LocalObjectReference: corev1.LocalObjectReference{
							Name: "nginx-" + d.Name,
						},
					},
				},
			},
			{
				Name: "site-settings-php",
				VolumeSource: corev1.VolumeSource{
					ConfigMap: &corev1.ConfigMapVolumeSource{
						LocalObjectReference: corev1.LocalObjectReference{
							Name: "site-settings-" + d.Name,
						},
					},
				},
			},
			{
				Name:         "empty-dir",
				VolumeSource: corev1.VolumeSource{EmptyDir: &corev1.EmptyDirVolumeSource{}},
			},
			{
				Name: "webdav-volume",
				VolumeSource: corev1.VolumeSource{
					Secret: &corev1.SecretVolumeSource{
						SecretName: "webdav-secret-" + d.Name,
					},
				},
			},
		}

		for i, container := range currentobject.Spec.Template.Spec.Containers {
			switch container.Name {
			case "nginx":
				currentobject.Spec.Template.Spec.Containers[i].Name = "nginx"
				// Set to always due to https://gitlab.cern.ch/drupal/paas/drupalsite-operator/-/issues/54
				currentobject.Spec.Template.Spec.Containers[i].ImagePullPolicy = "Always"
				currentobject.Spec.Template.Spec.Containers[i].Ports = []corev1.ContainerPort{{
					ContainerPort: 8080,
					Name:          "nginx",
					Protocol:      "TCP",
				}}
				currentobject.Spec.Template.Spec.Containers[i].Env = []corev1.EnvVar{
					{
						Name:  "DRUPAL_SHARED_VOLUME",
						Value: "/drupal-data",
					},
				}
				currentobject.Spec.Template.Spec.Containers[i].VolumeMounts = []corev1.VolumeMount{
					{
						Name:      "drupal-directory-" + d.Name,
						MountPath: "/drupal-data",
					},
					{
						Name:      "nginx-config-volume",
						MountPath: "/etc/nginx/custom.conf",
						SubPath:   "custom.conf",
						ReadOnly:  true,
					},
					{
						Name:      "empty-dir",
						MountPath: "/var/run/",
					},
					{
						Name:      "webdav-volume",
						MountPath: "/etc/nginx/webdav",
					},
				}
				currentobject.Spec.Template.Spec.Containers[i].Resources = nginxResources

			case "php-fpm":
				currentobject.Spec.Template.Spec.Containers[i].Name = "php-fpm"
				currentobject.Spec.Template.Spec.Containers[i].Command = []string{"/run-php-fpm.sh"}
				// Set to always due to https://gitlab.cern.ch/drupal/paas/drupalsite-operator/-/issues/54
				currentobject.Spec.Template.Spec.Containers[i].ImagePullPolicy = "Always"
				currentobject.Spec.Template.Spec.Containers[i].Ports = []corev1.ContainerPort{{
					ContainerPort: 9000,
					Name:          "php-fpm",
					Protocol:      "TCP",
				}}
				currentobject.Spec.Template.Spec.Containers[i].Env = []corev1.EnvVar{
					{
						Name:  "DRUPAL_SHARED_VOLUME",
						Value: "/drupal-data",
					},
				}
				currentobject.Spec.Template.Spec.Containers[i].EnvFrom = []corev1.EnvFromSource{
					{
						SecretRef: &corev1.SecretEnvSource{
							LocalObjectReference: corev1.LocalObjectReference{
								Name: databaseSecret,
							},
						},
					},
					{
						SecretRef: &corev1.SecretEnvSource{
							LocalObjectReference: corev1.LocalObjectReference{
								Name: oidcSecretName, //This is always set the same way
							},
						},
					},
				}
				currentobject.Spec.Template.Spec.Containers[i].VolumeMounts = []corev1.VolumeMount{
					{
						Name:      "drupal-directory-" + d.Name,
						MountPath: "/drupal-data",
					},
					{
						Name:      "php-config-volume",
						MountPath: "/usr/local/etc/php-fpm.d/zz-docker.conf",
						SubPath:   "zz-docker.conf",
						ReadOnly:  true,
					},
					{
						Name:      "empty-dir",
						MountPath: "/var/run/",
					},
					{
						Name:      "site-settings-php",
						MountPath: "/app/web/sites/default/settings.php",
						SubPath:   "settings.php",
						ReadOnly:  true,
					},
				}
				currentobject.Spec.Template.Spec.Containers[i].Resources = phpfpmResources

			case "drupal-logs":
				currentobject.Spec.Template.Spec.Containers[i].Name = "drupal-logs"
				currentobject.Spec.Template.Spec.Containers[i].Command = []string{"sh", "-c", "touch /drupal-data/drupal.log;" + " tail -f /drupal-data/drupal.log"}
				// Set to always due to https://gitlab.cern.ch/drupal/paas/drupalsite-operator/-/issues/54
				currentobject.Spec.Template.Spec.Containers[i].ImagePullPolicy = "Always"
				currentobject.Spec.Template.Spec.Containers[i].Ports = []corev1.ContainerPort{{
					ContainerPort: 8081,
					Name:          "drupal-logs",
					Protocol:      "TCP",
				}}
				currentobject.Spec.Template.Spec.Containers[i].VolumeMounts = []corev1.VolumeMount{
					{
						Name:      "drupal-directory-" + d.Name,
						MountPath: "/drupal-data",
					},
				}
				currentobject.Spec.Template.Spec.Containers[i].Resources = drupallogsResources
			}
		}

	}

	_, annotExists := currentobject.Spec.Template.ObjectMeta.Annotations["drupalVersion"]
	if !annotExists || d.Status.LastRunningDrupalVersion == "" || currentobject.Spec.Template.ObjectMeta.Annotations["drupalVersion"] != drupalVersion {
		for i, container := range currentobject.Spec.Template.Spec.Containers {
			switch container.Name {
			case "nginx":
				currentobject.Spec.Template.Spec.Containers[i].Image = "gitlab-registry.cern.ch/drupal/paas/drupal-runtime/nginx:" + releasedImageTag(drupalVersion)
			case "php-fpm":
				currentobject.Spec.Template.Spec.Containers[i].Image = baseImageReferenceToUse(d, drupalVersion).Name
			case "drupal-logs":
				currentobject.Spec.Template.Spec.Containers[i].Image = baseImageReferenceToUse(d, drupalVersion).Name
			}
		}
	}
	// Add an annotation to be able to verify what version of pod is running. Did not use labels, as it will affect the labelselector for the deployment and might cause downtime
	currentobject.Spec.Template.ObjectMeta.Annotations["drupalVersion"] = drupalVersion
	return nil
}

// secretForWebDAV returns a Secret object
func secretForWebDAV(currentobject *corev1.Secret, d *webservicesv1a1.DrupalSite) error {
	if currentobject.CreationTimestamp.IsZero() {
		addOwnerRefToObject(currentobject, asOwner(d))
		currentobject.Type = "kubernetes.io/basic-auth"
	}
	encryptedBasicAuthPassword, err := encryptBasicAuthPassword(d.Spec.WebDAVPassword)
	if err != nil {
		return err
	}
	currentobject.StringData = map[string]string{
		"password": "admin:" + encryptedBasicAuthPassword,
	}
	if currentobject.Labels == nil {
		currentobject.Labels = map[string]string{}
	}
	if len(currentobject.GetAnnotations()[adminAnnotation]) > 0 {
		// Do nothing
		return nil
	}
	ls := labelsForDrupalSite(d.Name)
	ls["app"] = "drupal"
	for k, v := range ls {
		currentobject.Labels[k] = v
	}
	return nil
}

// persistentVolumeClaimForDrupalSite returns a PVC object
func persistentVolumeClaimForDrupalSite(currentobject *corev1.PersistentVolumeClaim, d *webservicesv1a1.DrupalSite) error {
	if currentobject.CreationTimestamp.IsZero() {
		addOwnerRefToObject(currentobject, asOwner(d))
		currentobject.Spec = corev1.PersistentVolumeClaimSpec{
			// Selector: &metav1.LabelSelector{
			// 	MatchLabels: ls,
			// },
			StorageClassName: pointer.StringPtr("cephfs-no-backup"),
			AccessModes:      []corev1.PersistentVolumeAccessMode{"ReadWriteMany"},
			Resources: corev1.ResourceRequirements{
				Requests: corev1.ResourceList{
					corev1.ResourceName(corev1.ResourceStorage): resource.MustParse(d.Spec.DiskSize),
				},
			},
		}
		currentobject.Labels = map[string]string{}
		ls := labelsForDrupalSite(d.Name)
		ls["app"] = "drupal"
		for k, v := range ls {
			currentobject.Labels[k] = v
		}
	}
	return nil
}

// serviceForDrupalSite returns a service object for Nginx
func serviceForDrupalSite(currentobject *corev1.Service, d *webservicesv1a1.DrupalSite) error {
	if currentobject.CreationTimestamp.IsZero() {
		addOwnerRefToObject(currentobject, asOwner(d))
		currentobject.Labels = map[string]string{}
		ls := labelsForDrupalSite(d.Name)
		ls["app"] = "drupal"
		for k, v := range ls {
			currentobject.Labels[k] = v
		}
		currentobject.Spec.Selector = ls
		currentobject.Spec.Ports = []corev1.ServicePort{
			{
				TargetPort: intstr.FromInt(8080),
				Name:       "nginx",
				Port:       80,
				Protocol:   "TCP",
			},
			{
				TargetPort: intstr.FromInt(8081),
				Name:       "webdav",
				Port:       81,
				Protocol:   "TCP",
			}}
	}
	return nil
}

// routeForDrupalSite returns a route object
func routeForDrupalSite(currentobject *routev1.Route, d *webservicesv1a1.DrupalSite) error {
	if currentobject.CreationTimestamp.IsZero() {
		addOwnerRefToObject(currentobject, asOwner(d))
		currentobject.Labels = map[string]string{}
		currentobject.Annotations = map[string]string{}
		if _, exists := d.Annotations["haproxy.router.openshift.io/ip_whitelist"]; exists {
			currentobject.Annotations["haproxy.router.openshift.io/ip_whitelist"] = d.Annotations["haproxy.router.openshift.io/ip_whitelist"]
		}
		ls := labelsForDrupalSite(d.Name)
		ls["app"] = "drupal"

		for k, v := range ls {
			currentobject.Labels[k] = v
		}
		currentobject.Spec = routev1.RouteSpec{
			Host: d.Spec.SiteURL,
			To: routev1.RouteTargetReference{
				Kind:   "Service",
				Name:   d.Name,
				Weight: pointer.Int32Ptr(100),
			},
			Port: &routev1.RoutePort{
				TargetPort: intstr.FromInt(8080),
			},
		}
	}
	return nil
}

// newOidcReturnURI returns a oidcReturnURI object
func newOidcReturnURI(currentobject *authz.OidcReturnURI, d *webservicesv1a1.DrupalSite) error {
	if currentobject.CreationTimestamp.IsZero() {
		addOwnerRefToObject(currentobject, asOwner(d))
	}
	url, err := url.Parse(d.Spec.SiteURL)
	if err != nil {
		return err
	}
	// This will append `/openid-connect/*` to the URL, guaranteeing all subpaths of the link can be redirected
	url.Path = path.Join(url.Path, "openid-connect")
	returnURI := "http://" + url.String() + "/*" // Hardcoded since with path.Join method creates `%2A` which will not work in the AuthzAPI, and the prefix `http`
	currentobject.Spec = authz.OidcReturnURISpec{
		RedirectURI: returnURI,
	}
	return nil
}

// routeForWebDAV returns a route object
func routeForWebDAV(currentobject *routev1.Route, d *webservicesv1a1.DrupalSite) error {
	if currentobject.CreationTimestamp.IsZero() {
		addOwnerRefToObject(currentobject, asOwner(d))
	}
	if currentobject.Labels == nil {
		currentobject.Labels = map[string]string{}
	}
	if len(currentobject.GetAnnotations()[adminAnnotation]) > 0 {
		// Do nothing
		return nil
	}
	if len(currentobject.Annotations) < 1 {
		currentobject.Annotations = map[string]string{}
	}
	if _, exists := d.Annotations["haproxy.router.openshift.io/ip_whitelist"]; exists {
		currentobject.Annotations["haproxy.router.openshift.io/ip_whitelist"] = d.Annotations["haproxy.router.openshift.io/ip_whitelist"]
	}
	ls := labelsForDrupalSite(d.Name)
	ls["app"] = "drupal"

	for k, v := range ls {
		currentobject.Labels[k] = v
	}
	currentobject.Spec = routev1.RouteSpec{
		Host: "webdav-" + d.Spec.SiteURL,
		To: routev1.RouteTargetReference{
			Kind:   "Service",
			Name:   d.Name,
			Weight: pointer.Int32Ptr(100),
		},
		Port: &routev1.RoutePort{
			TargetPort: intstr.FromInt(8081),
		},
	}
	return nil
}

// jobForDrupalSiteDrush returns a job object thats runs drush
func jobForDrupalSiteDrush(currentobject *batchv1.Job, databaseSecret string, d *webservicesv1a1.DrupalSite) error {
	ls := labelsForDrupalSite(d.Name)
	if currentobject.CreationTimestamp.IsZero() {
		addOwnerRefToObject(currentobject, asOwner(d))
		currentobject.Labels = map[string]string{}
		currentobject.Spec.Template.ObjectMeta = metav1.ObjectMeta{
			Labels: ls,
		}
		currentobject.Spec.BackoffLimit = pointer.Int32Ptr(0)
		currentobject.Spec.Template.Spec = corev1.PodSpec{
			InitContainers: []corev1.Container{{
				Image:           "bash",
				Name:            "pvc-init",
				ImagePullPolicy: "IfNotPresent",
				Command:         []string{"bash", "-c", "mkdir -p $DRUPAL_SHARED_VOLUME/{files,private,modules,themes}"},
				Env: []corev1.EnvVar{
					{
						Name:  "DRUPAL_SHARED_VOLUME",
						Value: "/drupal-data",
					},
				},
				VolumeMounts: []corev1.VolumeMount{{
					Name:      "drupal-directory-" + d.Name,
					MountPath: "/drupal-data",
				}},
			}},
			RestartPolicy: "Never",
			Containers: []corev1.Container{{
				Image:           baseImageReferenceToUse(d, d.Spec.DrupalVersion).Name,
				Name:            "drush",
				ImagePullPolicy: "Always",
				Command:         siteInstallJobForDrupalSite(),
				Env: []corev1.EnvVar{
					{
						Name:  "DRUPAL_SHARED_VOLUME",
						Value: "/drupal-data",
					},
				},
				EnvFrom: []corev1.EnvFromSource{
					{
						SecretRef: &corev1.SecretEnvSource{
							LocalObjectReference: corev1.LocalObjectReference{
								Name: databaseSecret,
							},
						},
					},
					{
						SecretRef: &corev1.SecretEnvSource{
							LocalObjectReference: corev1.LocalObjectReference{
								Name: oidcSecretName, //This is always set the same way
							},
						},
					},
				},
				VolumeMounts: []corev1.VolumeMount{{
					Name:      "drupal-directory-" + d.Name,
					MountPath: "/drupal-data",
				}},
			}},
			Volumes: []corev1.Volume{{
				Name: "drupal-directory-" + d.Name,
				VolumeSource: corev1.VolumeSource{
					PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{
						ClaimName: "pv-claim-" + d.Name,
					},
				},
			}},
		}
		ls["app"] = "drush"
		for k, v := range ls {
			currentobject.Labels[k] = v
		}
	}
	return nil
}

// jobForDrupalSiteClone returns a job object thats clones a drupalsite
func jobForDrupalSiteClone(currentobject *batchv1.Job, databaseSecret string, d *webservicesv1a1.DrupalSite) error {
	ls := labelsForDrupalSite(d.Name)
	if currentobject.CreationTimestamp.IsZero() {
		addOwnerRefToObject(currentobject, asOwner(d))
		currentobject.Labels = map[string]string{}
		currentobject.Spec.Template.ObjectMeta = metav1.ObjectMeta{
			Labels: ls,
		}
		currentobject.Spec.Template.Spec = corev1.PodSpec{
			InitContainers: []corev1.Container{
				{
					Image:           baseImageReferenceToUse(d, d.Spec.DrupalVersion).Name,
					Name:            "db-backup",
					ImagePullPolicy: "Always",
					Command:         takeBackup("dbBackUp.sql"),
					Env: []corev1.EnvVar{
						{
							Name:  "DRUPAL_SHARED_VOLUME",
							Value: "/drupal-data",
						},
					},
					EnvFrom: []corev1.EnvFromSource{
						{
							SecretRef: &corev1.SecretEnvSource{
								LocalObjectReference: corev1.LocalObjectReference{
									Name: "dbcredentials-" + d.Spec.InitCloneFrom,
								},
							},
						},
					},
					VolumeMounts: []corev1.VolumeMount{{
						Name:      "drupal-directory-" + d.Spec.Environment.InitCloneFrom,
						MountPath: "/drupal-data",
					}},
				},
			},
			RestartPolicy: "Never",
			Containers: []corev1.Container{{
				Image:           baseImageReferenceToUse(d, d.Spec.DrupalVersion).Name,
				Name:            "clone",
				ImagePullPolicy: "Always",
				Command:         cloneSource("dbBackUp.sql"),
				Env: []corev1.EnvVar{
					{
						Name:  "DRUPAL_SHARED_VOLUME",
						Value: "/drupal-data-source",
					},
				},
				EnvFrom: []corev1.EnvFromSource{
					{
						SecretRef: &corev1.SecretEnvSource{
							LocalObjectReference: corev1.LocalObjectReference{
								Name: databaseSecret,
							},
						},
					},
				},
				VolumeMounts: []corev1.VolumeMount{
					{
						Name:      "drupal-directory-" + d.Spec.Environment.InitCloneFrom,
						MountPath: "/drupal-data-source",
					},
					{
						Name:      "drupal-directory-" + d.Name,
						MountPath: "/drupal-data",
					}},
			}},
			Volumes: []corev1.Volume{
				{
					Name: "drupal-directory-" + d.Name,
					VolumeSource: corev1.VolumeSource{
						PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{
							ClaimName: "pv-claim-" + d.Name,
						},
					},
				},
				{
					Name: "drupal-directory-" + d.Spec.Environment.InitCloneFrom,
					VolumeSource: corev1.VolumeSource{
						PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{
							ClaimName: "pv-claim-" + d.Spec.Environment.InitCloneFrom,
						},
					},
				}},
		}
	}
	if len(currentobject.GetAnnotations()[adminAnnotation]) > 0 {
		// Do nothing
		return nil
	}
	ls["app"] = "clone"
	for k, v := range ls {
		currentobject.Labels[k] = v
	}
	return nil
}

// updateConfigMapForPHPFPM modifies the configmap to include the php-fpm settings file.
// If the file contents change, it rolls out a new deployment.
func updateConfigMapForPHPFPM(ctx context.Context, currentobject *corev1.ConfigMap, d *webservicesv1a1.DrupalSite, c client.Client) error {
	configPath := "/tmp/qos-" + string(d.Spec.Environment.QoSClass) + "/php-fpm.conf"
	content, err := ioutil.ReadFile(configPath)
	if err != nil {
		return newApplicationError(fmt.Errorf("reading PHP-FPM configMap failed: %w", err), ErrFilesystemIO)
	}

	if currentobject.CreationTimestamp.IsZero() {
		addOwnerRefToObject(currentobject, asOwner(d))
		currentobject.Labels = map[string]string{}
		currentobject.Annotations = map[string]string{}
		ls := labelsForDrupalSite(d.Name)
		ls["app"] = "php"
		for k, v := range ls {
			currentobject.Labels[k] = v
		}
		currentobject.Annotations["drupalRuntimeRepoRef"] = ImageRecipesRepoRef

		// Upstream PHP docker images use zz-docker.conf for configuration and this file gets loaded last (because of 'zz*') and overrides the default configuration loaded from www.conf
		currentobject.Data = map[string]string{
			"zz-docker.conf": string(content),
		}
	}

	if !currentobject.CreationTimestamp.IsZero() {
		currentConfig := currentobject.Data["zz-docker.conf"]
		if currentConfig != string(content) {
			// Roll out a new deployment
			deploy := &appsv1.Deployment{}
			err = c.Get(ctx, types.NamespacedName{Name: d.Name, Namespace: d.Namespace}, deploy)
			if err != nil {
				return newApplicationError(fmt.Errorf("failed to roll out new deployment while updating the PHP-FPM configMap (deployment not found): %w", err), ErrClientK8s)
			}
			currentVersion, _ := strconv.Atoi(deploy.Spec.Template.ObjectMeta.Annotations["php-configmap-version"])
			deploy.Spec.Template.ObjectMeta.Annotations["php-configmap-version"] = strconv.Itoa(currentVersion + 1)
			if err := c.Update(ctx, deploy); err != nil {
				return newApplicationError(fmt.Errorf("failed to roll out new deployment while updating the PHP-FPM configMap: %w", err), ErrClientK8s)
			}
		}
	}
	return nil
}

// updateConfigMapForNginx modifies the configmap to include the Nginx settings file.
// If the file contents change, it rolls out a new deployment.
func updateConfigMapForNginx(ctx context.Context, currentobject *corev1.ConfigMap, d *webservicesv1a1.DrupalSite, c client.Client) error {
	configPath := "/tmp/qos-" + string(d.Spec.Environment.QoSClass) + "/nginx.conf"
	content, err := ioutil.ReadFile(configPath)
	if err != nil {
		return newApplicationError(fmt.Errorf("reading Nginx configuration failed: %w", err), ErrFilesystemIO)
	}

	if currentobject.CreationTimestamp.IsZero() {
		addOwnerRefToObject(currentobject, asOwner(d))
		currentobject.Labels = map[string]string{}
		currentobject.Annotations = map[string]string{}
		ls := labelsForDrupalSite(d.Name)
		ls["app"] = "nginx"
		for k, v := range ls {
			currentobject.Labels[k] = v
		}
		currentobject.Annotations["drupalRuntimeRepoRef"] = ImageRecipesRepoRef

		currentobject.Data = map[string]string{
			"custom.conf": string(content),
		}
	}
	if !currentobject.CreationTimestamp.IsZero() {
		currentConfig := currentobject.Data["custom.conf"]
		if currentConfig != string(content) {
			// Roll out a new deployment
			deploy := &appsv1.Deployment{}
			err = c.Get(ctx, types.NamespacedName{Name: d.Name, Namespace: d.Namespace}, deploy)
			if err != nil {
				return newApplicationError(fmt.Errorf("failed to roll out new deployment while updating the Nginx configMap (deployment not found): %w", err), ErrClientK8s)
			}
			currentVersion, _ := strconv.Atoi(deploy.Spec.Template.ObjectMeta.Annotations["nginx-configmap-version"])
			deploy.Spec.Template.ObjectMeta.Annotations["nginx-configmap-version"] = strconv.Itoa(currentVersion + 1)
			if err := c.Update(ctx, deploy); err != nil {
				return newApplicationError(fmt.Errorf("failed to roll out new deployment while updating the Nginx configMap: %w", err), ErrClientK8s)
			}
		}
	}
	return nil
}

// updateConfigMapForSiteSettings modifies the configmap to include the file settings.php
func updateConfigMapForSiteSettings(ctx context.Context, currentobject *corev1.ConfigMap, d *webservicesv1a1.DrupalSite) error {
	if currentobject.CreationTimestamp.IsZero() {
		addOwnerRefToObject(currentobject, asOwner(d))
		if currentobject.Labels == nil {
			currentobject.Labels = map[string]string{}
		}
		if currentobject.Annotations == nil {
			currentobject.Annotations = map[string]string{}
		}
		currentobject.Annotations["drupalRuntimeRepoRef"] = ImageRecipesRepoRef

		configPath := "/tmp/sitebuilder/settings.php"

		content, err := ioutil.ReadFile(configPath)
		if err != nil {
			return newApplicationError(fmt.Errorf("reading settings.php failed: %w", err), ErrFilesystemIO)
		}
		currentobject.Data = map[string]string{
			"settings.php": string(content),
		}
	}
	ls := labelsForDrupalSite(d.Name)
	ls["app"] = "nginx"
	for k, v := range ls {
		currentobject.Labels[k] = v
	}
	return nil
}

// addOwnerRefToObject appends the desired OwnerReference to the object
func addOwnerRefToObject(obj metav1.Object, ownerRef metav1.OwnerReference) {
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
	return []string{"/operations/site-install.sh"}
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
func takeBackup(filename string) []string {
	return []string{"/operations/database-backup.sh", "-f", filename}
}

// restoreBackup outputs the command need to restore the database backup from a given filename
func restoreBackup(filename string) []string {
	return []string{"/operations/database-restore.sh", "-f", filename}
}

// cloneSource outputs the command need to clone a drupal site
func cloneSource(filename string) []string {
	return []string{"/operations/clone.sh", "-f", filename}
}

// encryptBasicAuthPassword encrypts a password for basic authentication
func encryptBasicAuthPassword(password string) (string, error) {
	encryptedPassword, err := exec.Command("openssl", "passwd", "-apr1", password).Output()
	if err != nil {
		return "", newApplicationError(fmt.Errorf("encrypting password failed: %w", err), ErrFunctionDomain)
	}
	return string(encryptedPassword), nil
}

// checkIfSiteIsInitialized outputs the command to check if a site is initialized or not
func checkIfSiteIsInitialized() []string {
	return []string{"sh", "-c",
		"drush status bootstrap | grep -q Successful"}
}
