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

	"github.com/go-logr/logr"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	webservicescernchv1alpha1 "gitlab.cern.ch/drupal/paas/drupalsite-operator/api/v1alpha1"
)

// deploymentConfigForDrupalSiteRequestMySQL returns a drupalSiteRequest DeploymentConfigMySQL object
func deploymentConfigForDrupalSiteRequestMySQL(d *webservices.cern.chv1alpha1.DrupalSiteRequest) *apps.openshift.iov1.DeploymentConfig {
	ls := labelsForDrupalSiterequest(d.Name)
	objectName := "drupal-mysql-" + d.Name

	dep := &apps.openshift.iov1.DeploymentConfig{
		ObjectMeta: metav1.ObjectMeta{
			Name:      objectName,
			Namespace: d.Namespace,
		},
		Spec: appsv1.DeploymentConfigSpec{
			Selector: &metav1.LabelSelector{
				MatchLabels: ls,
			},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: ls,
				},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{{
						Image:   "mysql:5.7",
						Name:    "mysql",
						ImagePullPolicy: "Always",
						Ports: []corev1.ContainerPort{{
							ContainerPort: 3306,
							Name:          "mysql",
							Protocol: 	   "TCP",
						}},
						Env:   []corev1.EnvVar{
							corev1.EnvVar{
								Name:	"MYSQL_DATABASE",
								Value:	"drupal",
							},
							corev1.EnvVar{
								Name:		"MYSQL_ROOT_PASSWORD",
								ValueFrom: &corev1.EnvVarSource{
									SecretKeyRef:	&corev1.SecretKeySelector{
										Key:	"password",
										Name:	"mysql-pass-" + d.Name,
									}
								}
							}
						},
						VolumeMounts: []corev1.VolumeMount{{
							Name: "mysql-persistent-storage"
							MountPath: "/var/lib/mysql"
						}},
					}},
					Volumes:  []corev1.Volume{{
						Name:	"mysql-persistent-storage",
						EmptyDir: corev1.EmptyDirVolumeSource{},
					}},
				},
			},
		},
	}
	// Set DrupalSiteRequest instance as the owner and controller
	ctrl.SetControllerReference(d, dep, r.Scheme)
	return dep
}

// deploymentConfigForDrupalSiteRequestNginx returns a drupalSiteRequest DeploymentConfigNginx object
func deploymentConfigForDrupalSiteRequestNginx(d *webservices.cern.chv1alpha1.DrupalSiteRequest) *apps.openshift.iov1.DeploymentConfig {
	ls := labelsForDrupalSiterequest(d.Name)

	dep := &apps.openshift.iov1.DeploymentConfig{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "drupal-nginx-" + d.Name,
			Namespace: d.Namespace,
		},
		Spec: appsv1.DeploymentConfigSpec{
			Selector: &metav1.LabelSelector{
				MatchLabels: ls,
			},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: ls,
				},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{{
						Image:   "gitlab-registry.cern.ch/drupal/paas/drupalsite-operator/nginx:drupal-" + d.Spec.drupalVersion,
						Name:    "nginx",
						ImagePullPolicy: "Always",
						Ports: []corev1.ContainerPort{{
							ContainerPort: 8080,
							Name:          "nginx",
							Protocol: 	   "TCP",
						}},
						Env:   []corev1.EnvVar{
							corev1.EnvVar{
								Name:	"DRUPAL_SHARED_VOLUME",
								Value:	"/drupal-data",
							},
							corev1.EnvVar{
								Name:	"DB_HOST",
								Value:	"drupal-mysql-" + d.name,
							},
							corev1.EnvVar{
								Name:	"DB_PORT",
								Value:	"3306",
							},
							corev1.EnvVar{
								Name:	"DB_NAME",
								Value:	"drupal",
							},
							corev1.EnvVar{
								Name:	"DB_USER",
								Value:	"root",
							},
							corev1.EnvVar{
								Name:		"DB_PASSWORD",
								ValueFrom: &corev1.EnvVarSource{
									SecretKeyRef:	&corev1.SecretKeySelector{
										Key:	"password",
										Name:	"mysql-pass-" + d.Name,
									}
								}
							}
						},
						VolumeMounts: []corev1.VolumeMount{{
							Name: "drupal-directory-" + d.name,
							MountPath: "/drupal-data",
						}},
					}},
					Volumes:  []corev1.Volume{{
						Name:	"drupal-directory-" + d.name,
						PersistentVolumeClaim: corev1.PersistentVolumeClaimVolumeSource{
							claimName: "drupal-pv-claim-" + d.name,
						},
					}},
				},
			},
		},
	}
	// Set DrupalSiteRequest instance as the owner and controller
	ctrl.SetControllerReference(d, dep, r.Scheme)
	return dep
}

