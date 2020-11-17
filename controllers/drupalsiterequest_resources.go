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
	"os"

	"github.com/asaskevich/govalidator"
	"github.com/go-logr/logr"
	appsv1 "github.com/openshift/api/apps/v1"
	routev1 "github.com/openshift/api/route/v1"
	"github.com/prometheus/common/log"
	webservicescernchv1alpha1 "gitlab.cern.ch/drupal/paas/drupalsite-operator/api/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/utils/pointer"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

const (
	// finalizerStr string that is going to added to every DrupalSiteRequest created
	finalizerStr = "finalizer.controller-drupalsiterequest.webservices.cern.ch"
)

func validateSpec(appSpec webservicescernchv1alpha1.DrupalSiteRequestSpec) error {
	_, err := govalidator.ValidateStruct(appSpec)
	if err != nil {
		return err
	}
	// _, err = govalidator.ValidateStruct(appSpec.InitialOwner)
	return err
}

// ensureStatusInit ensures that the status have been initialized, returns true if it is required an update
func ensureStatusInit(app *webservicescernchv1alpha1.DrupalSiteRequest) (update bool) {
	if !app.Status.Created {
		app.Status.Created = true
		return true
	}
	return false
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
func ensureSpecFinalizer(app *webservicescernchv1alpha1.DrupalSiteRequest) (update bool) {
	if !contains(app.GetFinalizers(), finalizerStr) {
		app.SetFinalizers(append(app.GetFinalizers(), finalizerStr))
		update = true
	}
	if app.Spec.DisplayName == "" {
		app.Spec.DisplayName = app.DisplayNameConvention(os.Getenv("CLUSTER_NAME"))
		update = true
	}
	return
}

// labelsForDrupalSiterequest returns the labels for selecting the resources
// belonging to the given drupalSiteRequest CR name.
func labelsForDrupalSiterequest(name string) map[string]string {
	return map[string]string{"app": "drupalSiteRequest", "drupalSiteRequest_cr": name}
}

// deploymentConfigForDrupalSiteRequestMySQL returns a drupalSiteRequest DeploymentConfigMySQL object
func deploymentConfigForDrupalSiteRequestMySQL(d *webservicescernchv1alpha1.DrupalSiteRequest) *appsv1.DeploymentConfig {
	ls := labelsForDrupalSiterequest(d.Name)
	objectName := "drupal-mysql-" + d.Name

	dep := &appsv1.DeploymentConfig{
		ObjectMeta: metav1.ObjectMeta{
			Name:      objectName,
			Namespace: d.Namespace,
		},
		Spec: appsv1.DeploymentConfigSpec{
			Selector: ls,
			Template: &corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: ls,
				},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{{
						Image:           "mysql:5.7",
						Name:            "mysql",
						ImagePullPolicy: "Always",
						Ports: []corev1.ContainerPort{{
							ContainerPort: 3306,
							Name:          "mysql",
							Protocol:      "TCP",
						}},
						Env: []corev1.EnvVar{
							corev1.EnvVar{
								Name:  "MYSQL_DATABASE",
								Value: "drupal",
							},
							corev1.EnvVar{
								Name: "MYSQL_ROOT_PASSWORD",
								ValueFrom: &corev1.EnvVarSource{
									SecretKeyRef: &corev1.SecretKeySelector{
										Key: "password",
										LocalObjectReference: corev1.LocalObjectReference{
											Name: objectName,
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
	// Set DrupalSiteRequest instance as the owner and controller
	// ctrl.SetControllerReference(d, dep, r.Scheme)
	return dep
}

// deploymentConfigForDrupalSiteRequestNginx returns a drupalSiteRequest DeploymentConfigNginx object
func deploymentConfigForDrupalSiteRequestNginx(d *webservicescernchv1alpha1.DrupalSiteRequest) *appsv1.DeploymentConfig {
	ls := labelsForDrupalSiterequest(d.Name)

	dep := &appsv1.DeploymentConfig{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "drupal-nginx-" + d.Name,
			Namespace: d.Namespace,
		},
		Spec: appsv1.DeploymentConfigSpec{
			Selector: ls,
			Template: &corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: ls,
				},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{{
						Image:           "gitlab-registry.cern.ch/drupal/paas/drupalsite-operator/nginx:drupal-" + d.Spec.DrupalVersion,
						Name:            "nginx",
						ImagePullPolicy: "Always",
						Ports: []corev1.ContainerPort{{
							ContainerPort: 8080,
							Name:          "nginx",
							Protocol:      "TCP",
						}},
						Env: []corev1.EnvVar{
							corev1.EnvVar{
								Name:  "DRUPAL_SHARED_VOLUME",
								Value: "/drupal-data",
							},
							corev1.EnvVar{
								Name:  "DB_HOST",
								Value: "drupal-mysql-" + d.Name,
							},
							corev1.EnvVar{
								Name:  "DB_PORT",
								Value: "3306",
							},
							corev1.EnvVar{
								Name:  "DB_NAME",
								Value: "drupal",
							},
							corev1.EnvVar{
								Name:  "DB_USER",
								Value: "root",
							},
							corev1.EnvVar{
								Name: "DB_PASSWORD",
								ValueFrom: &corev1.EnvVarSource{
									SecretKeyRef: &corev1.SecretKeySelector{
										Key: "password",
										LocalObjectReference: corev1.LocalObjectReference{
											Name: "mysql-pass-" + d.Name,
										},
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
	// Set DrupalSiteRequest instance as the owner and controller
	// ctrl.SetControllerReference(d, dep, r.Scheme)
	return dep
}

// deploymentConfigForDrupalSiteRequestPHP returns a drupalSiteRequest DeploymentConfigPHP object
func deploymentConfigForDrupalSiteRequestPHP(d *webservicescernchv1alpha1.DrupalSiteRequest) *appsv1.DeploymentConfig {
	ls := labelsForDrupalSiterequest(d.Name)

	dep := &appsv1.DeploymentConfig{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "drupal-php-" + d.Name,
			Namespace: d.Namespace,
		},
		Spec: appsv1.DeploymentConfigSpec{
			Selector: ls,
			Template: &corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: ls,
				},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{{
						Image:           "gitlab-registry.cern.ch/drupal/paas/drupalsite-operator/php-fpm:drupal-" + d.Spec.DrupalVersion,
						Name:            "php-fpm",
						ImagePullPolicy: "Always",
						Ports: []corev1.ContainerPort{{
							ContainerPort: 9000,
							Name:          "php-fpm",
							Protocol:      "TCP",
						}},
						Env: []corev1.EnvVar{
							corev1.EnvVar{
								Name:  "DRUPAL_SHARED_VOLUME",
								Value: "/drupal-data",
							},
							corev1.EnvVar{
								Name:  "DB_HOST",
								Value: "drupal-mysql-" + d.Name,
							},
							corev1.EnvVar{
								Name:  "DB_PORT",
								Value: "3306",
							},
							corev1.EnvVar{
								Name:  "DB_NAME",
								Value: "drupal",
							},
							corev1.EnvVar{
								Name:  "DB_USER",
								Value: "root",
							},
							corev1.EnvVar{
								Name: "DB_PASSWORD",
								ValueFrom: &corev1.EnvVarSource{
									SecretKeyRef: &corev1.SecretKeySelector{
										Key: "password",
										LocalObjectReference: corev1.LocalObjectReference{
											Name: "mysql-pass-" + d.Name,
										},
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
	// Set DrupalSiteRequest instance as the owner and controller
	// ctrl.SetControllerReference(d, dep, r.Scheme)
	return dep
}

// persistentVolumeClaimForDrupalSiteRequest returns a drupalSiteRequest DeploymentConfigPHP object
func persistentVolumeClaimForDrupalSiteRequest(d *webservicescernchv1alpha1.DrupalSiteRequest) *corev1.PersistentVolumeClaim {
	ls := labelsForDrupalSiterequest(d.Name)

	pvc := &corev1.PersistentVolumeClaim{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "drupal-pv-claim-" + d.Name,
			Namespace: d.Namespace,
		},
		Spec: corev1.PersistentVolumeClaimSpec{
			Selector: &metav1.LabelSelector{
				MatchLabels: ls,
			},
			StorageClassName: pointer.StringPtr("cephfs-no-backup"),
			AccessModes:      []corev1.PersistentVolumeAccessMode{"ReadWriteOnce"},
			Resources: corev1.ResourceRequirements{
				Requests: corev1.ResourceList{
					corev1.ResourceName(corev1.ResourceStorage): resource.MustParse("5Gi"),
				},
			},
		},
	}
	// Set DrupalSiteRequest instance as the owner and controller
	// ctrl.SetControllerReference(d, pvc, r.Scheme)
	return pvc
}

// serviceForDrupalSiteRequestPHP returns a drupalSiteRequest servicePHP object
func serviceForDrupalSiteRequestPHP(d *webservicescernchv1alpha1.DrupalSiteRequest) *corev1.Service {
	ls := labelsForDrupalSiterequest(d.Name)

	svc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "php-fpm",
			Namespace: d.Namespace,
		},
		Spec: corev1.ServiceSpec{
			Selector: ls,
			Ports: []corev1.ServicePort{{
				TargetPort: intstr.FromInt(9000),
				Name:       "php-fpm",
				Port:       80,
				Protocol:   "TCP",
			}},
		},
	}
	// Set DrupalSiteRequest instance as the owner and controller
	// ctrl.SetControllerReference(d, svc, r.Scheme)
	return svc
}

// serviceForDrupalSiteRequestNginx returns a drupalSiteRequest servicePHP object
func serviceForDrupalSiteRequestNginx(d *webservicescernchv1alpha1.DrupalSiteRequest) *corev1.Service {
	ls := labelsForDrupalSiterequest(d.Name)

	svc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "drupal-nginx" + d.Name,
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
	// Set DrupalSiteRequest instance as the owner and controller
	// ctrl.SetControllerReference(d, svc, r.Scheme)
	return svc
}

// serviceForDrupalSiteRequestMySQL returns a drupalSiteRequest servicePHP object
func serviceForDrupalSiteRequestMySQL(d *webservicescernchv1alpha1.DrupalSiteRequest) *corev1.Service {
	ls := labelsForDrupalSiterequest(d.Name)

	svc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "drupal-mysql" + d.Name,
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
	// Set DrupalSiteRequest instance as the owner and controller
	// ctrl.SetControllerReference(d, svc, r.Scheme)
	return svc
}

// routeForDrupalSiteRequest returns a drupalSiteRequest route object
func routeForDrupalSiteRequest(d *webservicescernchv1alpha1.DrupalSiteRequest) *routev1.Route {
	// ls := labelsForDrupalSiterequest(d.Name)

	route := &routev1.Route{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "drupal-mysql" + d.Name,
			Namespace: d.Namespace,
		},
		Spec: routev1.RouteSpec{
			Host: d.Name + "-drupaltemplate.clustername.cern.ch",
			To: routev1.RouteTargetReference{
				Kind:   "Service",
				Name:   "drupal-nginx" + d.Name,
				Weight: pointer.Int32Ptr(100),
			},
			Port: &routev1.RoutePort{
				TargetPort: intstr.FromInt(8080),
			},
		},
	}
	// Set DrupalSiteRequest instance as the owner and controller
	// ctrl.SetControllerReference(d, svc, r.Scheme)
	return route
}

func (r *DrupalSiteRequestReconciler) ensureDeploymentConfig(ctx context.Context, d *webservicescernchv1alpha1.DrupalSiteRequest, dep *appsv1.DeploymentConfig) (transientErr reconcileError) {
	// dep := deploymentConfigForDrupalSiteRequestMySQL(d)
	// dep *appsv1.DeploymentConfig
	deleteRecreate := func() error {
		err := r.Delete(ctx, dep)
		if err != nil {
			return err
		}
		return r.Create(ctx, dep)
	}
	log.Info("Creating DeploymentConfig", "DeploymentConfig.Namespace", dep.Namespace, "DeploymentConfig.Name", dep.Name)
	err := r.Create(ctx, dep)
	if err != nil {
		switch {
		case apierrors.IsAlreadyExists(err):
			err = deleteRecreate()
			if err != nil {
				log.Error(err, "Failed to create new DeploymentConfig", "DeploymentConfig.Namespace", dep.Namespace, "DeploymentConfig.Name", dep.Name)
				return newApplicationError(err, ErrClientK8s)
			}
		default:
			return newApplicationError(err, ErrClientK8s)
		}
	}
	// Set DrupalSiteRequest instance as the owner and controller
	ctrl.SetControllerReference(d, dep, r.Scheme)
	return nil
}

func (r *DrupalSiteRequestReconciler) ensureService(ctx context.Context, d *webservicescernchv1alpha1.DrupalSiteRequest, svc *corev1.Service) (transientErr reconcileError) {
	deleteRecreate := func() error {
		err := r.Delete(ctx, svc)
		if err != nil {
			return err
		}
		return r.Create(ctx, svc)
	}
	log.Info("Creating Service", "Service.Namespace", svc.Namespace, "Service.Name", svc.Name)
	err := r.Create(ctx, svc)
	if err != nil {
		switch {
		case apierrors.IsAlreadyExists(err):
			err = deleteRecreate()
			if err != nil {
				log.Error(err, "Failed to create new Service", "Service.Namespace", svc.Namespace, "Service.Name", svc.Name)
				return newApplicationError(err, ErrClientK8s)
			}
		default:
			return newApplicationError(err, ErrClientK8s)
		}
	}
	// Set DrupalSiteRequest instance as the owner and controller
	ctrl.SetControllerReference(d, svc, r.Scheme)
	return nil
}

func (r *DrupalSiteRequestReconciler) ensurePersistentVolumeClaim(ctx context.Context, d *webservicescernchv1alpha1.DrupalSiteRequest, pvc *corev1.PersistentVolumeClaim) (transientErr reconcileError) {
	deleteRecreate := func() error {
		err := r.Delete(ctx, pvc)
		if err != nil {
			return err
		}
		return r.Create(ctx, pvc)
	}
	log.Info("Creating PVC", "PVC.Namespace", pvc.Namespace, "PVC.Name", pvc.Name)
	err := r.Create(ctx, pvc)
	if err != nil {
		switch {
		case apierrors.IsAlreadyExists(err):
			err = deleteRecreate()
			if err != nil {
				log.Error(err, "Failed to create new PVC", "PVC.Namespace", pvc.Namespace, "PVC.Name", pvc.Name)
				return newApplicationError(err, ErrClientK8s)
			}
		default:
			return newApplicationError(err, ErrClientK8s)
		}
	}
	// Set DrupalSiteRequest instance as the owner and controller
	ctrl.SetControllerReference(d, pvc, r.Scheme)
	return nil
}

func (r *DrupalSiteRequestReconciler) ensureRoute(ctx context.Context, d *webservicescernchv1alpha1.DrupalSiteRequest, route *routev1.Route) (transientErr reconcileError) {
	deleteRecreate := func() error {
		err := r.Delete(ctx, route)
		if err != nil {
			return err
		}
		return r.Create(ctx, route)
	}
	log.Info("Creating Route", "Route.Namespace", route.Namespace, "Route.Name", route.Name)
	err := r.Create(ctx, route)
	if err != nil {
		switch {
		case apierrors.IsAlreadyExists(err):
			err = deleteRecreate()
			if err != nil {
				log.Error(err, "Failed to create new Route", "Route.Namespace", route.Namespace, "Route.Name", route.Name)
				return newApplicationError(err, ErrClientK8s)
			}
		default:
			return newApplicationError(err, ErrClientK8s)
		}
	}
	// Set DrupalSiteRequest instance as the owner and controller
	ctrl.SetControllerReference(d, route, r.Scheme)
	return nil
}

// updateCRorFailReconcile tries to update the Custom Resource and logs any error
func (r *DrupalSiteRequestReconciler) updateCRorFailReconcile(ctx context.Context, log logr.Logger, app *webservicescernchv1alpha1.DrupalSiteRequest) (
	reconcile.Result, error) {
	if err := r.Update(ctx, app); err != nil {
		log.Error(err, fmt.Sprintf("%v failed to update the application", ErrClientK8s))
		return reconcile.Result{}, err
	}
	return reconcile.Result{}, nil
}

// updateCRStatusorFailReconcile tries to update the Custom Resource Status and logs any error
func (r *DrupalSiteRequestReconciler) updateCRStatusorFailReconcile(ctx context.Context, log logr.Logger, app *webservicescernchv1alpha1.DrupalSiteRequest) (
	reconcile.Result, error) {
	if err := r.Status().Update(ctx, app); err != nil {
		log.Error(err, fmt.Sprintf("%v failed to update the application status", ErrClientK8s))
		return reconcile.Result{}, err
	}
	return reconcile.Result{}, nil
}
