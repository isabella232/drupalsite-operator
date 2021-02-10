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

	"github.com/asaskevich/govalidator"
	"github.com/go-logr/logr"
	appsv1 "github.com/openshift/api/apps/v1"
	buildv1 "github.com/openshift/api/build/v1"
	imagev1 "github.com/openshift/api/image/v1"
	routev1 "github.com/openshift/api/route/v1"
	webservicesv1a1 "gitlab.cern.ch/drupal/paas/drupalsite-operator/api/v1alpha1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/utils/pointer"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

const (
	// finalizerStr string that is going to added to every DrupalSite created
	finalizerStr          = "controller.drupalsite.webservices.cern.ch"
	productionEnvironment = "production"
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
func ensureSpecFinalizer(drp *webservicesv1a1.DrupalSite) (update bool) {
	if !contains(drp.GetFinalizers(), finalizerStr) {
		drp.SetFinalizers(append(drp.GetFinalizers(), finalizerStr))
		update = true
	}
	return
}

/*
ensureResources ensures the presence of all the resources that the DrupalSite needs to serve content, apart from the ingress Route.
This includes BuildConfigs/ImageStreams, DB, PVC, PHP/Nginx deployment + service, site install job.
*/
func (r *DrupalSiteReconciler) ensureResources(drp *webservicesv1a1.DrupalSite, log logr.Logger) (transientErr reconcileError) {
	ctx := context.TODO()

	// 1. BuildConfigs and ImageStreams

	if len(drp.Spec.Environment.ExtraConfigRepo) > 0 {
		if transientErr := r.ensureResourceX(ctx, drp, "is_s2i", log); transientErr != nil {
			return transientErr.Wrap("%v: for S2I SiteBuilder ImageStream")
		}
		if transientErr := r.ensureResourceX(ctx, drp, "bc_s2i", log); transientErr != nil {
			return transientErr.Wrap("%v: for S2I SiteBuilder BuildConfig")
		}
	}
	if transientErr := r.ensureResourceX(ctx, drp, "is_php", log); transientErr != nil {
		return transientErr.Wrap("%v: for PHP ImageStream")
	}
	if transientErr := r.ensureResourceX(ctx, drp, "is_nginx", log); transientErr != nil {
		return transientErr.Wrap("%v: for Nginx ImageStream")
	}
	if transientErr := r.ensureResourceX(ctx, drp, "bc_php", log); transientErr != nil {
		return transientErr.Wrap("%v: for PHP BuildConfig")
	}
	if transientErr := r.ensureResourceX(ctx, drp, "bc_nginx", log); transientErr != nil {
		return transientErr.Wrap("%v: for Nginx BuildConfig")
	}

	// 2. Data layer

	if transientErr := r.ensureResourceX(ctx, drp, "pvc", log); transientErr != nil {
		return transientErr.Wrap("%v: for PVC")
	}
	if transientErr := r.ensureResourceX(ctx, drp, "dc_mysql", log); transientErr != nil {
		return transientErr.Wrap("%v: for Mysql DC")
	}
	if transientErr := r.ensureResourceX(ctx, drp, "svc_mysql", log); transientErr != nil {
		return transientErr.Wrap("%v: for Mysql SVC")
	}

	// 3. Serving layer

	if transientErr := r.ensureResourceX(ctx, drp, "fpm_cm", log); transientErr != nil {
		return transientErr.Wrap("%v: for PHP-FPM CM")
	}
	if transientErr := r.ensureResourceX(ctx, drp, "dc_drupal", log); transientErr != nil {
		return transientErr.Wrap("%v: for Drupal DC")
	}
	if transientErr := r.ensureResourceX(ctx, drp, "svc_nginx", log); transientErr != nil {
		return transientErr.Wrap("%v: for Nginx SVC")
	}

	// 4. Ingress

	if transientErr := r.ensureResourceX(ctx, drp, "site_install_job", log); transientErr != nil {
		return transientErr.Wrap("%v: for site install Job")
	}
	return nil
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
	return map[string]string{"CRD": "drupalSite", "drupalSite_cr": name}
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
			Name: imageStreamForDrupalSiteBuilderS2I(d).Name + ":" + d.Spec.DrupalVersion,
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
					Name: imageStreamForDrupalSiteBuilderS2I(d).Name + ":" + d.Spec.DrupalVersion,
				},
			},
		}
	}
	return buildv1.BuildTriggerPolicy{
		Type: buildv1.ConfigChangeBuildTriggerType,
	}
}

