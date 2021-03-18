/*


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
	"io/ioutil"
	"strconv"
	"strings"

	"github.com/asaskevich/govalidator"
	"github.com/go-logr/logr"
	buildv1 "github.com/openshift/api/build/v1"
	imagev1 "github.com/openshift/api/image/v1"
	routev1 "github.com/openshift/api/route/v1"
	webservicesv1a1 "gitlab.cern.ch/drupal/paas/drupalsite-operator/api/v1alpha1"
	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/utils/pointer"
	controllerruntime "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

const (
	// finalizerStr string that is going to added to every DrupalSite created
	finalizerStr          = "controller.drupalsite.webservices.cern.ch"
	productionEnvironment = "production"
	adminAnnotation       = "drupal.cern.ch/admin-custom-edit"
)

var (
	// ImageRecipesRepo refers to the drupal runtime repo which contains the dockerfiles and other config data to build the images
	// Example: "https://gitlab.cern.ch/drupal/paas/drupal-runtime.git"
	ImageRecipesRepo string
	// ImageRecipesRepoRef refers to the branch (git ref) of the drupal runtime repo which contains the dockerfiles
	// and other config data to build the images
	// Example: "s2i"
	ImageRecipesRepoRef string
	// ClusterName is used in the Route's Host field
	ClusterName string
)

type strFlagList []string

func (i *strFlagList) String() string {
	return strings.Join(*i, ",")
}

func (i *strFlagList) Set(value string) error {
	*i = append(*i, value)
	return nil
}

//validateSpec validates the spec against the DrupalSiteSpec definition
func validateSpec(drpSpec webservicesv1a1.DrupalSiteSpec) reconcileError {
	_, err := govalidator.ValidateStruct(drpSpec)
	if err != nil {
		return newApplicationError(err, ErrInvalidSpec)
	}
	return nil
}

func contains(a []string, x string) bool {
	for _, n := range a {
		if x == n {
			return true
		}
	}
	return false
}

// ensureSpecFinalizer ensures that the spec is valid, adding extra info if necessary, and that the finalizer is there,
// then returns if it needs to be updated.
func ensureSpecFinalizer(drp *webservicesv1a1.DrupalSite, log logr.Logger) (update bool) {
	if !controllerutil.ContainsFinalizer(drp, finalizerStr) {
		log.Info("Adding finalizer")
		controllerutil.AddFinalizer(drp, finalizerStr)
		update = true
	}
	return
}

/*
ensureResources ensures the presence of all the resources that the DrupalSite needs to serve content, apart from the ingress Route.
This includes BuildConfigs/ImageStreams, DB, PVC, PHP/Nginx deployment + service, site install job.
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
	if transientErr := r.ensureResourceX(ctx, drp, "is_php", log); transientErr != nil {
		transientErrs = append(transientErrs, transientErr.Wrap("%v: for PHP ImageStream"))
	}
	if transientErr := r.ensureResourceX(ctx, drp, "is_nginx", log); transientErr != nil {
		transientErrs = append(transientErrs, transientErr.Wrap("%v: for Nginx ImageStream"))
	}
	if transientErr := r.ensureResourceX(ctx, drp, "bc_php", log); transientErr != nil {
		transientErrs = append(transientErrs, transientErr.Wrap("%v: for PHP BuildConfig"))
	}
	if transientErr := r.ensureResourceX(ctx, drp, "bc_nginx", log); transientErr != nil {
		transientErrs = append(transientErrs, transientErr.Wrap("%v: for Nginx BuildConfig"))
	}

	// 2. Data layer

	if transientErr := r.ensureResourceX(ctx, drp, "pvc_drupal", log); transientErr != nil {
		transientErrs = append(transientErrs, transientErr.Wrap("%v: for Drupal PVC"))
	}
	if transientErr := r.ensureResourceX(ctx, drp, "pvc_mysql", log); transientErr != nil {
		transientErrs = append(transientErrs, transientErr.Wrap("%v: for MySQL PVC"))
	}
	if transientErr := r.ensureResourceX(ctx, drp, "deploy_mysql", log); transientErr != nil {
		transientErrs = append(transientErrs, transientErr.Wrap("%v: for Mysql DC"))
	}
	if transientErr := r.ensureResourceX(ctx, drp, "svc_mysql", log); transientErr != nil {
		transientErrs = append(transientErrs, transientErr.Wrap("%v: for Mysql SVC"))
	}
	if transientErr := r.ensureResourceX(ctx, drp, "cm_mysql", log); transientErr != nil {
		transientErrs = append(transientErrs, transientErr.Wrap("%v: for MySQL CM"))
	}

	// 3. Serving layer

	if transientErr := r.ensureResourceX(ctx, drp, "cm_php", log); transientErr != nil {
		transientErrs = append(transientErrs, transientErr.Wrap("%v: for PHP-FPM CM"))
	}
	if transientErr := r.ensureResourceX(ctx, drp, "cm_nginx", log); transientErr != nil {
		transientErrs = append(transientErrs, transientErr.Wrap("%v: for Nginx CM"))
	}
	if transientErr := r.ensureResourceX(ctx, drp, "deploy_drupal", log); transientErr != nil {
		transientErrs = append(transientErrs, transientErr.Wrap("%v: for Drupal DC"))
	}
	if transientErr := r.ensureResourceX(ctx, drp, "svc_nginx", log); transientErr != nil {
		transientErrs = append(transientErrs, transientErr.Wrap("%v: for Nginx SVC"))
	}

	// 4. Ingress

	if transientErr := r.ensureResourceX(ctx, drp, "site_install_job", log); transientErr != nil {
		transientErrs = append(transientErrs, transientErr.Wrap("%v: for site install Job"))
	}
	return transientErrs
}

// ensureIngressResources ensures the presence of the Route to access the website from the outside world
func (r *DrupalSiteReconciler) ensureIngressResources(drp *webservicesv1a1.DrupalSite, log logr.Logger) (transientErr reconcileError) {
	ctx := context.TODO()
	if transientErr := r.ensureResourceX(ctx, drp, "route", log); transientErr != nil {
		return transientErr.Wrap("%v: for Route")
	}
	return nil
}

// labelsForDrupalSite returns the labels for selecting the resources
// belonging to the given drupalSite CR name.
func labelsForDrupalSite(name string) map[string]string {
	return map[string]string{"drupalSite": name}
}

// TODO: Translate the `drupalVersion` -> {`PHP_BASE_VERSION`, `NGINX_VERSION`, `COMPOSER_VERSION`}
// Possibly an operator configmap
// see https://gitlab.cern.ch/drupal/paas/drupalsite-operator/-/issues/29
func phpBaseVersion(d *webservicesv1a1.DrupalSite) string { return "7.3.23-fpm-alpine3.12" }
func nginxVersion(d *webservicesv1a1.DrupalSite) string   { return "1.17.4" }

// baseImageReferenceToUse returns which base image to use, depending on whether the field `environment.ExtraConfigRepo` is set.
// If yes, the S2I buildconfig will be used; baseImageReferenceToUse returns the output of imageStreamForDrupalSiteBuilderS2I().
// Otherwise, returns the sitebuilder base
func baseImageReferenceToUse(d *webservicesv1a1.DrupalSite) corev1.ObjectReference {
	if len(d.Spec.Environment.ExtraConfigRepo) > 0 {
		return corev1.ObjectReference{
			Kind: "ImageStreamTag",
			Name: "drupal-site-builder-s2i-" + d.Name + ":" + d.Spec.DrupalVersion,
		}
	}
	return corev1.ObjectReference{
		Kind: "DockerImage",
		Name: "gitlab-registry.cern.ch/drupal/paas/drupal-runtime/site-builder-base:" + d.Spec.DrupalVersion,
	}
}

// triggersForBuildConfigs defines build triggers to be configured for the Nginx and PHP buildConfigs based on the input
// field `environment.ExtraConfigRepo`
func triggersForBuildConfigs(d *webservicesv1a1.DrupalSite) buildv1.BuildTriggerPolicy {
	if len(d.Spec.Environment.ExtraConfigRepo) > 0 {
		return buildv1.BuildTriggerPolicy{
			Type: buildv1.ImageChangeBuildTriggerType,
			ImageChange: &buildv1.ImageChangeTrigger{
				From: &corev1.ObjectReference{
					Kind: "ImageStreamTag",
					Name: "drupal-site-builder-s2i-" + d.Name + ":" + d.Spec.DrupalVersion,
				},
			},
		}
	}
	return buildv1.BuildTriggerPolicy{
		Type: buildv1.ConfigChangeBuildTriggerType,
	}
}

// imageStreamForDrupalSiteBuilderS2I returns a ImageStream object for Drupal SiteBuilder S2I
func imageStreamForDrupalSiteBuilderS2I(currentobject *imagev1.ImageStream, d *webservicesv1a1.DrupalSite) error {
	if currentobject.CreationTimestamp.IsZero() {
		addOwnerRefToObject(currentobject, asOwner(d))
		currentobject.Labels = map[string]string{}
	}
	if len(currentobject.GetAnnotations()[adminAnnotation]) > 0 {
		// Do nothing
		return nil
	}
	ls := labelsForDrupalSite(d.Name)
	ls["app"] = "site-builder"
	for k, v := range ls {
		currentobject.Labels[k] = v
	}
	currentobject.Spec.LookupPolicy.Local = true
	return nil
}

// imageStreamForDrupalSitePHP returns a ImageStream object for Drupal PHP
func imageStreamForDrupalSitePHP(currentobject *imagev1.ImageStream, d *webservicesv1a1.DrupalSite) error {
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
	ls := labelsForDrupalSite(d.Name)
	ls["app"] = "php"
	for k, v := range ls {
		currentobject.Labels[k] = v
	}
	currentobject.Spec.LookupPolicy.Local = true
	return nil
}

// imageStreamForDrupalSiteNginx returns a ImageStream object for Drupal Nginx
func imageStreamForDrupalSiteNginx(currentobject *imagev1.ImageStream, d *webservicesv1a1.DrupalSite) error {
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
	ls := labelsForDrupalSite(d.Name)
	ls["app"] = "nginx"
	for k, v := range ls {
		currentobject.Labels[k] = v
	}
	currentobject.Spec.LookupPolicy.Local = true
	return nil
}

// buildConfigForDrupalSiteBuilderS2I returns a BuildConfig object for Drupal SiteBuilder S2I
func buildConfigForDrupalSiteBuilderS2I(currentobject *buildv1.BuildConfig, d *webservicesv1a1.DrupalSite) error {
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
	ls := labelsForDrupalSite(d.Name)
	ls["app"] = "site-builder"
	for k, v := range ls {
		currentobject.Labels[k] = v
	}
	currentobject.Spec = buildv1.BuildConfigSpec{
		CommonSpec: buildv1.CommonSpec{
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
						Name: "gitlab-registry.cern.ch/drupal/paas/drupal-runtime/site-builder-base:" + d.Spec.DrupalVersion,
					},
				},
			},
			Output: buildv1.BuildOutput{
				To: &corev1.ObjectReference{
					Kind: "ImageStreamTag",
					Name: currentobject.Name + ":" + d.Spec.DrupalVersion,
				},
			},
		},
		Triggers: []buildv1.BuildTriggerPolicy{
			{
				Type: buildv1.ConfigChangeBuildTriggerType,
			},
		},
	}
	return nil
}

// buildConfigForDrupalSitePHP returns a BuildConfig object for Drupal PHP
func buildConfigForDrupalSitePHP(currentobject *buildv1.BuildConfig, d *webservicesv1a1.DrupalSite) error {
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
	ls := labelsForDrupalSite(d.Name)
	ls["app"] = "php"
	for k, v := range ls {
		currentobject.Labels[k] = v
	}
	currentobject.Spec = buildv1.BuildConfigSpec{
		CommonSpec: buildv1.CommonSpec{
			CompletionDeadlineSeconds: pointer.Int64Ptr(1200),
			Source: buildv1.BuildSource{
				Git: &buildv1.GitBuildSource{
					Ref: ImageRecipesRepoRef,
					URI: ImageRecipesRepo,
				},
				ContextDir: "images/php-fpm",
				Images: []buildv1.ImageSource{
					{
						From: baseImageReferenceToUse(d),
						Paths: []buildv1.ImageSourcePath{
							{
								SourcePath:     "/app/.",
								DestinationDir: "./images/php-fpm/drupal-files/",
							},
						},
					},
				},
			},
			Strategy: buildv1.BuildStrategy{
				DockerStrategy: &buildv1.DockerBuildStrategy{
					BuildArgs: []corev1.EnvVar{
						{
							Name:  "DRUPAL_VERSION",
							Value: d.Spec.DrupalVersion,
						},
					},
					Env: []corev1.EnvVar{
						{
							Name:  "SITE_BUILDER_DIR",
							Value: "drupal-files",
						},
					},
				},
			},
			Output: buildv1.BuildOutput{
				To: &corev1.ObjectReference{
					Kind: "ImageStreamTag",
					Name: currentobject.Name + ":" + d.Spec.DrupalVersion,
				},
			},
		},
		Triggers: []buildv1.BuildTriggerPolicy{
			triggersForBuildConfigs(d),
		},
	}
	return nil
}

// buildConfigForDrupalSiteNginx returns a BuildConfig object for Drupal Nginx
func buildConfigForDrupalSiteNginx(currentobject *buildv1.BuildConfig, d *webservicesv1a1.DrupalSite) error {
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
	ls := labelsForDrupalSite(d.Name)
	ls["app"] = "nginx"
	for k, v := range ls {
		currentobject.Labels[k] = v
	}
	currentobject.Spec = buildv1.BuildConfigSpec{
		CommonSpec: buildv1.CommonSpec{
			CompletionDeadlineSeconds: pointer.Int64Ptr(1200),
			Source: buildv1.BuildSource{
				Git: &buildv1.GitBuildSource{
					Ref: ImageRecipesRepoRef,
					URI: ImageRecipesRepo,
				},
				ContextDir: "images/nginx",
				Images: []buildv1.ImageSource{
					{
						From: baseImageReferenceToUse(d),
						Paths: []buildv1.ImageSourcePath{
							{
								SourcePath:     "/app/.",
								DestinationDir: "./images/nginx/drupal-files/",
							},
						},
					},
				},
			},
			Strategy: buildv1.BuildStrategy{
				DockerStrategy: &buildv1.DockerBuildStrategy{
					BuildArgs: []corev1.EnvVar{
						{
							Name:  "DRUPAL_VERSION",
							Value: d.Spec.DrupalVersion,
						},
					},
					Env: []corev1.EnvVar{
						{
							Name:  "SITE_BUILDER_DIR",
							Value: "drupal-files",
						},
					},
				},
			},
			Output: buildv1.BuildOutput{
				To: &corev1.ObjectReference{
					Kind: "ImageStreamTag",
					Name: currentobject.Name + ":" + d.Spec.DrupalVersion,
				},
			},
		},
		Triggers: []buildv1.BuildTriggerPolicy{
			triggersForBuildConfigs(d),
		},
	}
	return nil
}

// deploymentForDrupalSiteMySQL returns a Deployment object for MySQL
func deploymentForDrupalSiteMySQL(currentobject *appsv1.Deployment, d *webservicesv1a1.DrupalSite) error {
	if currentobject.CreationTimestamp.IsZero() {
		addOwnerRefToObject(currentobject, asOwner(d))
		currentobject.Spec.Template.ObjectMeta.Annotations = map[string]string{
			"mysql-configmap-version": "1",
		}
	}
	if currentobject.Labels == nil {
		currentobject.Labels = map[string]string{}
	}
	if len(currentobject.GetAnnotations()[adminAnnotation]) > 0 {
		// Do nothing
		return nil
	}
	ls := labelsForDrupalSite(d.Name)
	ls["app"] = "mysql"
	for k, v := range ls {
		currentobject.Labels[k] = v
	}
	currentobject.Spec.Replicas = pointer.Int32Ptr(1)
	currentobject.Spec.Selector = &metav1.LabelSelector{
		MatchLabels: ls,
	}
	currentobject.Spec.Template.ObjectMeta.Labels = ls

	currentobject.Spec.Template.Spec = corev1.PodSpec{
		Containers: []corev1.Container{{
			Image:           "mysql:5.7",
			Name:            "mysql",
			ImagePullPolicy: "IfNotPresent",
			Ports: []corev1.ContainerPort{{
				ContainerPort: 3306,
				Name:          "mysql",
				Protocol:      "TCP",
			}},
			Env: []corev1.EnvVar{
				{
					Name:  "MYSQL_DATABASE",
					Value: "drupal",
				},
				{
					Name: "MYSQL_ROOT_PASSWORD",
					ValueFrom: &corev1.EnvVarSource{
						SecretKeyRef: &corev1.SecretKeySelector{
							Key: "DB_PASSWORD",
							LocalObjectReference: corev1.LocalObjectReference{
								Name: "drupal-mysql-secret",
							},
						},
					},
				},
			},
			VolumeMounts: []corev1.VolumeMount{
				{
					Name:      "mysql-persistent-storage",
					MountPath: "/var/lib/mysql",
				},
				{
					Name:      "config-volume",
					MountPath: "/etc/mysql/conf.d/mysql-config.cnf",
					SubPath:   "mysql-config.cnf",
				}},
		}},
		Volumes: []corev1.Volume{
			{
				Name: "mysql-persistent-storage",
				VolumeSource: corev1.VolumeSource{
					PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{
						ClaimName: "mysql-pv-claim-" + d.Name,
					},
				},
			},
			{
				Name: "config-volume",
				VolumeSource: corev1.VolumeSource{
					ConfigMap: &corev1.ConfigMapVolumeSource{
						LocalObjectReference: corev1.LocalObjectReference{
							Name: "mysql-cm-" + d.Name,
						},
					},
				},
			},
		},
	}
	return nil
}

// deploymentForDrupalSite defines the server runtime deployment of a DrupalSite
func deploymentForDrupalSite(currentobject *appsv1.Deployment, d *webservicesv1a1.DrupalSite) error {
	if currentobject.CreationTimestamp.IsZero() {
		addOwnerRefToObject(currentobject, asOwner(d))
		currentobject.Annotations = map[string]string{
			"image.openshift.io/triggers": "[{\"from\":{\"kind\":\"ImageStreamTag\",\"name\":\"drupal-nginx-" + d.Name + ":" + d.Spec.DrupalVersion + "\",\"namespace\":\"" + d.Namespace + "\"},\"fieldPath\":\"spec.template.spec.containers[?(@.name==\"nginx\")].image\",\"pause\":\"false\"}, {\"from\":{\"kind\":\"ImageStreamTag\",\"name\":\"drupal-php-" + d.Name + ":" + d.Spec.DrupalVersion + "\",\"namespace\":\"" + d.Namespace + "\"},\"fieldPath\":\"spec.template.spec.containers[?(@.name==\"php-fpm\")].image\",\"pause\":\"false\"}]",
		}
		currentobject.Spec.Template.ObjectMeta.Annotations = map[string]string{
			"php-configmap-version":   "1",
			"nginx-configmap-version": "1",
		}
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
	currentobject.Spec.Replicas = pointer.Int32Ptr(1)
	currentobject.Spec.Selector = &metav1.LabelSelector{
		MatchLabels: ls,
	}
	currentobject.Spec.Template.ObjectMeta.Labels = ls

	currentobject.Spec.Template.Spec = corev1.PodSpec{
		Containers: []corev1.Container{{
      Image: "image-registry.openshift-image-registry.svc:5000/" + d.Namespace + "/drupal-nginx-" + d.Name + ":" + d.Spec.DrupalVersion,
			Name:            "nginx",
			ImagePullPolicy: "IfNotPresent",
			Ports: []corev1.ContainerPort{{
				ContainerPort: 8080,
				Name:          "nginx",
				Protocol:      "TCP",
			}},
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
							Name: "drupal-mysql-secret",
						},
					},
				},
			},
			VolumeMounts: []corev1.VolumeMount{
				{
					Name:      "drupal-directory-" + d.Name,
					MountPath: "/drupal-data",
				},
				{
					Name:      "nginx-config-volume",
					MountPath: "/etc/nginx/conf.d/default.conf",
					SubPath:   "default.conf",
				},
				{
					Name:      "empty-dir",
					MountPath: "/var/run/",
				},
			},
		},
			{
        Image: "image-registry.openshift-image-registry.svc:5000/" + d.Namespace + "/drupal-php-" + d.Name + ":" + d.Spec.DrupalVersion,
				Name:            "php-fpm",
				ImagePullPolicy: "IfNotPresent",
				Ports: []corev1.ContainerPort{{
					ContainerPort: 9000,
					Name:          "php-fpm",
					Protocol:      "TCP",
				}},
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
								Name: "drupal-mysql-secret",
							},
						},
					},
				},
				VolumeMounts: []corev1.VolumeMount{
					{
						Name:      "drupal-directory-" + d.Name,
						MountPath: "/drupal-data",
					},
					{
						Name:      "php-config-volume",
						MountPath: "/usr/local/etc/php-fpm.d/zz-docker.conf",
						SubPath:   "zz-docker.conf",
					},
					{
						Name:      "empty-dir",
						MountPath: "/var/run/",
					},
				},
			}},
		Volumes: []corev1.Volume{
			{
				Name: "drupal-directory-" + d.Name,
				VolumeSource: corev1.VolumeSource{
					PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{
						ClaimName: "drupal-pv-claim-" + d.Name,
					},
				}},
			{
				Name: "php-config-volume",
				VolumeSource: corev1.VolumeSource{
					ConfigMap: &corev1.ConfigMapVolumeSource{
						LocalObjectReference: corev1.LocalObjectReference{
							Name: "php-fpm-cm-" + d.Name,
						},
					},
				},
			},
			{
				Name: "nginx-config-volume",
				VolumeSource: corev1.VolumeSource{
					ConfigMap: &corev1.ConfigMapVolumeSource{
						LocalObjectReference: corev1.LocalObjectReference{
							Name: "nginx-cm-" + d.Name,
						},
					},
				},
			},
			{
				Name:         "empty-dir",
				VolumeSource: corev1.VolumeSource{EmptyDir: &corev1.EmptyDirVolumeSource{}},
			},
		},
	}
	return nil
}

// persistentVolumeClaimForMySQL returns a PVC object
func persistentVolumeClaimForMySQL(currentobject *corev1.PersistentVolumeClaim, d *webservicesv1a1.DrupalSite) error {
	if currentobject.CreationTimestamp.IsZero() {
		addOwnerRefToObject(currentobject, asOwner(d))
		currentobject.Spec = corev1.PersistentVolumeClaimSpec{
			// Selector: &metav1.LabelSelector{
			// 	MatchLabels: ls,
			// },
			StorageClassName: pointer.StringPtr("cephfs-no-backup"),
			AccessModes:      []corev1.PersistentVolumeAccessMode{"ReadWriteOnce"},
			Resources: corev1.ResourceRequirements{
				Requests: corev1.ResourceList{
					corev1.ResourceName(corev1.ResourceStorage): resource.MustParse("5Gi"),
				},
			},
		}
	}
	if currentobject.Labels == nil {
		currentobject.Labels = map[string]string{}
	}
	if len(currentobject.GetAnnotations()[adminAnnotation]) > 0 {
		// Do nothing
		return nil
	}
	ls := labelsForDrupalSite(d.Name)
	ls["app"] = "mysql"
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
			AccessModes:      []corev1.PersistentVolumeAccessMode{"ReadWriteOnce"},
			Resources: corev1.ResourceRequirements{
				Requests: corev1.ResourceList{
					corev1.ResourceName(corev1.ResourceStorage): resource.MustParse("10Gi"),
				},
			},
		}
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

// serviceForDrupalSite returns a service object for Nginx
func serviceForDrupalSite(currentobject *corev1.Service, d *webservicesv1a1.DrupalSite) error {
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
	ls := labelsForDrupalSite(d.Name)
	ls["app"] = "drupal"
	for k, v := range ls {
		currentobject.Labels[k] = v
	}
	currentobject.Spec.Selector = ls
	currentobject.Spec.Ports = []corev1.ServicePort{{
		TargetPort: intstr.FromInt(8080),
		Name:       "nginx",
		Port:       80,
		Protocol:   "TCP",
	}}
	return nil
}

// serviceForDrupalSiteMySQL returns a service object for MySQL
func serviceForDrupalSiteMySQL(currentobject *corev1.Service, d *webservicesv1a1.DrupalSite) error {
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
	ls := labelsForDrupalSite(d.Name)
	ls["app"] = "mysql"
	for k, v := range ls {
		currentobject.Labels[k] = v
	}
	currentobject.Spec.Selector = ls
	currentobject.Spec.Ports = []corev1.ServicePort{{
		TargetPort: intstr.FromInt(3306),
		Name:       "mysql",
		Port:       3306,
		Protocol:   "TCP",
	}}
	return nil
}

// routeForDrupalSite returns a route object
func routeForDrupalSite(currentobject *routev1.Route, d *webservicesv1a1.DrupalSite) error {
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
	ls := labelsForDrupalSite(d.Name)
	ls["app"] = "drupal"

	env := ""
	if d.Spec.Environment.Name != productionEnvironment {
		env = d.Spec.Environment.Name + "."
	}

	for k, v := range ls {
		currentobject.Labels[k] = v
	}
	currentobject.Spec = routev1.RouteSpec{
		Host: env + d.Name + "." + ClusterName + ".cern.ch",
		To: routev1.RouteTargetReference{
			Kind:   "Service",
			Name:   "drupal-" + d.Name,
			Weight: pointer.Int32Ptr(100),
		},
		Port: &routev1.RoutePort{
			TargetPort: intstr.FromInt(8080),
		},
	}
	return nil
}

// jobForDrupalSiteDrush returns a job object thats runs drush
func jobForDrupalSiteDrush(currentobject *batchv1.Job, d *webservicesv1a1.DrupalSite) error {
	ls := labelsForDrupalSite(d.Name)
	if currentobject.CreationTimestamp.IsZero() {
		addOwnerRefToObject(currentobject, asOwner(d))
		currentobject.Labels = map[string]string{}
		currentobject.Spec.Template.ObjectMeta = metav1.ObjectMeta{
			Labels: ls,
		}
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
				Image:           "drupal-php-" + d.Name + ":" + d.Spec.DrupalVersion,
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
								Name: "drupal-mysql-secret",
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
						ClaimName: "drupal-pv-claim-" + d.Name,
					},
				},
			}},
		}
	}
	if len(currentobject.GetAnnotations()[adminAnnotation]) > 0 {
		// Do nothing
		return nil
	}
	ls["app"] = "drush"
	for k, v := range ls {
		currentobject.Labels[k] = v
	}
	return nil
}

// updateConfigMapForPHPFPM ensures a configMaps object to configure PHP, if not present. Else configmap object is updated and a new rollout is triggered
func updateConfigMapForPHPFPM(ctx context.Context, currentobject *corev1.ConfigMap, d *webservicesv1a1.DrupalSite, c client.Client) error {
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
	ls := labelsForDrupalSite(d.Name)
	ls["app"] = "php"
	for k, v := range ls {
		currentobject.Labels[k] = v
	}

	configPath := "/tmp/qos-" + string(d.Spec.Environment.QoSClass) + "/php-fpm.conf"

	content, err := ioutil.ReadFile(configPath)
	if err != nil {
		return newApplicationError(fmt.Errorf("reading PHP-FPM configMap failed: %w", err), ErrFilesystemIO)
	}

	// Upstream PHP docker images use zz-docker.conf for configuration and this file gets loaded last (because of 'zz*') and overrides the default configuration loaded from www.conf
	currentConfig := currentobject.Data["zz-docker.conf"]
	currentobject.Data = map[string]string{
		"zz-docker.conf": string(content),
	}

	if !currentobject.CreationTimestamp.IsZero() {
		if currentConfig != string(content) {
			// Roll out a new deployment
			deploy := &appsv1.Deployment{}
			err = c.Get(ctx, types.NamespacedName{Name: "drupal-" + d.Name, Namespace: d.Namespace}, deploy)
			if err != nil {
				return newApplicationError(fmt.Errorf("Failed to roll out new deployment while updating the PHP-FPM configMap (deployment not found): %w", err), ErrClientK8s)
			}
			currentVersion, _ := strconv.Atoi(deploy.Spec.Template.ObjectMeta.Annotations["php-configmap-version"])
			deploy.Spec.Template.ObjectMeta.Annotations["php-configmap-version"] = strconv.Itoa(currentVersion + 1)
			if err := c.Update(ctx, deploy); err != nil {
				return newApplicationError(fmt.Errorf("Failed to roll out new deployment while updating the PHP-FPM configMap: %w", err), ErrClientK8s)
			}
		}
	}
	return nil
}

// updateConfigMapForNginx ensures a job object thats runs drush, if not present. Else configmap object is updated and a new rollout is triggered
func updateConfigMapForNginx(ctx context.Context, currentobject *corev1.ConfigMap, d *webservicesv1a1.DrupalSite, c client.Client) error {
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
	ls := labelsForDrupalSite(d.Name)
	ls["app"] = "nginx"
	for k, v := range ls {
		currentobject.Labels[k] = v
	}

	configPath := "/tmp/qos-" + string(d.Spec.Environment.QoSClass) + "/nginx-default.conf"

	content, err := ioutil.ReadFile(configPath)
	if err != nil {
		return newApplicationError(fmt.Errorf("reading Nginx configuration failed: %w", err), ErrFilesystemIO)
	}

	currentConfig := currentobject.Data["default.conf"]
	currentobject.Data = map[string]string{
		"default.conf": string(content),
	}

	if !currentobject.CreationTimestamp.IsZero() {
		if currentConfig != string(content) {
			// Roll out a new deployment
			deploy := &appsv1.Deployment{}
			err = c.Get(ctx, types.NamespacedName{Name: "drupal-" + d.Name, Namespace: d.Namespace}, deploy)
			if err != nil {
				return newApplicationError(fmt.Errorf("Failed to roll out new deployment while updating the Nginx configMap (deployment not found): %w", err), ErrClientK8s)
			}
			currentVersion, _ := strconv.Atoi(deploy.Spec.Template.ObjectMeta.Annotations["nginx-configmap-version"])
			deploy.Spec.Template.ObjectMeta.Annotations["nginx-configmap-version"] = strconv.Itoa(currentVersion + 1)
			if err := c.Update(ctx, deploy); err != nil {
				return newApplicationError(fmt.Errorf("Failed to roll out new deployment while updating the Nginx configMap: %w", err), ErrClientK8s)
			}
		}
	}
	return nil
}

// updateConfigMapForMySQL ensures a configmap object to configure MySQL if not present. Else configmap object is updated and a new rollout is triggered
func updateConfigMapForMySQL(ctx context.Context, currentobject *corev1.ConfigMap, d *webservicesv1a1.DrupalSite, c client.Client) error {
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
	ls := labelsForDrupalSite(d.Name)
	ls["app"] = "mysql"
	for k, v := range ls {
		currentobject.Labels[k] = v
	}

	configPath := "/tmp/qos-" + string(d.Spec.Environment.QoSClass) + "/mysql-config.cnf"

	content, err := ioutil.ReadFile(configPath)
	if err != nil {
		return newApplicationError(fmt.Errorf("reading MySQL configuration failed: %w", err), ErrFilesystemIO)
	}

	currentConfig := currentobject.Data["mysql-config.cnf"]
	currentobject.Data = map[string]string{
		"mysql-config.cnf": string(content),
	}

	if !currentobject.CreationTimestamp.IsZero() {
		if currentConfig != string(content) {
			// Roll out a new deployment
			deploy := &appsv1.Deployment{}
			err = c.Get(ctx, types.NamespacedName{Name: "drupal-" + d.Name, Namespace: d.Namespace}, deploy)
			if err != nil {
				return newApplicationError(fmt.Errorf("Failed to roll out new deployment while updating the MySQL configMap (deployment not found): %w", err), ErrClientK8s)
			}
			currentVersion, _ := strconv.Atoi(deploy.Spec.Template.ObjectMeta.Annotations["mysql-configmap-version"])
			deploy.Spec.Template.ObjectMeta.Annotations["mysql-configmap-version"] = strconv.Itoa(currentVersion + 1)
			if err := c.Update(ctx, deploy); err != nil {
				return newApplicationError(fmt.Errorf("Failed to roll out new deployment while updating the MySQL configMap: %w", err), ErrClientK8s)
			}
		}
	}
	return nil
}

/*
ensureResourceX ensure the requested resource is created, with the following valid values
	- deploy_mysql: Deployment for MySQL
	- svc_mysql: Service for MySQL
	- cm_mysql: Configmap for MySQL
	- pvc_mysql: PersistentVolume for the MySQL
	- pvc_drupal: PersistentVolume for the drupalsite
	- site_install_job: Kubernetes Job for the drush site-install
	- is_base: ImageStream for sitebuilder-base
	- is_s2i: ImageStream for S2I sitebuilder
	- is_php: ImageStream for PHP
	- is_nginx: ImageStream for Nginx
	- bc_s2i: BuildConfig for S2I sitebuilder
	- bc_php: BuildConfig for PHP
	- bc_nginx: BuildConfig for Nginx
	- deploy_drupal: Deployment for Nginx & PHP-FPM
	- svc_nginx: Service for Nginx
	- cm_php: ConfigMap for PHP-FPM
	- cm_nginx: ConfigMap for Nginx
	- route: Route for the drupalsite
*/
func (r *DrupalSiteReconciler) ensureResourceX(ctx context.Context, d *webservicesv1a1.DrupalSite, resType string, log logr.Logger) (transientErr reconcileError) {
	switch resType {
	case "is_s2i":
		is := &imagev1.ImageStream{ObjectMeta: metav1.ObjectMeta{Name: "drupal-site-builder-s2i-" + d.Name, Namespace: d.Namespace}}
		_, err := controllerruntime.CreateOrUpdate(ctx, r.Client, is, func() error {
			log.Info("Ensuring Resource", "Kind", is.TypeMeta.Kind, "Resource.Namespace", is.Namespace, "Resource.Name", is.Name)
			return imageStreamForDrupalSiteBuilderS2I(is, d)
		})
		if err != nil {
			log.Error(err, "Failed to ensure Resource", "Kind", is.TypeMeta.Kind, "Resource.Namespace", is.Namespace, "Resource.Name", is.Name)
			return newApplicationError(err, ErrClientK8s)
		}
		return nil
	case "is_nginx":
		is := &imagev1.ImageStream{ObjectMeta: metav1.ObjectMeta{Name: "drupal-nginx-" + d.Name, Namespace: d.Namespace}}
		_, err := controllerruntime.CreateOrUpdate(ctx, r.Client, is, func() error {
			log.Info("Ensuring Resource", "Kind", is.TypeMeta.Kind, "Resource.Namespace", is.Namespace, "Resource.Name", is.Name)
			return imageStreamForDrupalSiteNginx(is, d)
		})
		if err != nil {
			log.Error(err, "Failed to ensure Resource", "Kind", is.TypeMeta.Kind, "Resource.Namespace", is.Namespace, "Resource.Name", is.Name)
			return newApplicationError(err, ErrClientK8s)
		}
		return nil
	case "is_php":
		is := &imagev1.ImageStream{ObjectMeta: metav1.ObjectMeta{Name: "drupal-php-" + d.Name, Namespace: d.Namespace}}
		_, err := controllerruntime.CreateOrUpdate(ctx, r.Client, is, func() error {
			log.Info("Ensuring Resource", "Kind", is.TypeMeta.Kind, "Resource.Namespace", is.Namespace, "Resource.Name", is.Name)
			return imageStreamForDrupalSitePHP(is, d)
		})
		if err != nil {
			log.Error(err, "Failed to ensure Resource", "Kind", is.TypeMeta.Kind, "Resource.Namespace", is.Namespace, "Resource.Name", is.Name)
			return newApplicationError(err, ErrClientK8s)
		}
		return nil
	case "bc_s2i":
		bc := &buildv1.BuildConfig{ObjectMeta: metav1.ObjectMeta{Name: "drupal-site-builder-s2i-" + d.Name, Namespace: d.Namespace}}
		_, err := controllerruntime.CreateOrUpdate(ctx, r.Client, bc, func() error {
			log.Info("Ensuring Resource", "Kind", bc.TypeMeta.Kind, "Resource.Namespace", bc.Namespace, "Resource.Name", bc.Name)
			return buildConfigForDrupalSiteBuilderS2I(bc, d)
		})
		if err != nil {
			log.Error(err, "Failed to ensure Resource", "Kind", bc.TypeMeta.Kind, "Resource.Namespace", bc.Namespace, "Resource.Name", bc.Name)
			return newApplicationError(err, ErrClientK8s)
		}
		return nil
	case "bc_nginx":
		bc := &buildv1.BuildConfig{ObjectMeta: metav1.ObjectMeta{Name: "drupal-nginx-" + d.Name, Namespace: d.Namespace}}
		_, err := controllerruntime.CreateOrUpdate(ctx, r.Client, bc, func() error {
			log.Info("Ensuring Resource", "Kind", bc.TypeMeta.Kind, "Resource.Namespace", bc.Namespace, "Resource.Name", bc.Name)
			return buildConfigForDrupalSiteNginx(bc, d)
		})
		if err != nil {
			log.Error(err, "Failed to ensure Resource", "Kind", bc.TypeMeta.Kind, "Resource.Namespace", bc.Namespace, "Resource.Name", bc.Name)
			return newApplicationError(err, ErrClientK8s)
		}
		return nil
	case "bc_php":
		bc := &buildv1.BuildConfig{ObjectMeta: metav1.ObjectMeta{Name: "drupal-php-" + d.Name, Namespace: d.Namespace}}
		_, err := controllerruntime.CreateOrUpdate(ctx, r.Client, bc, func() error {
			log.Info("Ensuring Resource", "Kind", bc.TypeMeta.Kind, "Resource.Namespace", bc.Namespace, "Resource.Name", bc.Name)
			return buildConfigForDrupalSitePHP(bc, d)
		})
		if err != nil {
			log.Error(err, "Failed to ensure Resource", "Kind", bc.TypeMeta.Kind, "Resource.Namespace", bc.Namespace, "Resource.Name", bc.Name)
			return newApplicationError(err, ErrClientK8s)
		}
		return nil
	case "deploy_mysql":
		deploy := &appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Name: "drupal-mysql-" + d.Name, Namespace: d.Namespace}}
		_, err := controllerruntime.CreateOrUpdate(ctx, r.Client, deploy, func() error {
			log.Info("Ensuring Resource", "Kind", deploy.TypeMeta.Kind, "Resource.Namespace", deploy.Namespace, "Resource.Name", deploy.Name)
			return deploymentForDrupalSiteMySQL(deploy, d)
		})
		if err != nil {
			log.Error(err, "Failed to ensure Resource", "Kind", deploy.TypeMeta.Kind, "Resource.Namespace", deploy.Namespace, "Resource.Name", deploy.Name)
			return newApplicationError(err, ErrClientK8s)
		}
		return nil
	case "deploy_drupal":
		deploy := &appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Name: "drupal-" + d.Name, Namespace: d.Namespace}}
		_, err := controllerruntime.CreateOrUpdate(ctx, r.Client, deploy, func() error {
			log.Info("Ensuring Resource", "Kind", deploy.TypeMeta.Kind, "Resource.Namespace", deploy.Namespace, "Resource.Name", deploy.Name)
			return deploymentForDrupalSite(deploy, d)
		})
		if err != nil {
			log.Error(err, "Failed to ensure Resource", "Kind", deploy.TypeMeta.Kind, "Resource.Namespace", deploy.Namespace, "Resource.Name", deploy.Name)
			return newApplicationError(err, ErrClientK8s)
		}
		return nil
	case "svc_mysql":
		svc := &corev1.Service{ObjectMeta: metav1.ObjectMeta{Name: "drupal-mysql", Namespace: d.Namespace}}
		_, err := controllerruntime.CreateOrUpdate(ctx, r.Client, svc, func() error {
			log.Info("Ensuring Resource", "Kind", svc.TypeMeta.Kind, "Resource.Namespace", svc.Namespace, "Resource.Name", svc.Name)
			return serviceForDrupalSiteMySQL(svc, d)
		})
		if err != nil {
			log.Error(err, "Failed to ensure Resource", "Kind", svc.TypeMeta.Kind, "Resource.Namespace", svc.Namespace, "Resource.Name", svc.Name)
			return newApplicationError(err, ErrClientK8s)
		}
		return nil
	case "svc_nginx":
		svc := &corev1.Service{ObjectMeta: metav1.ObjectMeta{Name: "drupal-" + d.Name, Namespace: d.Namespace}}
		_, err := controllerruntime.CreateOrUpdate(ctx, r.Client, svc, func() error {
			log.Info("Ensuring Resource", "Kind", svc.TypeMeta.Kind, "Resource.Namespace", svc.Namespace, "Resource.Name", svc.Name)
			return serviceForDrupalSite(svc, d)
		})
		if err != nil {
			log.Error(err, "Failed to ensure Resource", "Kind", svc.TypeMeta.Kind, "Resource.Namespace", svc.Namespace, "Resource.Name", svc.Name)
			return newApplicationError(err, ErrClientK8s)
		}
		return nil
	case "pvc_mysql":
		pvc := &corev1.PersistentVolumeClaim{ObjectMeta: metav1.ObjectMeta{Name: "mysql-pv-claim-" + d.Name, Namespace: d.Namespace}}
		_, err := controllerruntime.CreateOrUpdate(ctx, r.Client, pvc, func() error {
			log.Info("Ensuring Resource", "Kind", pvc.TypeMeta.Kind, "Resource.Namespace", pvc.Namespace, "Resource.Name", pvc.Name)
			return persistentVolumeClaimForMySQL(pvc, d)
		})
		if err != nil {
			log.Error(err, "Failed to ensure Resource", "Kind", pvc.TypeMeta.Kind, "Resource.Namespace", pvc.Namespace, "Resource.Name", pvc.Name)
			return newApplicationError(err, ErrClientK8s)
		}
		return nil
	case "pvc_drupal":
		pvc := &corev1.PersistentVolumeClaim{ObjectMeta: metav1.ObjectMeta{Name: "drupal-pv-claim-" + d.Name, Namespace: d.Namespace}}
		_, err := controllerruntime.CreateOrUpdate(ctx, r.Client, pvc, func() error {
			log.Info("Ensuring Resource", "Kind", pvc.TypeMeta.Kind, "Resource.Namespace", pvc.Namespace, "Resource.Name", pvc.Name)
			return persistentVolumeClaimForDrupalSite(pvc, d)
		})
		if err != nil {
			log.Error(err, "Failed to ensure Resource", "Kind", pvc.TypeMeta.Kind, "Resource.Namespace", pvc.Namespace, "Resource.Name", pvc.Name)
			return newApplicationError(err, ErrClientK8s)
		}
		return nil
	case "route":
		route := &routev1.Route{ObjectMeta: metav1.ObjectMeta{Name: "drupal-" + d.Name, Namespace: d.Namespace}}
		_, err := controllerruntime.CreateOrUpdate(ctx, r.Client, route, func() error {
			log.Info("Ensuring Resource", "Kind", route.TypeMeta.Kind, "Resource.Namespace", route.Namespace, "Resource.Name", route.Name)
			return routeForDrupalSite(route, d)
		})
		if err != nil {
			log.Error(err, "Failed to ensure Resource", "Kind", route.TypeMeta.Kind, "Resource.Namespace", route.Namespace, "Resource.Name", route.Name)
			return newApplicationError(err, ErrClientK8s)
		}
		return nil
	case "site_install_job":
		job := &batchv1.Job{ObjectMeta: metav1.ObjectMeta{Name: "drupal-drush-" + d.Name, Namespace: d.Namespace}}
		_, err := controllerruntime.CreateOrUpdate(ctx, r.Client, job, func() error {
			log.Info("Ensuring Resource", "Kind", job.TypeMeta.Kind, "Resource.Namespace", job.Namespace, "Resource.Name", job.Name)
			return jobForDrupalSiteDrush(job, d)
		})
		if err != nil {
			log.Error(err, "Failed to ensure Resource", "Kind", job.TypeMeta.Kind, "Resource.Namespace", job.Namespace, "Resource.Name", job.Name)
			return newApplicationError(err, ErrClientK8s)
		}
		return nil
	case "cm_php":
		cm := &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "php-fpm-cm-" + d.Name, Namespace: d.Namespace}}
		_, err := controllerruntime.CreateOrUpdate(ctx, r.Client, cm, func() error {
			log.Info("Ensuring Resource", "Kind", cm.TypeMeta.Kind, "Resource.Namespace", cm.Namespace, "Resource.Name", cm.Name)
			return updateConfigMapForPHPFPM(ctx, cm, d, r.Client)
		})
		if err != nil {
			log.Error(err, "Failed to ensure Resource", "Kind", cm.TypeMeta.Kind, "Resource.Namespace", cm.Namespace, "Resource.Name", cm.Name)
			return newApplicationError(err, ErrClientK8s)
		}
		return nil
	case "cm_mysql":
		cm := &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "mysql-cm-" + d.Name, Namespace: d.Namespace}}
		_, err := controllerruntime.CreateOrUpdate(ctx, r.Client, cm, func() error {
			log.Info("Ensuring Resource", "Kind", cm.TypeMeta.Kind, "Resource.Namespace", cm.Namespace, "Resource.Name", cm.Name)
			return updateConfigMapForMySQL(ctx, cm, d, r.Client)
		})
		if err != nil {
			log.Error(err, "Failed to ensure Resource", "Kind", cm.TypeMeta.Kind, "Resource.Namespace", cm.Namespace, "Resource.Name", cm.Name)
			return newApplicationError(err, ErrClientK8s)
		}
		return nil
	case "cm_nginx":
		cm := &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "nginx-cm-" + d.Name, Namespace: d.Namespace}}
		_, err := controllerruntime.CreateOrUpdate(ctx, r.Client, cm, func() error {
			log.Info("Ensuring Resource", "Kind", cm.TypeMeta.Kind, "Resource.Namespace", cm.Namespace, "Resource.Name", cm.Name)
			return updateConfigMapForNginx(ctx, cm, d, r.Client)
		})
		if err != nil {
			log.Error(err, "Failed to ensure Resource", "Kind", cm.TypeMeta.Kind, "Resource.Namespace", cm.Namespace, "Resource.Name", cm.Name)
			return newApplicationError(err, ErrClientK8s)
		}
		return nil
	default:
		return newApplicationError(nil, ErrFunctionDomain)
	}
}

