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
	"time"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	webservicescernchv1alpha1 "gitlab.cern.ch/drupal/paas/drupalsite-operator/api/v1alpha1"
)

var _ = Describe("DrupalSiteRequest controller", func() {

	// Define utility constants for object names and testing timeouts/durations and intervals.
	const (
		Name      = "test"
		Namespace = "default"

		timeout  = time.Second * 30
		duration = time.Second * 30
		interval = time.Millisecond * 250
	)

	Context("Creating drupalSiteRequest object", func() {
		It("Should be created successfully", func() {
			// By("By creating a new drupalSiteRequest")
			ctx := context.Background()
			key := types.NamespacedName{
				Name:      Name,
				Namespace: Namespace,
			}
			drupalSiteRequestObject := &webservicescernchv1alpha1.DrupalSiteRequest{
				TypeMeta: metav1.TypeMeta{
					APIVersion: "webservices.cern.ch.webservices.cern.ch/v1alpha1",
					Kind:       "DrupalSiteRequest",
				},
				ObjectMeta: metav1.ObjectMeta{
					Name:      key.Name,
					Namespace: Namespace,
				},
				Spec: webservicescernchv1alpha1.DrupalSiteRequestSpec{
					Publish:       false,
					DrupalVersion: "8.9.7",
				},
			}
			fmt.Println(drupalSiteRequestObject)
			Expect(k8sClient.Create(ctx, drupalSiteRequestObject)).Should(Succeed())

			// By("Expecting created")
			// Eventually(func() error {
			// 	d := &webservicescernchv1alpha1.DrupalSiteRequest{}
			// 	return k8sClient.Get(ctx, key, d)
			// }, timeout, interval).Should(Succeed())

			// Delete
			// By("Expecting to delete successfully")
			// Eventually(func() error {
			// 	d := &webservicescernchv1alpha1.DrupalSiteRequest{}
			// 	k8sClient.Get(ctx, key, d)
			// 	return k8sClient.Delete(context.Background(), d)
			// }, timeout, interval).Should(Succeed())

			// By("Expecting to delete finish")
			// Eventually(func() error {
			// 	d := &webservicescernchv1alpha1.DrupalSiteRequest{}
			// 	return k8sClient.Get(ctx, key, d)
			// }, timeout, interval).ShouldNot(Succeed())
		})
	})
})