// imageStreamForDrupalSiteBuilderS2I returns a ImageStream object for Drupal SiteBuilder S2I
func imageStreamForDrupalSiteBuilderS2I(d *webservicesv1a1.DrupalSite) *imagev1.ImageStream {
	ls := labelsForDrupalSite(d.Name)
	ls["app"] = "site-builder"
	is := createImageStream("drupal-site-builder-s2i-"+d.Name, d.Namespace, ls)
	addOwnerRefToObject(is, asOwner(d))
	return is
}

// imageStreamForDrupalSitePHP returns a ImageStream object for Drupal PHP
func imageStreamForDrupalSitePHP(d *webservicesv1a1.DrupalSite) *imagev1.ImageStream {
	ls := labelsForDrupalSite(d.Name)
	ls["app"] = "php"
	is := createImageStream("drupal-php-"+d.Name, d.Namespace, ls)
	addOwnerRefToObject(is, asOwner(d))
	return is
}

// imageStreamForDrupalSiteNginx returns a ImageStream object for Drupal Nginx
func imageStreamForDrupalSiteNginx(d *webservicesv1a1.DrupalSite) *imagev1.ImageStream {
	ls := labelsForDrupalSite(d.Name)
	ls["app"] = "nginx"
	is := createImageStream("drupal-nginx-"+d.Name, d.Namespace, ls)
	addOwnerRefToObject(is, asOwner(d))
	return is
}

func createImageStream(name, namespace string, labels map[string]string) *imagev1.ImageStream {
	return &imagev1.ImageStream{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
			Labels:    labels,
		},
		Spec: imagev1.ImageStreamSpec{
			LookupPolicy: imagev1.ImageLookupPolicy{
				Local: true,
			},
		},
	}
}

// buildConfigForDrupalSiteBuilderS2I returns a BuildConfig object for Drupal SiteBuilder S2I
func buildConfigForDrupalSiteBuilderS2I(d *webservicesv1a1.DrupalSite) *buildv1.BuildConfig {
	ls := labelsForDrupalSite(d.Name)
	ls["app"] = "site-builder"
	objectName := "drupal-site-builder-s2i-" + d.Name

	bc := &buildv1.BuildConfig{
		ObjectMeta: metav1.ObjectMeta{
			Name:      objectName,
			Namespace: d.Namespace,
			Labels:    ls,
		},
		Spec: buildv1.BuildConfigSpec{
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
						Name: objectName + ":" + d.Spec.DrupalVersion,
					},
				},
			},
			Triggers: []buildv1.BuildTriggerPolicy{
				{
					Type: buildv1.ConfigChangeBuildTriggerType,
				},
			},
		},
	}
	// Set DrupalSite instance as the owner and controller
	// ctrl.SetControllerReference(d, dep, r.Scheme)
	// Add owner reference
	addOwnerRefToObject(bc, asOwner(d))
	return bc
}

// buildConfigForDrupalSitePHP returns a BuildConfig object for Drupal PHP
func buildConfigForDrupalSitePHP(d *webservicesv1a1.DrupalSite) *buildv1.BuildConfig {
	ls := labelsForDrupalSite(d.Name)
	ls["app"] = "php"
	objectName := "drupal-php-" + d.Name

	bc := &buildv1.BuildConfig{
		ObjectMeta: metav1.ObjectMeta{
			Name:      objectName,
			Namespace: d.Namespace,
			Labels:    ls,
		},
		Spec: buildv1.BuildConfigSpec{
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
						Name: objectName + ":" + d.Spec.DrupalVersion,
					},
				},
			},
			Triggers: []buildv1.BuildTriggerPolicy{
				triggersForBuildConfigs(d),
			},
		},
	}
	// Set DrupalSite instance as the owner and controller
	// ctrl.SetControllerReference(d, dep, r.Scheme)
	// Add owner reference
	addOwnerRefToObject(bc, asOwner(d))
	return bc
}

