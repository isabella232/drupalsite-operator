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
	"time"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	buildv1 "github.com/openshift/api/build/v1"
	imagev1 "github.com/openshift/api/image/v1"
	routev1 "github.com/openshift/api/route/v1"
	"github.com/operator-framework/operator-lib/status"
	velerov1 "github.com/vmware-tanzu/velero/pkg/apis/velero/v1"
	dbodv1a1 "gitlab.cern.ch/drupal/paas/dbod-operator/api/v1alpha1"
	drupalwebservicesv1alpha1 "gitlab.cern.ch/drupal/paas/drupalsite-operator/api/v1alpha1"
	authz "gitlab.cern.ch/paas-tools/operators/authz-operator/api/v1alpha1"
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

const dummySiteUrl = "testsite.webtest.cern.ch"

var _ = Describe("DrupalSite controller", func() {
	const (
		Name      = "test"
		Namespace = "default"

		veleroNamespace = "openshift-cern-drupal"
		timeout         = time.Second * 30
		duration        = time.Second * 30
		interval        = time.Millisecond * 250
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
				Version: drupalwebservicesv1alpha1.Version{
					Name:        "v8.9-1",
					ReleaseSpec: "stable",
				},
				Configuration: drupalwebservicesv1alpha1.Configuration{
					DiskSize:      "10Gi",
					QoSClass:      drupalwebservicesv1alpha1.QoSStandard,
					DatabaseClass: drupalwebservicesv1alpha1.DBODStandard,
				},
				SiteURL: []drupalwebservicesv1alpha1.Url{
					"test-1.webtest.cern.ch",
					"test-2.webtest.cern.ch",
					"test-3.webtest.cern.ch",
				},
			},
		}
	})

	Describe("Creating drupalSite object", func() {
		Context("With basic spec", func() {
			It("All dependent resources should be created", func() {
				By("By creating a namespace for velero backup resources")
				Eventually(func() error {
					return k8sClient.Create(ctx, &corev1.Namespace{
						TypeMeta: metav1.TypeMeta{
							APIVersion: "v1",
							Kind:       "Namespace",
						},
						ObjectMeta: metav1.ObjectMeta{
							Name: veleroNamespace,
						},
					})
				}, timeout, interval).Should(Succeed())

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
				oidcReturnUri := authz.OidcReturnURI{}
				schedule := velerov1.Schedule{}

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

				// Check Site settings configMap creation
				By("Expecting Site settings configmaps created")
				Eventually(func() []metav1.OwnerReference {
					k8sClient.Get(ctx, types.NamespacedName{Name: "site-settings-" + key.Name, Namespace: key.Namespace}, &configmap)
					return configmap.ObjectMeta.OwnerReferences
				}, timeout, interval).Should(ContainElement(expectedOwnerReference))

				// Check PHP Cli configMap creation
				By("Expecting PHP Cli configmaps created")
				Eventually(func() []metav1.OwnerReference {
					k8sClient.Get(ctx, types.NamespacedName{Name: "php-cli-config-" + key.Name, Namespace: key.Namespace}, &configmap)
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
					k8sClient.Get(ctx, types.NamespacedName{Name: "ensure-site-install-" + key.Name, Namespace: key.Namespace}, &job)
					return job.ObjectMeta.OwnerReferences
				}, timeout, interval).Should(ContainElement(expectedOwnerReference))

				// Update drupalSite custom resource status fields to allow route conditions
				By("Updating 'initialized' status field in drupalSite resource")
				Eventually(func() error {
					k8sClient.Get(ctx, types.NamespacedName{Name: key.Name, Namespace: key.Namespace}, &cr)
					cr.Status.Conditions.SetCondition(status.Condition{Type: "Initialized", Status: "True"})
					return k8sClient.Status().Update(ctx, &cr)
				}, timeout, interval).Should(Succeed())

				// Update deployment status fields to allow 'ready' status field to be set on the drupalSite resource
				By("Updating 'ReadyReplicas' and 'AvailableReplicas' status fields in deployment resource")
				Eventually(func() error {
					k8sClient.Get(ctx, key, &deploy)
					deploy.Status.Replicas = 1
					deploy.Status.AvailableReplicas = 1
					deploy.Status.ReadyReplicas = 1
					return k8sClient.Status().Update(ctx, &deploy)
				}, timeout, interval).Should(Succeed())

				// Check if the Schedule resource is created
				By("Expecting Schedule to be created")
				Eventually(func() []metav1.OwnerReference {
					k8sClient.Get(ctx, types.NamespacedName{Name: key.Namespace + "-" + key.Name, Namespace: veleroNamespace}, &schedule)
					return job.ObjectMeta.OwnerReferences
				}, timeout, interval).Should(ContainElement(expectedOwnerReference))

				// Check Routes
				By("Expecting Drupal Route(s) to be created")
				for _, url := range cr.Spec.SiteURL {
					route = routev1.Route{}
					hash := md5.Sum([]byte(url))
					Eventually(func() []metav1.OwnerReference {
						k8sClient.Get(ctx, types.NamespacedName{Name: key.Name + "-" + hex.EncodeToString(hash[0:4]), Namespace: key.Namespace}, &route)
						return route.ObjectMeta.OwnerReferences
					}, timeout, interval).Should(ContainElement(expectedOwnerReference))
				}

				// Check OidcReturnUris
				By("Expecting OidcReturnURIs created")
				for _, url := range cr.Spec.SiteURL {
					oidcReturnUri = authz.OidcReturnURI{}
					hash := md5.Sum([]byte(url))
					Eventually(func() []metav1.OwnerReference {
						k8sClient.Get(ctx, types.NamespacedName{Name: key.Name + "-" + hex.EncodeToString(hash[0:4]), Namespace: key.Namespace}, &oidcReturnUri)
						return oidcReturnUri.ObjectMeta.OwnerReferences
					}, timeout, interval).Should(ContainElement(expectedOwnerReference))
				}
			})
		})
	})

	Describe("Blocking and unblocking the drupalsite object", func() {
		Context("With basic spec", func() {
			It("Should be blocked and unblocked successfully", func() {
				key = types.NamespacedName{
					Name:      Name,
					Namespace: Namespace,
				}

				cr := drupalwebservicesv1alpha1.DrupalSite{}
				namespace := corev1.Namespace{}

				By("Expecting drupalSite object created")
				Eventually(func() error {
					return k8sClient.Get(ctx, key, &cr)
				}, timeout, interval).Should(Succeed())

				By("Adding label to namespace")
				Eventually(func() error {
					k8sClient.Get(ctx, types.NamespacedName{Name: key.Namespace}, &namespace)
					namespace.Labels = map[string]string{"drupal.cern.ch/user-project": "true"}
					return k8sClient.Update(ctx, &namespace)
				}, timeout, interval).Should(Succeed())

				By("Adding annotations to namespace")
				Eventually(func() error {
					k8sClient.Get(ctx, types.NamespacedName{Name: key.Namespace}, &namespace)
					namespace.Annotations = map[string]string{"blocked.webservices.cern.ch/blocked-timestamp": "2021-08-11T10:20:00+00:00", "blocked.webservices.cern.ch/reason": "Blocked due to security reason"}
					return k8sClient.Update(ctx, &namespace)
				}, timeout, interval).Should(Succeed())

				deploy := appsv1.Deployment{}
				By("Expecting to set deployment replicas to 0")
				Eventually(func() bool {
					k8sClient.Get(ctx, key, &deploy)
					return *deploy.Spec.Replicas == 0
				}, timeout, interval).Should(BeTrue())

				By("Removing annotations to namespace")
				Eventually(func() error {
					k8sClient.Get(ctx, types.NamespacedName{Name: key.Namespace}, &namespace)
					delete(namespace.Annotations, "blocked.webservices.cern.ch/blocked-timestamp")
					delete(namespace.Annotations, "blocked.webservices.cern.ch/reason")
					return k8sClient.Update(ctx, &namespace)
				}, timeout, interval).Should(Succeed())

				By("Expecting to set deployment replicas to 1")
				Eventually(func() bool {
					k8sClient.Get(ctx, key, &deploy)
					return *deploy.Spec.Replicas == 1
				}, timeout, interval).Should(BeTrue())
			})
		})
	})

	Describe("Creating a new backup resource", func() {
		Context("for the basic drupalSite", func() {
			It("New velero backups created for the site should reflect in the drupalSite Status", func() {
				By("Expecting drupalSite object created")
				cr := drupalwebservicesv1alpha1.DrupalSite{}
				Eventually(func() error {
					return k8sClient.Get(ctx, key, &cr)
				}, timeout, interval).Should(Succeed())

				// Create a backup resource for the drupalSite
				hash := md5.Sum([]byte(key.Namespace))
				backup := velerov1.Backup{
					TypeMeta: metav1.TypeMeta{
						APIVersion: "velero.io/v1",
						Kind:       "Backup",
					},
					ObjectMeta: metav1.ObjectMeta{
						Name:      key.Name + "backup",
						Namespace: veleroNamespace,
						Labels: map[string]string{
							"drupal.webservices.cern.ch/projectHash": hex.EncodeToString(hash[:]),
							"drupal.webservices.cern.ch/project":     key.Namespace,
							"drupal.webservices.cern.ch/drupalSite":  key.Name,
						},
						Annotations: map[string]string{
							"drupal.webservices.cern.ch/drupalSite": key.Name,
							"drupal.webservices.cern.ch/project":    key.Namespace,
						},
					},
					Status: velerov1.BackupStatus{
						Phase: velerov1.BackupPhaseCompleted,
					},
				}

				By("By creating a backup resource for the drupalSite")
				Eventually(func() error {
					return k8sClient.Create(ctx, &backup)
				}, timeout, interval).Should(Succeed())

				// Check if the backup resource is created
				By("By creating a backup resource for the drupalSite")
				backup1 := velerov1.Backup{}
				Eventually(func() error {
					return k8sClient.Get(ctx, types.NamespacedName{Name: key.Name + "backup", Namespace: veleroNamespace}, &backup1)
				}, timeout, interval).Should(Succeed())

				// Check for the Backup name in the Drupalsite Status
				By("By checking for the Backup in the DrupalSite Status")
				Eventually(func() bool {
					k8sClient.Get(ctx, key, &cr)
					return len(cr.Status.AvailableBackups) > 0 && cr.Status.AvailableBackups[0].BackupName == backup.Name
				}, timeout, interval).Should(BeTrue())
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
				newVersion := "v8.9-1"
				newReleaseSpec := "new"

				// Create drupalSite object
				cr := drupalwebservicesv1alpha1.DrupalSite{}
				By("Expecting drupalSite object created")
				Eventually(func() error {
					return k8sClient.Get(ctx, key, &cr)
				}, timeout, interval).Should(Succeed())

				deploy := appsv1.Deployment{}

				By("Updating the version")
				Eventually(func() error {
					k8sClient.Get(ctx, types.NamespacedName{Name: key.Name, Namespace: key.Namespace}, &cr)
					cr.Spec.Version.Name = newVersion
					cr.Spec.Version.ReleaseSpec = newReleaseSpec
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
					return deploy.Spec.Template.ObjectMeta.Annotations["releaseID"] == newVersion+"-"+newReleaseSpec
				}, timeout, interval).Should(BeTrue())

				pod := corev1.Pod{
					TypeMeta: metav1.TypeMeta{
						APIVersion: "v1",
						Kind:       "Pod",
					},
					ObjectMeta: metav1.ObjectMeta{
						Name:        key.Name,
						Namespace:   key.Namespace,
						Labels:      map[string]string{"drupalSite": cr.Name, "app": "drupal"},
						Annotations: map[string]string{"releaseID": cr.Spec.Version.Name + "-" + cr.Spec.Version.ReleaseSpec},
					},
					Spec: corev1.PodSpec{
						Containers: []corev1.Container{{
							Image: "test-image",
							Name:  "test-image",
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

				// NOTE: Commenting this out temporarily. Refer to https://gitlab.cern.ch/drupal/paas/drupalsite-operator/-/issues/74
				// Check if the drupalSiteObject has 'codeUpdateFailed' status set
				// By("Expecting 'codeUpdateFailed' status set on the drupalSiteObject")
				// Eventually(func() bool {
				// 	k8sClient.Get(ctx, key, &cr)
				// 	return cr.ConditionTrue("CodeUpdateFailed")
				// }, timeout, interval).Should(BeTrue())

				// Update drupalSite Failsafe status to simulate a successful upgrade
				By("Updating 'Failsafe' status field in drupalSite resource")
				Eventually(func() error {
					k8sClient.Get(ctx, types.NamespacedName{Name: key.Name, Namespace: key.Namespace}, &cr)
					cr.Status.Failsafe = newVersion + "-" + newReleaseSpec
					return k8sClient.Status().Update(ctx, &cr)
				}, timeout, interval).Should(Succeed())

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
				oidcReturnUri := authz.OidcReturnURI{}

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

				// Check Site settings configMap creation
				By("Expecting Site settings configmaps recreated")
				Eventually(func() error {
					k8sClient.Get(ctx, types.NamespacedName{Name: "site-settings-" + key.Name, Namespace: key.Namespace}, &configmap)
					return k8sClient.Delete(ctx, &configmap)
				}, timeout, interval).Should(Succeed())
				Eventually(func() []metav1.OwnerReference {
					k8sClient.Get(ctx, types.NamespacedName{Name: "site-settings-" + key.Name, Namespace: key.Namespace}, &configmap)
					return configmap.ObjectMeta.OwnerReferences
				}, timeout, interval).Should(ContainElement(expectedOwnerReference))

				// Check PHP Cli configMap creation
				By("Expecting PHP Cli configmaps recreated")
				Eventually(func() error {
					k8sClient.Get(ctx, types.NamespacedName{Name: "php-cli-config-" + key.Name, Namespace: key.Namespace}, &configmap)
					return k8sClient.Delete(ctx, &configmap)
				}, timeout, interval).Should(Succeed())
				Eventually(func() []metav1.OwnerReference {
					k8sClient.Get(ctx, types.NamespacedName{Name: "php-cli-config-" + key.Name, Namespace: key.Namespace}, &configmap)
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
					k8sClient.Get(ctx, types.NamespacedName{Name: "ensure-site-install-" + key.Name, Namespace: key.Namespace}, &job)
					return k8sClient.Delete(ctx, &job)
				}, timeout, interval).Should(Succeed())
				Eventually(func() []metav1.OwnerReference {
					k8sClient.Get(ctx, types.NamespacedName{Name: "ensure-site-install-" + key.Name, Namespace: key.Namespace}, &job)
					return job.ObjectMeta.OwnerReferences
				}, timeout, interval).Should(ContainElement(expectedOwnerReference))

				// Check Routes
				By("Expecting Drupal Route(s) recreated")
				for _, url := range cr.Spec.SiteURL {
					route = routev1.Route{}
					hash := md5.Sum([]byte(url))
					Eventually(func() error {
						k8sClient.Get(ctx, types.NamespacedName{Name: key.Name + "-" + hex.EncodeToString(hash[0:4]), Namespace: key.Namespace}, &route)
						return k8sClient.Delete(ctx, &route)
					}, timeout, interval).Should(Succeed())
					Eventually(func() []metav1.OwnerReference {
						k8sClient.Get(ctx, types.NamespacedName{Name: key.Name + "-" + hex.EncodeToString(hash[0:4]), Namespace: key.Namespace}, &route)
						return route.ObjectMeta.OwnerReferences
					}, timeout, interval).Should(ContainElement(expectedOwnerReference))
				}

				// Check OidcReturnUris
				By("Expecting OidcReturnUri(s) recreated")
				for _, url := range cr.Spec.SiteURL {
					oidcReturnUri = authz.OidcReturnURI{}
					hash := md5.Sum([]byte(url))
					Eventually(func() error {
						k8sClient.Get(ctx, types.NamespacedName{Name: key.Name + "-" + hex.EncodeToString(hash[0:4]), Namespace: key.Namespace}, &oidcReturnUri)
						return k8sClient.Delete(ctx, &oidcReturnUri)
					}, timeout, interval).Should(Succeed())
					Eventually(func() []metav1.OwnerReference {
						k8sClient.Get(ctx, types.NamespacedName{Name: key.Name + "-" + hex.EncodeToString(hash[0:4]), Namespace: key.Namespace}, &oidcReturnUri)
						return oidcReturnUri.ObjectMeta.OwnerReferences
					}, timeout, interval).Should(ContainElement(expectedOwnerReference))
				}
			})
		})
	})

	Describe("Updating siteUrl Spec", func() {
		Context("Of the basic drupalSite", func() {
			It("Extra routes, oidcReturnUris should be removed and siteUrl list should be ensured", func() {
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
				route := routev1.Route{}
				oidcReturnUri := authz.OidcReturnURI{}

				By("Updating the siteUrl spec")
				Eventually(func() error {
					k8sClient.Get(ctx, types.NamespacedName{Name: key.Name, Namespace: key.Namespace}, &cr)
					cr.Spec.SiteURL = cr.Spec.SiteURL[:len(cr.Spec.SiteURL)-1]
					cr.Spec.SiteURL = append(cr.Spec.SiteURL, "test-4.webtest.cern.ch")
					return k8sClient.Update(ctx, &cr)
				}, timeout, interval).Should(Succeed())

				// Check Routes
				By("Expecting Drupal Route(s) created")
				for _, url := range cr.Spec.SiteURL {
					route = routev1.Route{}
					hash := md5.Sum([]byte(url))
					Eventually(func() []metav1.OwnerReference {
						k8sClient.Get(ctx, types.NamespacedName{Name: key.Name + "-" + hex.EncodeToString(hash[0:4]), Namespace: key.Namespace}, &route)
						return route.ObjectMeta.OwnerReferences
					}, timeout, interval).Should(ContainElement(expectedOwnerReference))
				}

				// Check OidcReturnUris
				By("Expecting OidcReturnURIs created")
				for _, url := range cr.Spec.SiteURL {
					oidcReturnUri = authz.OidcReturnURI{}
					hash := md5.Sum([]byte(url))
					Eventually(func() []metav1.OwnerReference {
						k8sClient.Get(ctx, types.NamespacedName{Name: key.Name + "-" + hex.EncodeToString(hash[0:4]), Namespace: key.Namespace}, &oidcReturnUri)
						return oidcReturnUri.ObjectMeta.OwnerReferences
					}, timeout, interval).Should(ContainElement(expectedOwnerReference))
				}

				// Check deleted entries
				By("Expecting Drupal Route deleted")
				hash := md5.Sum([]byte("test-3.webtest.cern.ch"))
				Eventually(func() error {
					return k8sClient.Get(ctx, types.NamespacedName{Name: key.Name + "-" + hex.EncodeToString(hash[0:4]), Namespace: key.Namespace}, &route)
				}, timeout, interval).ShouldNot(Succeed())

				By("Expecting oidcReturnURI deleted")
				hash = md5.Sum([]byte("test-3.webtest.cern.ch"))
				Eventually(func() error {
					return k8sClient.Get(ctx, types.NamespacedName{Name: key.Name + "-" + hex.EncodeToString(hash[0:4]), Namespace: key.Namespace}, &oidcReturnUri)
				}, timeout, interval).ShouldNot(Succeed())
			})
		})
	})

	Describe("Updating deployment object", func() {
		Context("With debug annotations", func() {
			It("Should not be updated successfully", func() {
				By("Expecting drupalSite object created")
				cr := drupalwebservicesv1alpha1.DrupalSite{}
				Eventually(func() error {
					return k8sClient.Get(ctx, key, &cr)
				}, timeout, interval).Should(Succeed())

				By("Updating the drupalSite with debug annotation")
				Eventually(func() error {
					k8sClient.Get(ctx, types.NamespacedName{Name: key.Name, Namespace: key.Namespace}, &cr)
					if cr.Annotations == nil {
						cr.Annotations = map[string]string{}
					}
					cr.Annotations[debugAnnotation] = "true"
					return k8sClient.Update(ctx, &cr)
				}, timeout, interval).Should(Succeed())

				By("By updating deployment object")
				deploy := appsv1.Deployment{}
				k8sClient.Get(ctx, types.NamespacedName{Name: key.Name, Namespace: key.Namespace}, &deploy)
				deploy.Spec.Template.ObjectMeta.Annotations["pre.hook.backup.velero.io/container"] = "test-debug"
				Eventually(func() error {
					return k8sClient.Update(ctx, &deploy)
				}, timeout, interval).Should(Succeed())

				Eventually(func() bool {
					k8sClient.Get(ctx, types.NamespacedName{Name: key.Name, Namespace: key.Namespace}, &deploy)
					return deploy.Spec.Template.GetAnnotations()["pre.hook.backup.velero.io/container"] == "test-debug"
				}, timeout, interval).ShouldNot(BeTrue())
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
						Version: drupalwebservicesv1alpha1.Version{
							Name:        "v8.9-1",
							ReleaseSpec: "stable",
						},
						Configuration: drupalwebservicesv1alpha1.Configuration{
							DiskSize:               "10Gi",
							QoSClass:               drupalwebservicesv1alpha1.QoSStandard,
							DatabaseClass:          drupalwebservicesv1alpha1.DBODStandard,
							ExtraConfigurationRepo: "https://gitlab.cern.ch/rvineetr/test-ravineet-d8-containers-buildconfig.git",
						},
						SiteURL: []drupalwebservicesv1alpha1.Url{dummySiteUrl},
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
				oidcReturnUri := authz.OidcReturnURI{}
				schedule := velerov1.Schedule{}
				secret := corev1.Secret{}

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

				// Check Site settings configMap creation
				By("Expecting Site settings configmaps created")
				Eventually(func() []metav1.OwnerReference {
					k8sClient.Get(ctx, types.NamespacedName{Name: "site-settings-" + key.Name, Namespace: key.Namespace}, &configmap)
					return configmap.ObjectMeta.OwnerReferences
				}, timeout, interval).Should(ContainElement(expectedOwnerReference))

				// Check PHP Cli configMap creation
				By("Expecting PHP Cli configmaps created")
				Eventually(func() []metav1.OwnerReference {
					k8sClient.Get(ctx, types.NamespacedName{Name: "php-cli-config-" + key.Name, Namespace: key.Namespace}, &configmap)
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
					k8sClient.Get(ctx, types.NamespacedName{Name: "sitebuilder-s2i-" + key.Name, Namespace: key.Namespace}, &is)
					return is.ObjectMeta.OwnerReferences
				}, timeout, interval).Should(ContainElement(expectedOwnerReference))

				// Check Drush job
				By("Expecting Drush job created")
				Eventually(func() []metav1.OwnerReference {
					k8sClient.Get(ctx, types.NamespacedName{Name: "ensure-site-install-" + key.Name, Namespace: key.Namespace}, &job)
					return job.ObjectMeta.OwnerReferences
				}, timeout, interval).Should(ContainElement(expectedOwnerReference))

				// Check S2I buildConfig
				By("Expecting S2I buildConfig created")
				Eventually(func() []metav1.OwnerReference {
					k8sClient.Get(ctx, types.NamespacedName{Name: "sitebuilder-s2i-" + nameVersionHash(drupalSiteObject), Namespace: key.Namespace}, &bc)
					return bc.ObjectMeta.OwnerReferences
				}, timeout, interval).Should(ContainElement(expectedOwnerReference))

				// Update drupalSite custom resource status fields to allow route conditions
				By("Updating 'initialized' status field in drupalSite resource")
				Eventually(func() error {
					k8sClient.Get(ctx, types.NamespacedName{Name: key.Name, Namespace: key.Namespace}, &cr)
					cr.Status.Conditions.SetCondition(status.Condition{Type: "Initialized", Status: "True"})
					return k8sClient.Status().Update(ctx, &cr)
				}, timeout, interval).Should(Succeed())

				// Update deployment status fields to allow 'ready' status field to be set on the drupalSite resource
				By("Updating 'ReadyReplicas' and 'AvailableReplicas' status fields in deployment resource")
				Eventually(func() error {
					k8sClient.Get(ctx, key, &deploy)
					deploy.Status.Replicas = 1
					deploy.Status.AvailableReplicas = 1
					deploy.Status.ReadyReplicas = 1
					return k8sClient.Status().Update(ctx, &deploy)
				}, timeout, interval).Should(Succeed())

				// Check if the Schedule resource is created
				By("Expecting Schedule to be created")
				Eventually(func() []metav1.OwnerReference {
					k8sClient.Get(ctx, types.NamespacedName{Name: key.Namespace + "-" + key.Name, Namespace: veleroNamespace}, &schedule)
					return job.ObjectMeta.OwnerReferences
				}, timeout, interval).Should(ContainElement(expectedOwnerReference))

				// Check Routes
				By("Expecting Drupal Route(s) to be created")
				for _, url := range cr.Spec.SiteURL {
					route = routev1.Route{}
					hash := md5.Sum([]byte(url))
					Eventually(func() []metav1.OwnerReference {
						k8sClient.Get(ctx, types.NamespacedName{Name: key.Name + "-" + hex.EncodeToString(hash[0:4]), Namespace: key.Namespace}, &route)
						return route.ObjectMeta.OwnerReferences
					}, timeout, interval).Should(ContainElement(expectedOwnerReference))
				}

				// Check OidcReturnUris
				By("Expecting OidcReturnURIs created")
				for _, url := range cr.Spec.SiteURL {
					oidcReturnUri = authz.OidcReturnURI{}
					hash := md5.Sum([]byte(url))
					Eventually(func() []metav1.OwnerReference {
						k8sClient.Get(ctx, types.NamespacedName{Name: key.Name + "-" + hex.EncodeToString(hash[0:4]), Namespace: key.Namespace}, &oidcReturnUri)
						return oidcReturnUri.ObjectMeta.OwnerReferences
					}, timeout, interval).Should(ContainElement(expectedOwnerReference))
				}

				// Check gitlab webhook secret resource creation
				By("Expecting Gitlab webhook secret created")
				Eventually(func() []metav1.OwnerReference {
					k8sClient.Get(ctx, types.NamespacedName{Name: "gitlab-trigger-secret-" + key.Name, Namespace: key.Namespace}, &secret)
					return secret.ObjectMeta.OwnerReferences
				}, timeout, interval).Should(ContainElement(expectedOwnerReference))

				// Check gitlab webhook URL updated on the drupalSite status
				By("Expecting Gitlab webhook secret listed in the DrupalSite status")
				Eventually(func() bool {
					return cr.Status.GitlabWebhookURL == "https://api."+ClusterName+".okd.cern.ch:443/apis/build.openshift.io/v1/namespaces/"+drupalSiteObject.Namespace+"/buildconfigs/"+"sitebuilder-s2i-"+nameVersionHash(drupalSiteObject)+"/webhooks/"+string(secret.Name)+"/gitlab"
				}, timeout, interval).Should(BeTrue())
			})
		})
	})

	Describe("Creating a new backup resource", func() {
		Context("for the drupalSite with s2i source", func() {
			It("New velero backups created for the site should reflect in the drupalSite Status", func() {
				By("Expecting drupalSite object created")
				cr := drupalwebservicesv1alpha1.DrupalSite{}
				Eventually(func() error {
					return k8sClient.Get(ctx, key, &cr)
				}, timeout, interval).Should(Succeed())

				// Create a backup resource for the drupalSite
				hash := md5.Sum([]byte(key.Namespace))
				backup := velerov1.Backup{
					TypeMeta: metav1.TypeMeta{
						APIVersion: "velero.io/v1",
						Kind:       "Backup",
					},
					ObjectMeta: metav1.ObjectMeta{
						Name:      key.Name + "backup",
						Namespace: veleroNamespace,

						Labels: map[string]string{
							"drupal.webservices.cern.ch/projectHash": hex.EncodeToString(hash[:]),
							"drupal.webservices.cern.ch/project":     key.Namespace,
							"drupal.webservices.cern.ch/drupalSite":  key.Name,
						},
						Annotations: map[string]string{
							"drupal.webservices.cern.ch/drupalSite": key.Name,
							"drupal.webservices.cern.ch/project":    key.Namespace,
						},
					},
					Status: velerov1.BackupStatus{
						Phase: velerov1.BackupPhaseCompleted,
					},
				}

				By("By creating a backup resource for the drupalSite")
				Eventually(func() error {
					return k8sClient.Create(ctx, &backup)
				}, timeout, interval).Should(Succeed())

				// Check if the backup resource is created
				By("By creating a backup resource for the drupalSite")
				backup1 := velerov1.Backup{}
				Eventually(func() error {
					return k8sClient.Get(ctx, types.NamespacedName{Name: key.Name + "backup", Namespace: veleroNamespace}, &backup1)
				}, timeout, interval).Should(Succeed())

				// Check for the Backup name in the Drupalsite Status
				By("By checking for the Backup in the DrupalSite Status")
				Eventually(func() bool {
					k8sClient.Get(ctx, key, &cr)
					return len(cr.Status.AvailableBackups) > 0 && cr.Status.AvailableBackups[0].BackupName == backup.Name
				}, timeout, interval).Should(BeTrue())
			})
		})
	})

	Describe("Update the drupalsite object with s2i source", func() {
		Context("With a different drupal Version", func() {
			It("Should be updated successfully", func() {
				key = types.NamespacedName{
					Name:      Name + "-advanced",
					Namespace: "advanced",
				}
				newVersion := "v8.9-1"
				newReleaseSpec := "new"

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

				By("Updating the version")
				Eventually(func() error {
					k8sClient.Get(ctx, types.NamespacedName{Name: key.Name, Namespace: key.Namespace}, &cr)
					cr.Spec.Version.Name = newVersion
					cr.Spec.Version.ReleaseSpec = newReleaseSpec
					return k8sClient.Update(ctx, &cr)
				}, timeout, interval).Should(Succeed())

				By("Expecting new S2I buildConfig to be updated")
				Eventually(func() []metav1.OwnerReference {
					k8sClient.Get(ctx, types.NamespacedName{Name: "sitebuilder-s2i-" + nameVersionHash(&cr), Namespace: key.Namespace}, &bc)
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
					return deploy.Spec.Template.ObjectMeta.Annotations["releaseID"] == newVersion+"-"+newReleaseSpec
				}, timeout, interval).Should(BeTrue())

				pod := corev1.Pod{
					TypeMeta: metav1.TypeMeta{
						APIVersion: "v1",
						Kind:       "Pod",
					},
					ObjectMeta: metav1.ObjectMeta{
						Name:        key.Name,
						Namespace:   key.Namespace,
						Labels:      map[string]string{"drupalSite": cr.Name, "app": "drupal"},
						Annotations: map[string]string{"releaseID": cr.Spec.Version.Name + "-" + cr.Spec.Version.ReleaseSpec},
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

				// NOTE: Commenting this out temporarily. Refer to https://gitlab.cern.ch/drupal/paas/drupalsite-operator/-/issues/74
				// Check if the drupalSiteObject has 'codeUpdateFailed' status set
				// By("Expecting 'codeUpdateFailed' status set on the drupalSiteObject")
				// Eventually(func() bool {
				// 	k8sClient.Get(ctx, key, &cr)
				// 	return cr.ConditionTrue("CodeUpdateFailed")
				// }, timeout, interval).Should(BeTrue())

				// NOTE: Commenting this out temporarily. Refer to https://gitlab.cern.ch/drupal/paas/drupalsite-operator/-/issues/74
				// Check if the drupalSiteObject has 'updateInProgress' annotation unset
				// By("Expecting 'updateInProgress' annotation unset on the drupalSiteObject")
				// Eventually(func() bool {
				// 	k8sClient.Get(ctx, key, &cr)
				// 	_, set := cr.Annotations["updateInProgress"]
				// 	return set
				// }, timeout, interval).Should(BeFalse())

				// Update drupalSite Failsafe status to simulate a successful upgrade
				By("Updating 'Failsafe' status field in drupalSite resource")
				Eventually(func() error {
					k8sClient.Get(ctx, types.NamespacedName{Name: key.Name, Namespace: key.Namespace}, &cr)
					cr.Status.Failsafe = newVersion + "-" + newReleaseSpec
					return k8sClient.Status().Update(ctx, &cr)
				}, timeout, interval).Should(Succeed())

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
						Version: drupalwebservicesv1alpha1.Version{
							Name:        "v8.9-1",
							ReleaseSpec: "stable",
						},
						Configuration: drupalwebservicesv1alpha1.Configuration{
							DiskSize:               "10Gi",
							QoSClass:               drupalwebservicesv1alpha1.QoSStandard,
							DatabaseClass:          drupalwebservicesv1alpha1.DBODStandard,
							ExtraConfigurationRepo: "https://gitlab.cern.ch/rvineetr/test-ravineet-d8-containers-buildconfig.git",
						},
						SiteURL: []drupalwebservicesv1alpha1.Url{dummySiteUrl},
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

	Describe("Creating a drupalSite object", func() {
		Context("Without 'configuration' & 'releaseSpec' field", func() {
			It("Default values should be used and the site should be created", func() {
				key = types.NamespacedName{
					Name:      Name + "-defaults",
					Namespace: "defaults",
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
						Version: drupalwebservicesv1alpha1.Version{
							Name: "v8.9-1",
						},
						SiteURL: []drupalwebservicesv1alpha1.Url{dummySiteUrl},
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

				By("Expecting the default configuration values to be set")
				Eventually(func() bool {
					return string(cr.Spec.Configuration.QoSClass) == string(drupalwebservicesv1alpha1.QoSStandard)
				}, timeout, interval).Should(BeTrue())
				Eventually(func() bool {
					return string(cr.Spec.Configuration.DatabaseClass) == string(drupalwebservicesv1alpha1.DBODStandard)
				}, timeout, interval).Should(BeTrue())
				Eventually(func() bool {
					k8sClient.Get(ctx, key, &cr)
					return string(cr.Spec.Configuration.DiskSize) == "2000Mi"
				}, timeout, interval).Should(BeTrue())

				By("Expecting the default configuration values to be set")
				Eventually(func() bool {
					k8sClient.Get(ctx, key, &cr)
					return cr.Spec.Version.ReleaseSpec == DefaultD8ReleaseSpec
				}, timeout, interval).Should(BeTrue())

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
				dbod := dbodv1a1.Database{}
				route := routev1.Route{}
				oidcReturnUri := authz.OidcReturnURI{}

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

				// Check Site settings configMap creation
				By("Expecting Site settings configmaps created")
				Eventually(func() []metav1.OwnerReference {
					k8sClient.Get(ctx, types.NamespacedName{Name: "site-settings-" + key.Name, Namespace: key.Namespace}, &configmap)
					return configmap.ObjectMeta.OwnerReferences
				}, timeout, interval).Should(ContainElement(expectedOwnerReference))

				// Check PHP Cli configMap creation
				By("Expecting PHP Cli configmaps created")
				Eventually(func() []metav1.OwnerReference {
					k8sClient.Get(ctx, types.NamespacedName{Name: "php-cli-config-" + key.Name, Namespace: key.Namespace}, &configmap)
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
					k8sClient.Get(ctx, types.NamespacedName{Name: "ensure-site-install-" + key.Name, Namespace: key.Namespace}, &job)
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

				// Check Routes
				By("Expecting Drupal Route(s) to be created")
				for _, url := range cr.Spec.SiteURL {
					route = routev1.Route{}
					hash := md5.Sum([]byte(url))
					Eventually(func() []metav1.OwnerReference {
						k8sClient.Get(ctx, types.NamespacedName{Name: key.Name + "-" + hex.EncodeToString(hash[0:4]), Namespace: key.Namespace}, &route)
						return route.ObjectMeta.OwnerReferences
					}, timeout, interval).Should(ContainElement(expectedOwnerReference))
				}

				// Check OidcReturnUris
				By("Expecting OidcReturnURIs created")
				for _, url := range cr.Spec.SiteURL {
					oidcReturnUri = authz.OidcReturnURI{}
					hash := md5.Sum([]byte(url))
					Eventually(func() []metav1.OwnerReference {
						k8sClient.Get(ctx, types.NamespacedName{Name: key.Name + "-" + hex.EncodeToString(hash[0:4]), Namespace: key.Namespace}, &oidcReturnUri)
						return oidcReturnUri.ObjectMeta.OwnerReferences
					}, timeout, interval).Should(ContainElement(expectedOwnerReference))
				}

				By("Expecting to delete successfully")
				Eventually(func() error {
					return k8sClient.Delete(ctx, drupalSiteObject)
				}, timeout, interval).Should(Succeed())

				By("Expecting to delete finish")
				Eventually(func() error {
					return k8sClient.Get(ctx, key, drupalSiteObject)
				}, timeout, interval).ShouldNot(Succeed())

				// Passing only one configuration value
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
						Version: drupalwebservicesv1alpha1.Version{
							Name: "v8.9-1",
						},
						Configuration: drupalwebservicesv1alpha1.Configuration{
							DatabaseClass: drupalwebservicesv1alpha1.DBODStandard,
						},
						SiteURL: []drupalwebservicesv1alpha1.Url{dummySiteUrl},
					},
				}

				By("By creating a new drupalSite")
				Eventually(func() error {
					return k8sClient.Create(ctx, drupalSiteObject)
				}, timeout, interval).Should(Succeed())

				// Create drupalSite object
				By("Expecting drupalSite object created")
				cr = drupalwebservicesv1alpha1.DrupalSite{}
				Eventually(func() error {
					return k8sClient.Get(ctx, key, &cr)
				}, timeout, interval).Should(Succeed())

				By("Expecting the default configuration values to be set")
				Eventually(func() bool {
					return string(cr.Spec.Configuration.QoSClass) == string(drupalwebservicesv1alpha1.QoSStandard)
				}, timeout, interval).Should(BeTrue())
				Eventually(func() bool {
					return string(cr.Spec.Configuration.DatabaseClass) == string(drupalwebservicesv1alpha1.DBODStandard)
				}, timeout, interval).Should(BeTrue())
				Eventually(func() bool {
					k8sClient.Get(ctx, key, &cr)
					return string(cr.Spec.Configuration.DiskSize) == "2000Mi"
				}, timeout, interval).Should(BeTrue())

				By("Expecting to delete successfully")
				Eventually(func() error {
					return k8sClient.Delete(ctx, drupalSiteObject)
				}, timeout, interval).Should(Succeed())

				By("Expecting to delete finish")
				Eventually(func() error {
					return k8sClient.Get(ctx, key, drupalSiteObject)
				}, timeout, interval).ShouldNot(Succeed())

				// Passing invalid configuration value
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
						Version: drupalwebservicesv1alpha1.Version{
							Name: "v8.9-1",
						},
						Configuration: drupalwebservicesv1alpha1.Configuration{
							QoSClass: "randomval",
						},
						SiteURL: []drupalwebservicesv1alpha1.Url{dummySiteUrl},
					},
				}

				By("By creating a new drupalSite")
				Eventually(func() error {
					return k8sClient.Create(ctx, drupalSiteObject)
				}, timeout, interval).ShouldNot(Succeed())

				// Create drupalSite object
				By("Expecting drupalSite object to not be created")
				cr = drupalwebservicesv1alpha1.DrupalSite{}
				Eventually(func() error {
					return k8sClient.Get(ctx, key, &cr)
				}, timeout, interval).ShouldNot(Succeed())

			})
		})
	})

	Describe("Creating a drupalSite object", func() {
		Context("With critical QoS", func() {
			It("All dependent resources should be created", func() {
				key = types.NamespacedName{
					Name:      Name + "-critical",
					Namespace: "critical",
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
						Version: drupalwebservicesv1alpha1.Version{
							Name:        "v8.9-1",
							ReleaseSpec: "stable",
						},
						Configuration: drupalwebservicesv1alpha1.Configuration{
							DiskSize:      "10Gi",
							QoSClass:      drupalwebservicesv1alpha1.QoSCritical,
							DatabaseClass: drupalwebservicesv1alpha1.DBODStandard,
						},
						SiteURL: []drupalwebservicesv1alpha1.Url{dummySiteUrl},
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
				dbod := dbodv1a1.Database{}
				route := routev1.Route{}
				oidcReturnUri := authz.OidcReturnURI{}
				schedule := velerov1.Schedule{}

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

				By("Expecting the drupal deployment to have 3 replicas")
				Eventually(func() bool {
					k8sClient.Get(ctx, types.NamespacedName{Name: key.Name, Namespace: key.Namespace}, &deploy)
					return *deploy.Spec.Replicas == 3
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
					k8sClient.Get(ctx, types.NamespacedName{Name: "ensure-site-install-" + key.Name, Namespace: key.Namespace}, &job)
					return job.ObjectMeta.OwnerReferences
				}, timeout, interval).Should(ContainElement(expectedOwnerReference))

				// Update drupalSite custom resource status fields to allow route conditions
				By("Updating 'initialized' status field in drupalSite resource")
				Eventually(func() error {
					k8sClient.Get(ctx, types.NamespacedName{Name: key.Name, Namespace: key.Namespace}, &cr)
					cr.Status.Conditions.SetCondition(status.Condition{Type: "Initialized", Status: "True"})
					return k8sClient.Status().Update(ctx, &cr)
				}, timeout, interval).Should(Succeed())

				// Update deployment status fields to allow 'ready' status field to be set on the drupalSite resource
				By("Updating 'ReadyReplicas' and 'AvailableReplicas' status fields in deployment resource")
				Eventually(func() error {
					k8sClient.Get(ctx, key, &deploy)
					deploy.Status.Replicas = 1
					deploy.Status.AvailableReplicas = 1
					deploy.Status.ReadyReplicas = 1
					return k8sClient.Status().Update(ctx, &deploy)
				}, timeout, interval).Should(Succeed())

				// Check if the Schedule resource is created
				By("Expecting Schedule to be created")
				Eventually(func() []metav1.OwnerReference {
					k8sClient.Get(ctx, types.NamespacedName{Name: key.Namespace + "-" + key.Name, Namespace: veleroNamespace}, &schedule)
					return job.ObjectMeta.OwnerReferences
				}, timeout, interval).Should(ContainElement(expectedOwnerReference))

				// Check Routes
				By("Expecting Drupal Route(s) to be created")
				for _, url := range cr.Spec.SiteURL {
					route = routev1.Route{}
					hash := md5.Sum([]byte(url))
					Eventually(func() []metav1.OwnerReference {
						k8sClient.Get(ctx, types.NamespacedName{Name: key.Name + "-" + hex.EncodeToString(hash[0:4]), Namespace: key.Namespace}, &route)
						return route.ObjectMeta.OwnerReferences
					}, timeout, interval).Should(ContainElement(expectedOwnerReference))
				}

				// Check OidcReturnUris
				By("Expecting OidcReturnURIs created")
				for _, url := range cr.Spec.SiteURL {
					oidcReturnUri = authz.OidcReturnURI{}
					hash := md5.Sum([]byte(url))
					Eventually(func() []metav1.OwnerReference {
						k8sClient.Get(ctx, types.NamespacedName{Name: key.Name + "-" + hex.EncodeToString(hash[0:4]), Namespace: key.Namespace}, &oidcReturnUri)
						return oidcReturnUri.ObjectMeta.OwnerReferences
					}, timeout, interval).Should(ContainElement(expectedOwnerReference))
				}

			})
		})
	})

	Describe("Updating the critical QoS drupalSite object", func() {
		Context("With standard QoS", func() {
			It("All dependent resources should be created", func() {
				key = types.NamespacedName{
					Name:      Name + "-critical",
					Namespace: "critical",
				}

				// Create drupalSite object
				By("Expecting drupalSite object created")
				cr := drupalwebservicesv1alpha1.DrupalSite{}
				Eventually(func() error {
					return k8sClient.Get(ctx, key, &cr)
				}, timeout, interval).Should(Succeed())

				// Update drupalSite QoSClass fields to standard QoS
				By("Updating 'qosClass' spec field in drupalSite resource")
				Eventually(func() error {
					k8sClient.Get(ctx, types.NamespacedName{Name: key.Name, Namespace: key.Namespace}, &cr)
					cr.Spec.QoSClass = drupalwebservicesv1alpha1.QoSStandard
					return k8sClient.Update(ctx, &cr)
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
				dbod := dbodv1a1.Database{}
				route := routev1.Route{}
				oidcReturnUri := authz.OidcReturnURI{}
				schedule := velerov1.Schedule{}

				// Check DBOD resource creation
				By("Expecting Database resource created")
				Eventually(func() []metav1.OwnerReference {
					k8sClient.Get(ctx, types.NamespacedName{Name: key.Name, Namespace: key.Namespace}, &dbod)
					return dbod.ObjectMeta.OwnerReferences
				}, timeout, interval).Should(ContainElement(expectedOwnerReference))

				By("Expecting the drupal deployment to have at least 2 containers")
				Eventually(func() bool {
					k8sClient.Get(ctx, types.NamespacedName{Name: key.Name, Namespace: key.Namespace}, &deploy)
					return len(deploy.Spec.Template.Spec.Containers) >= 2
				}, timeout, interval).Should(BeTrue())

				By("Expecting the drupal deployment to have 1 replicas")
				Eventually(func() bool {
					k8sClient.Get(ctx, types.NamespacedName{Name: key.Name, Namespace: key.Namespace}, &deploy)
					return *deploy.Spec.Replicas == 1
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
					k8sClient.Get(ctx, types.NamespacedName{Name: "ensure-site-install-" + key.Name, Namespace: key.Namespace}, &job)
					return job.ObjectMeta.OwnerReferences
				}, timeout, interval).Should(ContainElement(expectedOwnerReference))

				// Update drupalSite custom resource status fields to allow route conditions
				By("Updating 'initialized' status field in drupalSite resource")
				Eventually(func() error {
					k8sClient.Get(ctx, types.NamespacedName{Name: key.Name, Namespace: key.Namespace}, &cr)
					cr.Status.Conditions.SetCondition(status.Condition{Type: "Initialized", Status: "True"})
					return k8sClient.Status().Update(ctx, &cr)
				}, timeout, interval).Should(Succeed())

				// Update deployment status fields to allow 'ready' status field to be set on the drupalSite resource
				By("Updating 'ReadyReplicas' and 'AvailableReplicas' status fields in deployment resource")
				Eventually(func() error {
					k8sClient.Get(ctx, key, &deploy)
					deploy.Status.Replicas = 1
					deploy.Status.AvailableReplicas = 1
					deploy.Status.ReadyReplicas = 1
					return k8sClient.Status().Update(ctx, &deploy)
				}, timeout, interval).Should(Succeed())

				// Check if the Schedule resource is created
				By("Expecting Schedule to be created")
				Eventually(func() []metav1.OwnerReference {
					k8sClient.Get(ctx, types.NamespacedName{Name: key.Namespace + "-" + key.Name, Namespace: veleroNamespace}, &schedule)
					return job.ObjectMeta.OwnerReferences
				}, timeout, interval).Should(ContainElement(expectedOwnerReference))

				// Check Routes
				By("Expecting Drupal Route(s) to be created")
				for _, url := range cr.Spec.SiteURL {
					route = routev1.Route{}
					hash := md5.Sum([]byte(url))
					Eventually(func() []metav1.OwnerReference {
						k8sClient.Get(ctx, types.NamespacedName{Name: key.Name + "-" + hex.EncodeToString(hash[0:4]), Namespace: key.Namespace}, &route)
						return route.ObjectMeta.OwnerReferences
					}, timeout, interval).Should(ContainElement(expectedOwnerReference))
				}

				// Check OidcReturnUris
				By("Expecting OidcReturnURIs created")
				for _, url := range cr.Spec.SiteURL {
					oidcReturnUri = authz.OidcReturnURI{}
					hash := md5.Sum([]byte(url))
					Eventually(func() []metav1.OwnerReference {
						k8sClient.Get(ctx, types.NamespacedName{Name: key.Name + "-" + hex.EncodeToString(hash[0:4]), Namespace: key.Namespace}, &oidcReturnUri)
						return oidcReturnUri.ObjectMeta.OwnerReferences
					}, timeout, interval).Should(ContainElement(expectedOwnerReference))
				}

			})
		})
	})

	Describe("Creating a new backup resource", func() {
		Context("for the drupalSite with critical QoS", func() {
			It("New velero backups created for the site should reflect in the drupalSite Status", func() {
				By("Expecting drupalSite object created")
				cr := drupalwebservicesv1alpha1.DrupalSite{}
				Eventually(func() error {
					return k8sClient.Get(ctx, key, &cr)
				}, timeout, interval).Should(Succeed())

				// Create a backup resource for the drupalSite
				hash := md5.Sum([]byte(key.Namespace))
				backup := velerov1.Backup{
					TypeMeta: metav1.TypeMeta{
						APIVersion: "velero.io/v1",
						Kind:       "Backup",
					},
					ObjectMeta: metav1.ObjectMeta{
						Name:      key.Name + "backup",
						Namespace: veleroNamespace,

						Labels: map[string]string{
							"drupal.webservices.cern.ch/projectHash": hex.EncodeToString(hash[:]),
							"drupal.webservices.cern.ch/project":     key.Namespace,
							"drupal.webservices.cern.ch/drupalSite":  key.Name,
						},
						Annotations: map[string]string{
							"drupal.webservices.cern.ch/drupalSite": key.Name,
							"drupal.webservices.cern.ch/project":    key.Namespace,
						},
					},
					Status: velerov1.BackupStatus{
						Phase: velerov1.BackupPhaseCompleted,
					},
				}

				By("By creating a backup resource for the drupalSite")
				Eventually(func() error {
					return k8sClient.Create(ctx, &backup)
				}, timeout, interval).Should(Succeed())

				// Check if the backup resource is created
				By("By creating a backup resource for the drupalSite")
				backup1 := velerov1.Backup{}
				Eventually(func() error {
					return k8sClient.Get(ctx, types.NamespacedName{Name: key.Name + "backup", Namespace: veleroNamespace}, &backup1)
				}, timeout, interval).Should(Succeed())

				// Check for the Backup name in the Drupalsite Status
				By("By checking for the Backup in the DrupalSite Status")
				Eventually(func() bool {
					k8sClient.Get(ctx, key, &cr)
					return len(cr.Status.AvailableBackups) > 0 && cr.Status.AvailableBackups[0].BackupName == backup.Name
				}, timeout, interval).Should(BeTrue())
			})
		})
	})

	Describe("Update the drupalsite object with critical QoS", func() {
		Context("With a different drupal Version", func() {
			It("Should be updated successfully", func() {
				key = types.NamespacedName{
					Name:      Name + "-critical",
					Namespace: "critical",
				}
				newVersion := "v8.9-1"
				newReleaseSpec := "new"

				// Create drupalSite object
				cr := drupalwebservicesv1alpha1.DrupalSite{}
				By("Expecting drupalSite object created")
				Eventually(func() error {
					return k8sClient.Get(ctx, key, &cr)
				}, timeout, interval).Should(Succeed())

				deploy := appsv1.Deployment{}

				By("Updating the version")
				Eventually(func() error {
					k8sClient.Get(ctx, types.NamespacedName{Name: key.Name, Namespace: key.Namespace}, &cr)
					cr.Spec.Version.Name = newVersion
					cr.Spec.Version.ReleaseSpec = newReleaseSpec
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
					return deploy.Spec.Template.ObjectMeta.Annotations["releaseID"] == newVersion+"-"+newReleaseSpec
				}, timeout, interval).Should(BeTrue())

				pod := corev1.Pod{
					TypeMeta: metav1.TypeMeta{
						APIVersion: "v1",
						Kind:       "Pod",
					},
					ObjectMeta: metav1.ObjectMeta{
						Name:        key.Name,
						Namespace:   key.Namespace,
						Labels:      map[string]string{"drupalSite": cr.Name, "app": "drupal"},
						Annotations: map[string]string{"releaseID": cr.Spec.Version.Name + "-" + cr.Spec.Version.ReleaseSpec},
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

				// NOTE: Commenting this out temporarily. Refer to https://gitlab.cern.ch/drupal/paas/drupalsite-operator/-/issues/74
				// Check if the drupalSiteObject has 'codeUpdateFailed' status set
				// By("Expecting 'codeUpdateFailed' status set on the drupalSiteObject")
				// Eventually(func() bool {
				// 	k8sClient.Get(ctx, key, &cr)
				// 	return cr.ConditionTrue("CodeUpdateFailed")
				// }, timeout, interval).Should(BeTrue())

				// NOTE: Commenting this out temporarily. Refer to https://gitlab.cern.ch/drupal/paas/drupalsite-operator/-/issues/74
				// Check if the drupalSiteObject has 'updateInProgress' annotation unset
				// By("Expecting 'updateInProgress' annotation unset on the drupalSiteObject")
				// Eventually(func() bool {
				// 	k8sClient.Get(ctx, key, &cr)
				// 	_, set := cr.Annotations["updateInProgress"]
				// 	return set
				// }, timeout, interval).Should(BeFalse())

				// Update drupalSite Failsafe status to simulate a successful upgrade
				By("Updating 'Failsafe' status field in drupalSite resource")
				Eventually(func() error {
					k8sClient.Get(ctx, types.NamespacedName{Name: key.Name, Namespace: key.Namespace}, &cr)
					cr.Status.Failsafe = newVersion + "-" + newReleaseSpec
					return k8sClient.Status().Update(ctx, &cr)
				}, timeout, interval).Should(Succeed())

				// Further tests need to be implemented, if we can bypass ExecErr with envtest
			})
		})
	})
	Describe("TODO Using DrupalProjectConfig", func() {
		Context("", func() {
			It("", func() {
				// Tests that should be done
				// Create a new project without any DrupalProjectConfig, operator logs Warning: Project $PROJECT does not contain any DrupalProjectConfig!
				// Createda DrupalProjectConfig with one website and saw spec getting automatically filled, $PROJECT only 1 drupalsite\"$DRUPALSITE\", which is considered the primary production site
				// Creation of a second website does not affect DrupalProjectConfig
				// Creation of DrupalProjectConfig with 2 DrupalSites in place will not automatically fill spec.primarySiteName
				// Deletion of 2 instances to 1, and spec.primarySiteName=="", will auto-fill
				// If spec.primarySiteName!="", it is never auto-filled, not even with just one instance being created
				// By default DrupalSite.status.isPrimary is false
				// Editing DrupalProjectConfig will reflect changes on DrupalSite.status.isPrimary
			})
		})
	})
	Describe("Deleting the drupalsite object", func() {
		Context("With critical QoS", func() {
			It("Should be deleted successfully", func() {
				key = types.NamespacedName{
					Name:      Name + "-critical",
					Namespace: "critical",
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
						Version: drupalwebservicesv1alpha1.Version{
							Name:        "v8.9-1",
							ReleaseSpec: "stable",
						},
						Configuration: drupalwebservicesv1alpha1.Configuration{
							DiskSize:      "10Gi",
							QoSClass:      drupalwebservicesv1alpha1.QoSStandard,
							DatabaseClass: drupalwebservicesv1alpha1.DBODStandard,
						},
						SiteURL: []drupalwebservicesv1alpha1.Url{dummySiteUrl},
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
