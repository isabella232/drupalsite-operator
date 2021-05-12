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
	"time"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	buildv1 "github.com/openshift/api/build/v1"
	imagev1 "github.com/openshift/api/image/v1"
	routev1 "github.com/openshift/api/route/v1"
	"github.com/operator-framework/operator-lib/status"
	dbodv1a1 "gitlab.cern.ch/drupal/paas/dbod-operator/api/v1alpha1"
	drupalwebservicesv1alpha1 "gitlab.cern.ch/drupal/paas/drupalsite-operator/api/v1alpha1"
	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
)

// Ginkgo makes it easy to write expressive specs that describe the behavior of your code in an organized manner.
// Describe and Context containers are used organize the tests and It is used to define the test result
// Describe is a top-level container that defines the test
// Context defines the circumstance i.e parameters or specifications
// It defines what the test result should be
// By defines smaller tests under a given it and in case of failure, the 'By' text is printed out
// Ref: http://onsi.github.io/ginkgo/

var _ = Describe("DrupalSite controller", func() {
	const (
		Name      = "test"
		Namespace = "default"

		timeout  = time.Second * 30
		duration = time.Second * 30
		interval = time.Millisecond * 250
	)
	var (
		drupalSiteObject = &drupalwebservicesv1alpha1.DrupalSite{}
	)

	ctx := context.Background()
	key := types.NamespacedName{
		Name:      Name,
		Namespace: Namespace,
	}

	BeforeEach(func() {
		drupalSiteObject = &drupalwebservicesv1alpha1.DrupalSite{
			TypeMeta: metav1.TypeMeta{
				APIVersion: "drupal.webservices.cern.ch/v1alpha1",
				Kind:       "DrupalSite",
			},
			ObjectMeta: metav1.ObjectMeta{
				Name:      key.Name,
				Namespace: key.Namespace,
			},
			Spec: drupalwebservicesv1alpha1.DrupalSiteSpec{
				Publish:       true,
				DrupalVersion: "8.9.13",
				DiskSize:      "10Gi",
				Environment: drupalwebservicesv1alpha1.Environment{
					Name:          "dev",
					QoSClass:      "standard",
					DatabaseClass: "test",
				},
			},
		}
	})

	Describe("Creating drupalSite object", func() {
		Context("With basic spec", func() {
			It("All dependent resources should be created", func() {
				By("By creating a new drupalSite")
				Eventually(func() error {
					return k8sClient.Create(ctx, drupalSiteObject)
				}, timeout, interval).Should(Succeed())

				By("Expecting drupalSite object created")
				cr := drupalwebservicesv1alpha1.DrupalSite{}
				Eventually(func() error {
					return k8sClient.Get(ctx, key, &cr)
				}, timeout, interval).Should(Succeed())
				trueVar := true
				expectedOwnerReference := metav1.OwnerReference{
					APIVersion: "drupal.webservices.cern.ch/v1alpha1",
					Kind:       "DrupalSite",
					Name:       Name,
					UID:        cr.UID,
					Controller: &trueVar,
				}
				configmap := corev1.ConfigMap{}
				svc := corev1.Service{}
				pvc := corev1.PersistentVolumeClaim{}
				job := batchv1.Job{}
				deploy := appsv1.Deployment{}
				dbod := dbodv1a1.Database{}
				route := routev1.Route{}

				// Check DBOD resource creation
				By("Expecting Database resource created")
				Eventually(func() []metav1.OwnerReference {
					k8sClient.Get(ctx, types.NamespacedName{Name: key.Name, Namespace: key.Namespace}, &dbod)
					return dbod.ObjectMeta.OwnerReferences
				}, timeout, interval).Should(ContainElement(expectedOwnerReference))

				// Update DBOD resource status field
				By("Updating DBOD instance in Database resource status")
				Eventually(func() error {
					k8sClient.Get(ctx, types.NamespacedName{Name: key.Name, Namespace: key.Namespace}, &dbod)
					dbod.Status.DbodInstance = "test"
					return k8sClient.Status().Update(ctx, &dbod)
				}, timeout, interval).Should(Succeed())

				//By("Expecting the drupal deployment to have the EnvFrom secret field set correctly")
				By("Expecting the drupal deployment to have at least 2 containers")
				Eventually(func() bool {
					k8sClient.Get(ctx, types.NamespacedName{Name: key.Name, Namespace: key.Namespace}, &deploy)
					return len(deploy.Spec.Template.Spec.Containers) >= 2
				}, timeout, interval).Should(BeTrue())

				// Check PHP-FPM configMap creation
				By("Expecting PHP_FPM configmaps created")
				Eventually(func() []metav1.OwnerReference {
					k8sClient.Get(ctx, types.NamespacedName{Name: "php-fpm-" + key.Name, Namespace: key.Namespace}, &configmap)
					return configmap.ObjectMeta.OwnerReferences
				}, timeout, interval).Should(ContainElement(expectedOwnerReference))

				// Check Nginx configMap creation
				By("Expecting Nginx configmaps created")
				Eventually(func() []metav1.OwnerReference {
					k8sClient.Get(ctx, types.NamespacedName{Name: "nginx-" + key.Name, Namespace: key.Namespace}, &configmap)
					return configmap.ObjectMeta.OwnerReferences
				}, timeout, interval).Should(ContainElement(expectedOwnerReference))

				// Check Drupal service
				By("Expecting Drupal service created")
				Eventually(func() []metav1.OwnerReference {
					k8sClient.Get(ctx, types.NamespacedName{Name: key.Name, Namespace: key.Namespace}, &svc)
					return svc.ObjectMeta.OwnerReferences
				}, timeout, interval).Should(ContainElement(expectedOwnerReference))

				// Check drupal persistentVolumeClaim
				By("Expecting drupal persistentVolumeClaim created")
				Eventually(func() []metav1.OwnerReference {
					k8sClient.Get(ctx, types.NamespacedName{Name: "pv-claim-" + key.Name, Namespace: key.Namespace}, &pvc)
					return pvc.ObjectMeta.OwnerReferences
				}, timeout, interval).Should(ContainElement(expectedOwnerReference))

				// Check Drupal deployments
				By("Expecting Drupal deployments created")
				Eventually(func() []metav1.OwnerReference {
					k8sClient.Get(ctx, types.NamespacedName{Name: key.Name, Namespace: key.Namespace}, &deploy)
					return deploy.ObjectMeta.OwnerReferences
				}, timeout, interval).Should(ContainElement(expectedOwnerReference))

				// Check Drush job
				By("Expecting Drush job created")
				Eventually(func() []metav1.OwnerReference {
					k8sClient.Get(ctx, types.NamespacedName{Name: "site-install-" + key.Name, Namespace: key.Namespace}, &job)
					return job.ObjectMeta.OwnerReferences
				}, timeout, interval).Should(ContainElement(expectedOwnerReference))

				// Update drupalSite custom resource status fields to allow route conditions
				By("Updating 'initialized' and 'ready' status fields in drupalSite resource")
				Eventually(func() error {
					k8sClient.Get(ctx, types.NamespacedName{Name: key.Name, Namespace: key.Namespace}, &cr)
					cr.Status.Conditions.SetCondition(status.Condition{Type: "Ready", Status: "True"})
					cr.Status.Conditions.SetCondition(status.Condition{Type: "Initialized", Status: "True"})
					return k8sClient.Status().Update(ctx, &cr)
				}, timeout, interval).Should(Succeed())

				// Check Route
				By("Expecting Route to be created since publish is true")
				Eventually(func() []metav1.OwnerReference {
					k8sClient.Get(ctx, types.NamespacedName{Name: key.Name, Namespace: key.Namespace}, &route)
					return route.ObjectMeta.OwnerReferences
				}, timeout, interval).Should(ContainElement(expectedOwnerReference))

				// Switch "publish: false"
				Eventually(func() error {
					k8sClient.Get(ctx, types.NamespacedName{Name: key.Name, Namespace: key.Namespace}, &cr)
					cr.Spec.Publish = false
					return k8sClient.Update(ctx, &cr)
				}, timeout, interval).Should(Succeed())

				By("Expecting Route to be removed after switching publish to false.")
				Eventually(func() error {
					return k8sClient.Get(ctx, types.NamespacedName{Name: key.Name, Namespace: key.Namespace}, &route)
				}, timeout, interval).Should(Not(Succeed()))
			})
		})
	})

	Describe("Update the basic drupalsite object", func() {
		Context("With a different drupal Version", func() {
			It("Should be updated successfully", func() {
				key = types.NamespacedName{
					Name:      Name,
					Namespace: Namespace,
				}
				newVersion := "8.9.13-new"

				// Create drupalSite object
				cr := drupalwebservicesv1alpha1.DrupalSite{}
				By("Expecting drupalSite object created")
				Eventually(func() error {
					return k8sClient.Get(ctx, key, &cr)
				}, timeout, interval).Should(Succeed())

				deploy := appsv1.Deployment{}

				// Update deployment status fields to allow update to proceed
				By("Updating 'ReadyReplicas' and 'AvailableReplicas' status fields in deployment resource")
				Eventually(func() error {
					k8sClient.Get(ctx, key, &deploy)
					deploy.Status.Replicas = 1
					deploy.Status.AvailableReplicas = 1
					deploy.Status.ReadyReplicas = 1
					return k8sClient.Status().Update(ctx, &deploy)
				}, timeout, interval).Should(Succeed())

				// Update drupalSite custom resource status fields to allow update to proceed
				By("Updating 'initialized' status fields in drupalSite resource")
				Eventually(func() error {
					k8sClient.Get(ctx, key, &cr)
					cr.Status.Conditions.SetCondition(status.Condition{Type: "Initialized", Status: "True"})
					return k8sClient.Status().Update(ctx, &cr)
				}, timeout, interval).Should(Succeed())

				By("Updating the version")
				Eventually(func() error {
					k8sClient.Get(ctx, types.NamespacedName{Name: key.Name, Namespace: key.Namespace}, &cr)
					cr.Spec.DrupalVersion = newVersion
					return k8sClient.Update(ctx, &cr)
				}, timeout, interval).Should(Succeed())

				// Check if the drupalSiteObject has 'updateInProgress' annotation set
				By("Expecting 'updateInProgress' annotation set on the drupalSiteObject")
				Eventually(func() bool {
					k8sClient.Get(ctx, key, &cr)
					return cr.Annotations["updateInProgress"] == "true"
				}, timeout, interval).Should(BeTrue())

				// Check the annotation on the deployment
				By("Expecting the new drupal Version on the pod annotation")
				Eventually(func() bool {
					k8sClient.Get(ctx, key, &deploy)
					return deploy.Spec.Template.ObjectMeta.Annotations["drupalVersion"] == newVersion
				}, timeout, interval).Should(BeTrue())

				pod := corev1.Pod{
					TypeMeta: metav1.TypeMeta{
						APIVersion: "drupal.webservices.cern.ch/v1alpha1",
						Kind:       "DrupalSite",
					},
					ObjectMeta: metav1.ObjectMeta{
						Name:        key.Name,
						Namespace:   key.Namespace,
						Labels:      map[string]string{"drupalSite": cr.Name, "app": "drupal"},
						Annotations: map[string]string{"drupalVersion": cr.Spec.DrupalVersion},
					},
					Spec: corev1.PodSpec{
						Containers: []corev1.Container{{
							Image: "busybox",
							Name:  "busybox",
						}},
					},
				}

				// Create a pod to simulate a error in the update process
				By("Expecting a new pod with required labels to be created")
				Eventually(func() error {
					return k8sClient.Create(ctx, &pod)
				}, timeout, interval).Should(Succeed())

				// Set the pod status phase to 'Failed' to simulate a error in the update process
				By("Expecting a new pod status to be updated")
				Eventually(func() error {
					k8sClient.Get(ctx, key, &pod)
					pod.Status.Phase = corev1.PodFailed
					return k8sClient.Status().Update(ctx, &pod)
				}, timeout, interval).Should(Succeed())

				// Check if the drupalSiteObject has 'codeUpdateFailed' status set
				By("Expecting 'codeUpdateFailed' status set on the drupalSiteObject")
				Eventually(func() bool {
					k8sClient.Get(ctx, key, &cr)
					return cr.ConditionTrue("CodeUpdateFailed")
				}, timeout, interval).Should(BeTrue())

				// Further tests need to be implemented, if we can bypass ExecErr with envtest
			})
		})
	})

	Describe("Deleting dependent objects", func() {
		Context("Of the basic drupalSite", func() {
			It("All dependent resources should be recreated successfully", func() {
				By("Expecting drupalSite object created")
				cr := drupalwebservicesv1alpha1.DrupalSite{}
				Eventually(func() error {
					return k8sClient.Get(ctx, key, &cr)
				}, timeout, interval).Should(Succeed())
				trueVar := true
				expectedOwnerReference := metav1.OwnerReference{
					APIVersion: "drupal.webservices.cern.ch/v1alpha1",
					Kind:       "DrupalSite",
					Name:       key.Name,
					UID:        cr.UID,
					Controller: &trueVar,
				}
				configmap := corev1.ConfigMap{}
				svc := corev1.Service{}
				pvc := corev1.PersistentVolumeClaim{}
				job := batchv1.Job{}
				deploy := appsv1.Deployment{}
				route := routev1.Route{}

				// Check PHP-FPM configMap recreation
				By("Expecting PHP_FPM configmaps recreated")
				Eventually(func() error {
					k8sClient.Get(ctx, types.NamespacedName{Name: "php-fpm-" + key.Name, Namespace: key.Namespace}, &configmap)
					return k8sClient.Delete(ctx, &configmap)
				}, timeout, interval).Should(Succeed())
				Eventually(func() []metav1.OwnerReference {
					k8sClient.Get(ctx, types.NamespacedName{Name: "php-fpm-" + key.Name, Namespace: key.Namespace}, &configmap)
					return configmap.ObjectMeta.OwnerReferences
				}, timeout, interval).Should(ContainElement(expectedOwnerReference))

				// Check Nginx configMap creation
				By("Expecting Nginx configmaps recreated")
				Eventually(func() error {
					k8sClient.Get(ctx, types.NamespacedName{Name: "nginx-" + key.Name, Namespace: key.Namespace}, &configmap)
					return k8sClient.Delete(ctx, &configmap)
				}, timeout, interval).Should(Succeed())
				Eventually(func() []metav1.OwnerReference {
					k8sClient.Get(ctx, types.NamespacedName{Name: "nginx-" + key.Name, Namespace: key.Namespace}, &configmap)
					return configmap.ObjectMeta.OwnerReferences
				}, timeout, interval).Should(ContainElement(expectedOwnerReference))

				// Check Drupal service
				By("Expecting Drupal service recreated")
				Eventually(func() error {
					k8sClient.Get(ctx, types.NamespacedName{Name: key.Name, Namespace: key.Namespace}, &svc)
					return k8sClient.Delete(ctx, &svc)
				}, timeout, interval).Should(Succeed())
				Eventually(func() []metav1.OwnerReference {
					k8sClient.Get(ctx, types.NamespacedName{Name: key.Name, Namespace: key.Namespace}, &svc)
					return svc.ObjectMeta.OwnerReferences
				}, timeout, interval).Should(ContainElement(expectedOwnerReference))

				// Check drupal persistentVolumeClaim
				By("Expecting drupal persistentVolumeClaim recreated")
				Eventually(func() error {
					k8sClient.Get(ctx, types.NamespacedName{Name: "pv-claim-" + key.Name, Namespace: key.Namespace}, &pvc)
					return k8sClient.Delete(ctx, &pvc)
				}, timeout, interval).Should(Succeed())
				Eventually(func() []metav1.OwnerReference {
					k8sClient.Get(ctx, types.NamespacedName{Name: "pv-claim-" + key.Name, Namespace: key.Namespace}, &pvc)
					return pvc.ObjectMeta.OwnerReferences
				}, timeout, interval).Should(ContainElement(expectedOwnerReference))

				// Check Drupal deployments
				By("Expecting Drupal deployments recreated")
				Eventually(func() error {
					k8sClient.Get(ctx, types.NamespacedName{Name: key.Name, Namespace: key.Namespace}, &deploy)
					return k8sClient.Delete(ctx, &deploy)
				}, timeout, interval).Should(Succeed())
				Eventually(func() []metav1.OwnerReference {
					k8sClient.Get(ctx, types.NamespacedName{Name: key.Name, Namespace: key.Namespace}, &deploy)
					return deploy.ObjectMeta.OwnerReferences
				}, timeout, interval).Should(ContainElement(expectedOwnerReference))

				// Check Drush job
				By("Expecting Drush job recreated")
				Eventually(func() error {
					k8sClient.Get(ctx, types.NamespacedName{Name: "site-install-" + key.Name, Namespace: key.Namespace}, &job)
					return k8sClient.Delete(ctx, &job)
				}, timeout, interval).Should(Succeed())
				Eventually(func() []metav1.OwnerReference {
					k8sClient.Get(ctx, types.NamespacedName{Name: "site-install-" + key.Name, Namespace: key.Namespace}, &job)
					return job.ObjectMeta.OwnerReferences
				}, timeout, interval).Should(ContainElement(expectedOwnerReference))

				// Check Route
				// Since we switch the publish field to 'false' in the last test case, there shouldn't be a route that exists
				By("Expecting Route recreated")
				Eventually(func() error {
					k8sClient.Get(ctx, types.NamespacedName{Name: key.Name, Namespace: key.Namespace}, &route)
					return k8sClient.Delete(ctx, &route)
				}, timeout, interval).Should(Not(Succeed()))
				Eventually(func() []metav1.OwnerReference {
					k8sClient.Get(ctx, types.NamespacedName{Name: key.Name, Namespace: key.Namespace}, &route)
					return route.ObjectMeta.OwnerReferences
				}, timeout, interval).Should(Not(ContainElement(expectedOwnerReference)))
			})
		})
	})

	Describe("Deleting the drupalsite object", func() {
		Context("With basic spec", func() {
			It("Should be deleted successfully", func() {
				By("Expecting to delete successfully")
				Eventually(func() error {
					return k8sClient.Delete(context.Background(), drupalSiteObject)
				}, timeout, interval).Should(Succeed())

				By("Expecting to delete finish")
				Eventually(func() error {
					return k8sClient.Get(ctx, key, drupalSiteObject)
				}, timeout, interval).ShouldNot(Succeed())
			})
		})
	})

	Describe("Creating a drupalSite object", func() {
		Context("With s2i source", func() {
			It("All dependent resources should be created", func() {
				key = types.NamespacedName{
					Name:      Name + "-advanced",
					Namespace: "advanced",
				}
				drupalSiteObject = &drupalwebservicesv1alpha1.DrupalSite{
					TypeMeta: metav1.TypeMeta{
						APIVersion: "drupal.webservices.cern.ch/v1alpha1",
						Kind:       "DrupalSite",
					},
					ObjectMeta: metav1.ObjectMeta{
						Name:      key.Name,
						Namespace: key.Namespace,
					},
					Spec: drupalwebservicesv1alpha1.DrupalSiteSpec{
						Publish:       false,
						DrupalVersion: "8.9.13",
						DiskSize:      "10Gi",
						Environment: drupalwebservicesv1alpha1.Environment{
							Name:            "dev",
							QoSClass:        "standard",
							DatabaseClass:   "test",
							ExtraConfigRepo: "https://gitlab.cern.ch/rvineetr/test-ravineet-d8-containers-buildconfig.git",
						},
					},
				}

				By("By creating the testing namespace")
				Eventually(func() error {
					return k8sClient.Create(ctx, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{
						Name: key.Namespace},
					})
				}, timeout, interval).Should(Succeed())

				By("By creating a new drupalSite")
				Eventually(func() error {
					return k8sClient.Create(ctx, drupalSiteObject)
				}, timeout, interval).Should(Succeed())

				// Create drupalSite object
				By("Expecting drupalSite object created")
				cr := drupalwebservicesv1alpha1.DrupalSite{}
				Eventually(func() error {
					return k8sClient.Get(ctx, key, &cr)
				}, timeout, interval).Should(Succeed())
				trueVar := true
				expectedOwnerReference := metav1.OwnerReference{
					APIVersion: "drupal.webservices.cern.ch/v1alpha1",
					Kind:       "DrupalSite",
					Name:       key.Name,
					UID:        cr.UID,
					Controller: &trueVar,
				}
				configmap := corev1.ConfigMap{}
				svc := corev1.Service{}
				pvc := corev1.PersistentVolumeClaim{}
				job := batchv1.Job{}
				deploy := appsv1.Deployment{}
				is := imagev1.ImageStream{}
				bc := buildv1.BuildConfig{}
				dbod := dbodv1a1.Database{}
				route := routev1.Route{}

				// Check DBOD resource creation
				By("Expecting Database resource created")
				Eventually(func() []metav1.OwnerReference {
					k8sClient.Get(ctx, types.NamespacedName{Name: key.Name, Namespace: key.Namespace}, &dbod)
					return dbod.ObjectMeta.OwnerReferences
				}, timeout, interval).Should(ContainElement(expectedOwnerReference))

				// Update DBOD resource status field
				By("Updating the DBOD instance in Database resource status")
				Eventually(func() error {
					k8sClient.Get(ctx, types.NamespacedName{Name: key.Name, Namespace: key.Namespace}, &dbod)
					dbod.Status.DbodInstance = "test"
					return k8sClient.Status().Update(ctx, &dbod)
				}, timeout, interval).Should(Succeed())

				By("Expecting the drupal deployment to have at least 2 containers")
				Eventually(func() bool {
					k8sClient.Get(ctx, types.NamespacedName{Name: key.Name, Namespace: key.Namespace}, &deploy)
					return len(deploy.Spec.Template.Spec.Containers) >= 2
				}, timeout, interval).Should(BeTrue())

				// Check PHP-FPM configMap creation
				By("Expecting PHP_FPM configmaps created")
				Eventually(func() []metav1.OwnerReference {
					k8sClient.Get(ctx, types.NamespacedName{Name: "php-fpm-" + key.Name, Namespace: key.Namespace}, &configmap)
					return configmap.ObjectMeta.OwnerReferences
				}, timeout, interval).Should(ContainElement(expectedOwnerReference))

				// Check Nginx configMap creation
				By("Expecting Nginx configmaps created")
				Eventually(func() []metav1.OwnerReference {
					k8sClient.Get(ctx, types.NamespacedName{Name: "nginx-" + key.Name, Namespace: key.Namespace}, &configmap)
					return configmap.ObjectMeta.OwnerReferences
				}, timeout, interval).Should(ContainElement(expectedOwnerReference))

				// Check Drupal service
				By("Expecting Drupal service created")
				Eventually(func() []metav1.OwnerReference {
					k8sClient.Get(ctx, types.NamespacedName{Name: key.Name, Namespace: key.Namespace}, &svc)
					return svc.ObjectMeta.OwnerReferences
				}, timeout, interval).Should(ContainElement(expectedOwnerReference))

				// Check drupal persistentVolumeClaim
				By("Expecting drupal persistentVolumeClaim created")
				Eventually(func() []metav1.OwnerReference {
					k8sClient.Get(ctx, types.NamespacedName{Name: "pv-claim-" + key.Name, Namespace: key.Namespace}, &pvc)
					return pvc.ObjectMeta.OwnerReferences
				}, timeout, interval).Should(ContainElement(expectedOwnerReference))

				// Check Drupal deployments
				By("Expecting Drupal deployments created")
				Eventually(func() []metav1.OwnerReference {
					k8sClient.Get(ctx, types.NamespacedName{Name: key.Name, Namespace: key.Namespace}, &deploy)
					return deploy.ObjectMeta.OwnerReferences
				}, timeout, interval).Should(ContainElement(expectedOwnerReference))

				// Check sitebuilder-s2i imageStream
				By("Expecting sitebuilder-s2i imageStream created")
				Eventually(func() []metav1.OwnerReference {
					k8sClient.Get(ctx, types.NamespacedName{Name: "site-builder-s2i-" + key.Name, Namespace: key.Namespace}, &is)
					return is.ObjectMeta.OwnerReferences
				}, timeout, interval).Should(ContainElement(expectedOwnerReference))

				// Check Drush job
				By("Expecting Drush job created")
				Eventually(func() []metav1.OwnerReference {
					k8sClient.Get(ctx, types.NamespacedName{Name: "site-install-" + key.Name, Namespace: key.Namespace}, &job)
					return job.ObjectMeta.OwnerReferences
				}, timeout, interval).Should(ContainElement(expectedOwnerReference))

				// Check S2I buildConfig
				By("Expecting S2I buildConfig created")
				Eventually(func() []metav1.OwnerReference {
					k8sClient.Get(ctx, types.NamespacedName{Name: "site-builder-s2i-" + nameVersionHash(drupalSiteObject), Namespace: key.Namespace}, &bc)
					return bc.ObjectMeta.OwnerReferences
				}, timeout, interval).Should(ContainElement(expectedOwnerReference))

				// Update drupalSite custom resource status fields to allow route conditions
				By("Updating 'initialized' and 'ready' status fields in drupalSite resource")
				Eventually(func() error {
					k8sClient.Get(ctx, types.NamespacedName{Name: key.Name, Namespace: key.Namespace}, &cr)
					cr.Status.Conditions.SetCondition(status.Condition{Type: "Ready", Status: "True"})
					cr.Status.Conditions.SetCondition(status.Condition{Type: "Initialized", Status: "True"})
					return k8sClient.Status().Update(ctx, &cr)
				}, timeout, interval).Should(Succeed())

				// Check Route
				By("Expecting Route to not be created since publish is false")
				Eventually(func() error {
					return k8sClient.Get(ctx, types.NamespacedName{Name: key.Name, Namespace: key.Namespace}, &route)
				}, timeout, interval).Should(Not(Succeed()))
			})
		})
	})

	Describe("Update the drupalsite object", func() {
		Context("With a different drupal Version", func() {
			It("Should be updated successfully", func() {
				key = types.NamespacedName{
					Name:      Name + "-advanced",
					Namespace: "advanced",
				}
				newVersion := "8.9.13-new"

				// Create drupalSite object
				cr := drupalwebservicesv1alpha1.DrupalSite{}
				By("Expecting drupalSite object created")
				Eventually(func() error {
					return k8sClient.Get(ctx, key, &cr)
				}, timeout, interval).Should(Succeed())

				trueVar := true
				expectedOwnerReference := metav1.OwnerReference{
					APIVersion: "drupal.webservices.cern.ch/v1alpha1",
					Kind:       "DrupalSite",
					Name:       key.Name,
					UID:        cr.UID,
					Controller: &trueVar,
				}
				bc := buildv1.BuildConfig{}
				deploy := appsv1.Deployment{}

				// Update deployment status fields to allow update to proceed
				By("Updating 'ReadyReplicas' and 'AvailableReplicas' status fields in deployment resource")
				Eventually(func() error {
					k8sClient.Get(ctx, key, &deploy)
					deploy.Status.Replicas = 1
					deploy.Status.AvailableReplicas = 1
					deploy.Status.ReadyReplicas = 1
					return k8sClient.Status().Update(ctx, &deploy)
				}, timeout, interval).Should(Succeed())

				// Update drupalSite custom resource status fields to allow update to proceed
				By("Updating 'initialized' status fields in drupalSite resource")
				Eventually(func() error {
					k8sClient.Get(ctx, key, &cr)
					cr.Status.Conditions.SetCondition(status.Condition{Type: "Initialized", Status: "True"})
					return k8sClient.Status().Update(ctx, &cr)
				}, timeout, interval).Should(Succeed())

				By("Updating the version")
				Eventually(func() error {
					k8sClient.Get(ctx, types.NamespacedName{Name: key.Name, Namespace: key.Namespace}, &cr)
					cr.Spec.DrupalVersion = newVersion
					return k8sClient.Update(ctx, &cr)
				}, timeout, interval).Should(Succeed())

				By("Expecting new S2I buildConfig to be updated")
				Eventually(func() []metav1.OwnerReference {
					k8sClient.Get(ctx, types.NamespacedName{Name: "site-builder-s2i-" + nameVersionHash(&cr), Namespace: key.Namespace}, &bc)
					return bc.ObjectMeta.OwnerReferences
				}, timeout, interval).Should(ContainElement(expectedOwnerReference))

				// Check if the drupalSiteObject has 'updateInProgress' annotation set
				By("Expecting 'updateInProgress' annotation set on the drupalSiteObject")
				Eventually(func() bool {
					k8sClient.Get(ctx, key, &cr)
					return cr.Annotations["updateInProgress"] == "true"
				}, timeout, interval).Should(BeTrue())

				// Check the annotation on the deployment
				By("Expecting the new drupal Version on the pod annotation")
				Eventually(func() bool {
					k8sClient.Get(ctx, key, &deploy)
					return deploy.Spec.Template.ObjectMeta.Annotations["drupalVersion"] == newVersion
				}, timeout, interval).Should(BeTrue())

				pod := corev1.Pod{
					TypeMeta: metav1.TypeMeta{
						APIVersion: "drupal.webservices.cern.ch/v1alpha1",
						Kind:       "DrupalSite",
					},
					ObjectMeta: metav1.ObjectMeta{
						Name:        key.Name,
						Namespace:   key.Namespace,
						Labels:      map[string]string{"drupalSite": cr.Name, "app": "drupal"},
						Annotations: map[string]string{"drupalVersion": cr.Spec.DrupalVersion},
					},
					Spec: corev1.PodSpec{
						Containers: []corev1.Container{{
							Image: "test-image",
							Name:  "test-image",
						}},
					},
				}

				// Since there is no deployment controller for creating pods. Create a pod manually to simulate a error in the update process
				By("Expecting a new pod with required labels to be created")
				Eventually(func() error {
					return k8sClient.Create(ctx, &pod)
				}, timeout, interval).Should(Succeed())

				// Set the pod status phase to 'Failed' to simulate a error in the update process
				By("Expecting a new pod status to be updated")
				Eventually(func() error {
					k8sClient.Get(ctx, key, &pod)
					pod.Status.Phase = corev1.PodFailed
					return k8sClient.Status().Update(ctx, &pod)
				}, timeout, interval).Should(Succeed())

				// Check if the drupalSiteObject has 'codeUpdateFailed' status set
				By("Expecting 'codeUpdateFailed' status set on the drupalSiteObject")
				Eventually(func() bool {
					k8sClient.Get(ctx, key, &cr)
					return cr.ConditionTrue("CodeUpdateFailed")
				}, timeout, interval).Should(BeTrue())

				// Further tests need to be implemented, if we can bypass ExecErr with envtest
			})
		})
	})

	Describe("Deleting the drupalsite object", func() {
		Context("With s2i spec", func() {
			It("Should be deleted successfully", func() {
				key = types.NamespacedName{
					Name:      Name + "-advanced",
					Namespace: "advanced",
				}
				drupalSiteObject = &drupalwebservicesv1alpha1.DrupalSite{
					TypeMeta: metav1.TypeMeta{
						APIVersion: "drupal.webservices.cern.ch/v1alpha1",
						Kind:       "DrupalSite",
					},
					ObjectMeta: metav1.ObjectMeta{
						Name:      key.Name,
						Namespace: key.Namespace,
					},
					Spec: drupalwebservicesv1alpha1.DrupalSiteSpec{
						Publish:       false,
						DrupalVersion: "8.9.13",
						DiskSize:      "10Gi",
						Environment: drupalwebservicesv1alpha1.Environment{
							Name:            "dev",
							QoSClass:        "standard",
							DatabaseClass:   "test",
							ExtraConfigRepo: "https://gitlab.cern.ch/rvineetr/test-ravineet-d8-containers-buildconfig.git",
						},
					},
				}
				By("Expecting to delete successfully")
				Eventually(func() error {
					return k8sClient.Delete(ctx, drupalSiteObject)
				}, timeout, interval).Should(Succeed())

				By("Expecting to delete finish")
				Eventually(func() error {
					return k8sClient.Get(ctx, key, drupalSiteObject)
				}, timeout, interval).ShouldNot(Succeed())
			})
		})
	})
})