// buildConfigForDrupalSiteNginx returns a BuildConfig object for Drupal Nginx
func buildConfigForDrupalSiteNginx(d *webservicesv1a1.DrupalSite) *buildv1.BuildConfig {
	ls := labelsForDrupalSite(d.Name)
	ls["app"] = "nginx"
	objectName := "drupal-nginx-" + d.Name

	bc := &buildv1.BuildConfig{
		ObjectMeta: metav1.ObjectMeta{
			Name:      objectName,
			Namespace: d.Namespace,
			Labels:    ls,
		},
		Spec: buildv1.BuildConfigSpec{
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
						Name: objectName + ":" + d.Spec.DrupalVersion,
					},
				},
			},
			Triggers: []buildv1.BuildTriggerPolicy{
				triggersForBuildConfigs(d),
			},
		},
	}
	// Set DrupalSite instance as the owner and controller
	// ctrl.SetControllerReference(d, dep, r.Scheme)
	// Add owner reference
	addOwnerRefToObject(bc, asOwner(d))
	return bc
}

// deploymentConfigForDrupalSiteMySQL returns a DeploymentConfig object for MySQL
func deploymentConfigForDrupalSiteMySQL(d *webservicesv1a1.DrupalSite) *appsv1.DeploymentConfig {
	ls := labelsForDrupalSite(d.Name)
	ls["app"] = "mysql"
	objectName := "drupal-mysql-" + d.Name

	dep := &appsv1.DeploymentConfig{
		ObjectMeta: metav1.ObjectMeta{
			Name:      objectName,
			Namespace: d.Namespace,
		},
		Spec: appsv1.DeploymentConfigSpec{
			Replicas: 1,
			Selector: ls,
			Template: &corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: ls,
				},
				Spec: corev1.PodSpec{
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
						VolumeMounts: []corev1.VolumeMount{{
							Name:      "mysql-persistent-storage",
							MountPath: "/var/lib/mysql",
						}},
					}},
					Volumes: []corev1.Volume{{
						Name:         "mysql-persistent-storage",
						VolumeSource: corev1.VolumeSource{EmptyDir: &corev1.EmptyDirVolumeSource{}},
					}},
				},
			},
		},
	}
	// Set DrupalSite instance as the owner and controller
	// ctrl.SetControllerReference(d, dep, r.Scheme)
	// Add owner reference
	addOwnerRefToObject(dep, asOwner(d))
	return dep
}