// deploymentConfigForDrupalSiteRequestPHP returns a drupalSiteRequest DeploymentConfigPHP object
func deploymentConfigForDrupalSiteRequestNginx(d *webservices.cern.chv1alpha1.DrupalSiteRequest) *apps.openshift.iov1.DeploymentConfig {
	ls := labelsForDrupalSiterequest(d.Name)

	dep := &apps.openshift.iov1.DeploymentConfig{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "drupal-php-" + d.Name,
			Namespace: d.Namespace,
		},
		Spec: appsv1.DeploymentConfigSpec{
			Selector: &metav1.LabelSelector{
				MatchLabels: ls,
			},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: ls,
				},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{{
						Image:   "gitlab-registry.cern.ch/drupal/paas/drupalsite-operator/php-fpm:drupal-" + d.Spec.drupalVersion,
						Name:    "php-fpm",
						ImagePullPolicy: "Always",
						Ports: []corev1.ContainerPort{{
							ContainerPort: 9000,
							Name:          "php-fpm",
							Protocol: 	   "TCP",
						}},
						Env:   []corev1.EnvVar{
							corev1.EnvVar{
								Name:	"DRUPAL_SHARED_VOLUME",
								Value:	"/drupal-data",
							},
							corev1.EnvVar{
								Name:	"DB_HOST",
								Value:	"drupal-mysql-" + d.name,
							},
							corev1.EnvVar{
								Name:	"DB_PORT",
								Value:	"3306",
							},
							corev1.EnvVar{
								Name:	"DB_NAME",
								Value:	"drupal",
							},
							corev1.EnvVar{
								Name:	"DB_USER",
								Value:	"root",
							},
							corev1.EnvVar{
								Name:		"DB_PASSWORD",
								ValueFrom: &corev1.EnvVarSource{
									SecretKeyRef:	&corev1.SecretKeySelector{
										Key:	"password",
										Name:	"mysql-pass-" + d.Name,
									}
								}
							}
						},
						VolumeMounts: []corev1.VolumeMount{{
							Name: "drupal-directory-" + d.name,
							MountPath: "/drupal-data",
						}},
					}},
					Volumes:  []corev1.Volume{{
						Name:	"drupal-directory-" + d.name,
						PersistentVolumeClaim: corev1.PersistentVolumeClaimVolumeSource{
							claimName: "drupal-pv-claim-" + d.name,
						},
					}},
				},
			},
		},
	}
	// Set DrupalSiteRequest instance as the owner and controller
	ctrl.SetControllerReference(d, dep, r.Scheme)
	return dep
}

// persistentVolumeClaimForDrupalSiteRequest returns a drupalSiteRequest DeploymentConfigPHP object
func persistentVolumeClaimForDrupalSiteRequest(d *webservices.cern.chv1alpha1.DrupalSiteRequest) *corev1.PersistentVolumeClaim {
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
			StorageClassName: "cephfs-no-backup",
			AccessModes: []corev1.PersistentVolumeClaimVolumeAccessMode{"ReadWriteOnce"},
			Resources: corev1.ResourceRequirements{
				Requests: corev1.ResourceList{
					corev1.ResourceName(corev1.ResourceStorage): resource.MustParse("5Gi"),
				},
			},
		},
	}
	// Set DrupalSiteRequest instance as the owner and controller
	ctrl.SetControllerReference(d, pvc, r.Scheme)
	return pvc
}