// updateCRorFailReconcile tries to update the Custom Resource and logs any error
func (r *DrupalSiteReconciler) updateCRorFailReconcile(ctx context.Context, log logr.Logger, drp *webservicesv1a1.DrupalSite) (
	reconcile.Result, error) {
	if err := r.Update(ctx, drp); err != nil {
		log.Error(err, fmt.Sprintf("%v failed to update the application", ErrClientK8s))
		return reconcile.Result{}, err
	}
	return reconcile.Result{}, nil
}

// updateCRStatusorFailReconcile tries to update the Custom Resource Status and logs any error
func (r *DrupalSiteReconciler) updateCRStatusorFailReconcile(ctx context.Context, log logr.Logger, drp *webservicesv1a1.DrupalSite) (
	reconcile.Result, error) {
	if err := r.Status().Update(ctx, drp); err != nil {
		log.Error(err, fmt.Sprintf("%v failed to update the application status", ErrClientK8s))
		return reconcile.Result{}, err
	}
	return reconcile.Result{}, nil
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

// isInstallJobCompleted checks if the drush job is successfully completed
func (r *DrupalSiteReconciler) isInstallJobCompleted(ctx context.Context, d *webservicesv1a1.DrupalSite) bool {
	found := &batchv1.Job{}
	jobObject := &batchv1.Job{ObjectMeta: metav1.ObjectMeta{Name: "drupal-drush-" + d.Name, Namespace: d.Namespace}}
	err := r.Get(ctx, types.NamespacedName{Name: jobObject.Name, Namespace: jobObject.Namespace}, found)
	if err == nil {
		if found.Status.Succeeded != 0 {
			return true
		}
	}
	return false
}

// isDrupalSiteReady checks if the drupal site is to ready to serve requests by checking the status of Nginx & PHP pods
func (r *DrupalSiteReconciler) isDrupalSiteReady(ctx context.Context, d *webservicesv1a1.DrupalSite) bool {
	deployment := &appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Name: "drupal-" + d.Name, Namespace: d.Namespace}}
	err1 := r.Get(ctx, types.NamespacedName{Name: deployment.Name, Namespace: deployment.Namespace}, deployment)
	if err1 == nil {
		// Change the implementation here
		if deployment.Status.ReadyReplicas != 0 {
			return true
		}
	}
	return false
}

// siteInstallJobForDrupalSite outputs the command needed for jobForDrupalSiteDrush
func siteInstallJobForDrupalSite() []string {
	// return []string{"sh", "-c", "echo"}
	return []string{"sh", "-c", "drush site-install -y --config-dir=../config/sync --account-name=admin --account-pass=pass --account-mail=admin@example.com"}
}