// deploymentConfigForDrupalSite defines the server runtime deployment of a DrupalSite
func deploymentConfigForDrupalSite(d *webservicesv1a1.DrupalSite) *appsv1.DeploymentConfig {
	ls := labelsForDrupalSite(d.Name)
	ls["app"] = "drupal"
	objectName := "drupal-deployment-" + d.Name

	dep := &appsv1.DeploymentConfig{
		ObjectMeta: metav1.ObjectMeta{
			Name:      objectName,
			Namespace: d.Namespace,
		},
		Spec: appsv1.DeploymentConfigSpec{
			Replicas: 1,
			Selector: ls,
			Template: &corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: ls,
				},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{{
						Image:           imageStreamForDrupalSiteNginx(d).Name + ":" + d.Spec.DrupalVersion,
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
						VolumeMounts: []corev1.VolumeMount{{
							Name:      "drupal-directory-" + d.Name,
							MountPath: "/drupal-data",
						}},
					},
						{
							Image:           imageStreamForDrupalSitePHP(d).Name + ":" + d.Spec.DrupalVersion,
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
									Name:      "config-volume",
									MountPath: "/usr/local/etc/php-fpm.d/www.conf",
									SubPath:   "www.conf",
								}},
						}},
					Volumes: []corev1.Volume{{
						Name: "drupal-directory-" + d.Name,
						VolumeSource: corev1.VolumeSource{
							PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{
								ClaimName: "drupal-pv-claim-" + d.Name,
							},
						}},
						{
							Name: "config-volume",
							VolumeSource: corev1.VolumeSource{
								ConfigMap: &corev1.ConfigMapVolumeSource{
									LocalObjectReference: corev1.LocalObjectReference{
										Name: "php-fpm-cm-" + d.Name,
									},
								},
							},
						},
					},
				},
			},
			Triggers: appsv1.DeploymentTriggerPolicies{
				appsv1.DeploymentTriggerPolicy{
					Type: appsv1.DeploymentTriggerOnImageChange,
					ImageChangeParams: &appsv1.DeploymentTriggerImageChangeParams{
						Automatic:      true,
						ContainerNames: []string{"nginx"},
						From: corev1.ObjectReference{
							Kind: "ImageStreamTag",
							Name: imageStreamForDrupalSiteNginx(d).Name + ":" + d.Spec.DrupalVersion,
						},
					},
				},
				appsv1.DeploymentTriggerPolicy{
					Type: appsv1.DeploymentTriggerOnImageChange,
					ImageChangeParams: &appsv1.DeploymentTriggerImageChangeParams{
						Automatic:      true,
						ContainerNames: []string{"php-fpm"},
						From: corev1.ObjectReference{
							Kind: "ImageStreamTag",
							Name: imageStreamForDrupalSitePHP(d).Name + ":" + d.Spec.DrupalVersion,
						},
					},
				},
			},
		},
	}
	// Set DrupalSite instance as the owner and controller
	// ctrl.SetControllerReference(d, dep, r.Scheme)
	// Add owner reference
	addOwnerRefToObject(dep, asOwner(d))
	return dep
}

// persistentVolumeClaimForDrupalSite returns a PVC object
func persistentVolumeClaimForDrupalSite(d *webservicesv1a1.DrupalSite) *corev1.PersistentVolumeClaim {
	// ls := labelsForDrupalSite(d.Name)

	pvc := &corev1.PersistentVolumeClaim{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "drupal-pv-claim-" + d.Name,
			Namespace: d.Namespace,
		},
		Spec: corev1.PersistentVolumeClaimSpec{
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
		},
	}
	// Set DrupalSite instance as the owner and controller
	// ctrl.SetControllerReference(d, pvc, r.Scheme)
	// Add owner reference
	addOwnerRefToObject(pvc, asOwner(d))
	return pvc
}

// serviceForDrupalSite returns a service object for Nginx
func serviceForDrupalSite(d *webservicesv1a1.DrupalSite) *corev1.Service {
	ls := labelsForDrupalSite(d.Name)
	ls["app"] = "drupal"

	svc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "drupal-" + d.Name,
			Namespace: d.Namespace,
		},
		Spec: corev1.ServiceSpec{
			Selector: ls,
			Ports: []corev1.ServicePort{{
				TargetPort: intstr.FromInt(8080),
				Name:       "nginx",
				Port:       80,
				Protocol:   "TCP",
			}},
		},
	}
	// Set DrupalSite instance as the owner and controller
	// ctrl.SetControllerReference(d, svc, r.Scheme)
	// Add owner reference
	addOwnerRefToObject(svc, asOwner(d))
	return svc
}

// serviceForDrupalSiteMySQL returns a service object for MySQL
func serviceForDrupalSiteMySQL(d *webservicesv1a1.DrupalSite) *corev1.Service {
	ls := labelsForDrupalSite(d.Name)
	ls["app"] = "mysql"

	svc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "drupal-mysql",
			Namespace: d.Namespace,
		},
		Spec: corev1.ServiceSpec{
			Selector: ls,
			Ports: []corev1.ServicePort{{
				TargetPort: intstr.FromInt(3306),
				Name:       "mysql",
				Port:       3306,
				Protocol:   "TCP",
			}},
		},
	}
	// Set DrupalSite instance as the owner and controller
	// ctrl.SetControllerReference(d, svc, r.Scheme)
	// Add owner reference
	addOwnerRefToObject(svc, asOwner(d))
	return svc
}