// serviceForDrupalSiteRequestPHP returns a drupalSiteRequest servicePHP object
func serviceForDrupalSiteRequestPHP(d *webservices.cern.chv1alpha1.DrupalSiteRequest) *corev1.Service {
	ls := labelsForDrupalSiterequest(d.Name)

	svc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "php-fpm",
			Namespace: d.Namespace,
		},
		Spec: corev1.ServiceSpec{
			Selector: &metav1.LabelSelector{
				MatchLabels: ls,
			},
			Ports: []corev1.ServicePort{{
				TargetPort: 	9000,
				Name:         	"php-fpm",
				Port:			80,
				Protocol: 	  	"TCP",
			}},
			
		},
	}
	// Set DrupalSiteRequest instance as the owner and controller
	ctrl.SetControllerReference(d, svc, r.Scheme)
	return svc
}

// serviceForDrupalSiteRequestNginx returns a drupalSiteRequest servicePHP object
func serviceForDrupalSiteRequestNginx(d *webservices.cern.chv1alpha1.DrupalSiteRequest) *corev1.Service {
	ls := labelsForDrupalSiterequest(d.Name)

	svc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "drupal-nginx" + d.Name,
			Namespace: d.Namespace,
		},
		Spec: corev1.ServiceSpec{
			Selector: &metav1.LabelSelector{
				MatchLabels: ls,
			},
			Ports: []corev1.ServicePort{{
				TargetPort: 	8080,
				Name:         	"nginx",
				Port:			80,
				Protocol: 	  	"TCP",
			}},
			
		},
	}
	// Set DrupalSiteRequest instance as the owner and controller
	ctrl.SetControllerReference(d, svc, r.Scheme)
	return svc
}

// serviceForDrupalSiteRequestMySQL returns a drupalSiteRequest servicePHP object
func serviceForDrupalSiteRequestMySQL(d *webservices.cern.chv1alpha1.DrupalSiteRequest) *corev1.Service {
	ls := labelsForDrupalSiterequest(d.Name)

	svc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "drupal-mysql" + d.Name,
			Namespace: d.Namespace,
		},
		Spec: corev1.ServiceSpec{
			Selector: &metav1.LabelSelector{
				MatchLabels: ls,
			},
			Ports: []corev1.ServicePort{{
				TargetPort: 	3306,
				Name:         	"mysql",
				Port:			3306,
				Protocol: 	  	"TCP",
			}},
			
		},
	}
	// Set DrupalSiteRequest instance as the owner and controller
	ctrl.SetControllerReference(d, svc, r.Scheme)
	return svc
}

// routeForDrupalSiteRequest returns a drupalSiteRequest route object
func routeForDrupalSiteRequest(d *webservices.cern.chv1alpha1.DrupalSiteRequest) *route.openshift.iov1.Route {
	ls := labelsForDrupalSiterequest(d.Name)

	route := &route.openshift.iov1.Route{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "drupal-mysql" + d.Name,
			Namespace: d.Namespace,
		},
		Spec: route.openshift.iov1.RouteSpec{
			Host: d.name + "-drupaltemplate.clustername.cern.ch",
			To : route.openshift.iov1.RouteTargetReference{
				Kind:	"Service",
				Name:	"drupal-nginx" + d.Name,
				Weight:	100,
			},
			Port: route.openshift.iov1.RoutePort{
				TargetPort: 8080,
			},
			
		},
	}
	// Set DrupalSiteRequest instance as the owner and controller
	ctrl.SetControllerReference(d, svc, r.Scheme)
	return svc
}