// routeForDrupalSite returns a route object
func routeForDrupalSite(d *webservicesv1a1.DrupalSite) *routev1.Route {
	// ls := labelsForDrupalSite(d.Name)
	var env string
	if d.Spec.Environment.Name == productionEnvironment {
		env = ""
	} else {
		env = d.Spec.Environment.Name + "."
	}
	route := &routev1.Route{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "drupal" + d.Name,
			Namespace: d.Namespace,
		},
		Spec: routev1.RouteSpec{
			Host: env + d.Name + "." + ClusterName + ".cern.ch",
			To: routev1.RouteTargetReference{
				Kind:   "Service",
				Name:   "drupal-nginx",
				Weight: pointer.Int32Ptr(100),
			},
			Port: &routev1.RoutePort{
				TargetPort: intstr.FromInt(8080),
			},
		},
	}
	// Set DrupalSite instance as the owner and controller
	// ctrl.SetControllerReference(d, svc, r.Scheme)
	// Add owner reference
	addOwnerRefToObject(route, asOwner(d))
	return route
}

// jobForDrupalSiteDrush returns a job object thats runs drush
func jobForDrupalSiteDrush(d *webservicesv1a1.DrupalSite) *batchv1.Job {
	ls := labelsForDrupalSite(d.Name)
	ls["job"] = "drush"
	objectName := "drupal-drush-" + d.Name

	job := &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{
			Name:      objectName,
			Namespace: d.Namespace,
		},
		Spec: batchv1.JobSpec{
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: ls,
				},
				Spec: corev1.PodSpec{
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
						Image:           imageStreamForDrupalSitePHP(d).Name + ":" + d.Spec.DrupalVersion,
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
				},
			},
		},
	}
	// Set DrupalSite instance as the owner and controller
	// ctrl.SetControllerReference(d, dep, r.Scheme)
	// Add owner reference
	addOwnerRefToObject(job, asOwner(d))
	return job
}

// configMapForPHPFPM returns a job object thats runs drush
func configMapForPHPFPM(d *webservicesv1a1.DrupalSite, log logr.Logger) *corev1.ConfigMap {
	ls := labelsForDrupalSite(d.Name)
	ls["app"] = "php"

	content, err := ioutil.ReadFile("config/www.conf")
	if err != nil {
		log.Error(err, fmt.Sprintf("read failed"))
		return nil
	}

	cm := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "php-fpm-cm-" + d.Name,
			Namespace: d.Namespace,
		},
		Data: map[string]string{
			"www.conf": string(content),
		},
	}
	// Set DrupalSite instance as the owner and controller
	// ctrl.SetControllerReference(d, dep, r.Scheme)
	// Add owner reference
	addOwnerRefToObject(cm, asOwner(d))
	return cm
}

// createResource creates a given resource passed as an argument
func createResource(ctx context.Context, res client.Object, name string, namespace string, r *DrupalSiteReconciler, log logr.Logger) (transientErr reconcileError) {
	deleteRecreate := func() error {
		err := r.Delete(ctx, res)
		if err != nil {
			return err
		}
		return r.Create(ctx, res)
	}
	err := r.Get(ctx, types.NamespacedName{Name: name, Namespace: namespace}, res)
	if err != nil && errors.IsNotFound(err) {
		log.Info("Creating Resource", "Resource.Namespace", namespace, "Resource.Name", name)
		err := r.Create(ctx, res)
		if err != nil {
			switch {
			case apierrors.IsAlreadyExists(err):
				err = deleteRecreate()
				if err != nil {
					log.Error(err, "Failed to create new Resource", "Resource.Namespace", namespace, "Resource.Name", name)
					return newApplicationError(err, ErrClientK8s)
				}
			default:
				return newApplicationError(err, ErrClientK8s)
			}
		}
	}
	return nil
}

/*
ensureResourceX ensure the requested resource is created, with the following valid values
	- dc_mysql: DeploymentConfig for MySQL
	- svc_mysql: Service for MySQL
	- pvc: PersistentVolume for the drupalsite
	- site_install_job: Kubernetes Job for the drush site-install
	- is_base: ImageStream for sitebuilder-base
	- is_s2i: ImageStream for S2I sitebuilder
	- is_php: ImageStream for PHP
	- is_nginx: ImageStream for Nginx
	- bc_s2i: BuildConfig for S2I sitebuilder
	- bc_php: BuildConfig for PHP
	- bc_nginx: BuildConfig for Nginx
	- dc_drupal: DeploymentConfig for Nginx & PHP-FPM
	- svc_nginx: Service for Nginx
	- fpm_cm: ConfigMap for PHP-FPM
	- route: Route for the drupalsite
*/
func (r *DrupalSiteReconciler) ensureResourceX(ctx context.Context, d *webservicesv1a1.DrupalSite, resType string, log logr.Logger) (transientErr reconcileError) {
	switch resType {
	case "is_s2i":
		res := imageStreamForDrupalSiteBuilderS2I(d)
		return createResource(ctx, res, res.Name, res.Namespace, r, log)
	case "is_nginx":
		res := imageStreamForDrupalSiteNginx(d)
		return createResource(ctx, res, res.Name, res.Namespace, r, log)
	case "is_php":
		res := imageStreamForDrupalSitePHP(d)
		return createResource(ctx, res, res.Name, res.Namespace, r, log)
	case "bc_s2i":
		res := buildConfigForDrupalSiteBuilderS2I(d)
		return createResource(ctx, res, res.Name, res.Namespace, r, log)
	case "bc_nginx":
		res := buildConfigForDrupalSiteNginx(d)
		return createResource(ctx, res, res.Name, res.Namespace, r, log)
	case "bc_php":
		res := buildConfigForDrupalSitePHP(d)
		return createResource(ctx, res, res.Name, res.Namespace, r, log)
	case "dc_mysql":
		res := deploymentConfigForDrupalSiteMySQL(d)
		return createResource(ctx, res, res.Name, res.Namespace, r, log)
	case "dc_drupal":
		res := deploymentConfigForDrupalSite(d)
		return createResource(ctx, res, res.Name, res.Namespace, r, log)
	case "svc_mysql":
		res := serviceForDrupalSiteMySQL(d)
		return createResource(ctx, res, res.Name, res.Namespace, r, log)
	case "svc_nginx":
		res := serviceForDrupalSite(d)
		return createResource(ctx, res, res.Name, res.Namespace, r, log)
	case "pvc":
		res := persistentVolumeClaimForDrupalSite(d)
		return createResource(ctx, res, res.Name, res.Namespace, r, log)
	case "route":
		res := routeForDrupalSite(d)
		return createResource(ctx, res, res.Name, res.Namespace, r, log)
	case "site_install_job":
		res := jobForDrupalSiteDrush(d)
		return createResource(ctx, res, res.Name, res.Namespace, r, log)
	case "fpm_cm":
		res := configMapForPHPFPM(d, log)
		if res == nil {
			return newApplicationError(nil, ErrFunctionDomain)
		}
		return createResource(ctx, res, res.Name, res.Namespace, r, log)
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
	jobObject := jobForDrupalSiteDrush(d)
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
	deploymentConfig := deploymentConfigForDrupalSite(d)
	err1 := r.Get(ctx, types.NamespacedName{Name: deploymentConfig.Name, Namespace: deploymentConfig.Namespace}, deploymentConfig)
	if err1 == nil {
		// Change the implementation here
		if deploymentConfig.Status.ReadyReplicas != 0 {
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
