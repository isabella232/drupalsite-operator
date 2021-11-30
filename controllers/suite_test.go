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
	"flag"
	"path/filepath"
	"testing"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	dbodv1a1 "gitlab.cern.ch/drupal/paas/dbod-operator/api/v1alpha1"
	drupalwebservicesv1alpha1 "gitlab.cern.ch/drupal/paas/drupalsite-operator/api/v1alpha1"
	authz "gitlab.cern.ch/paas-tools/operators/authz-operator/api/v1alpha1"
	"k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
	"sigs.k8s.io/controller-runtime/pkg/envtest/printer"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"

	// +kubebuilder:scaffold:imports
	buildv1 "github.com/openshift/api/build/v1"
	imagev1 "github.com/openshift/api/image/v1"
	routev1 "github.com/openshift/api/route/v1"
	velerov1 "github.com/vmware-tanzu/velero/pkg/apis/velero/v1"
	appsv1 "k8s.io/api/apps/v1"
)

// These tests use Ginkgo (BDD-style Go testing framework). Refer to
// http://onsi.github.io/ginkgo/ to learn more about Ginkgo.
var k8sClient client.Client
var testEnv *envtest.Environment

func init() {
	flag.StringVar(&SiteBuilderImage, "sitebuilder-image", "", "The sitebuilder source image name.")
}

func TestAPIs(t *testing.T) {
	RegisterFailHandler(Fail)

	RunSpecsWithDefaultAndCustomReporters(t,
		"Controller Suite",
		[]Reporter{printer.NewlineReporter{}})
}

var scheme = runtime.NewScheme()

var _ = BeforeSuite(func(done Done) {
	logf.SetLogger(zap.New(zap.WriteTo(GinkgoWriter), zap.UseDevMode(true)))

	customApiServerFlags := []string{
		"--secure-port=6884",
	}

	apiServerFlags := append([]string(nil), envtest.DefaultKubeAPIServerFlags...)
	apiServerFlags = append(apiServerFlags, customApiServerFlags...)

	SiteBuilderImage = "gitlab-registry.cern.ch/drupal/paas/drupal-runtime/site-builder"
	VeleroNamespace = "openshift-cern-drupal"
	PhpFpmExporterImage = "test-phpfpmexporter"
	WebDAVImage = "test-webdav"
	DefaultD8ReleaseSpec = "test-d8-spec"
	DefaultD9ReleaseSpec = "test-d9-spec"
	ClusterName = "test"

	By("bootstrapping test environment")
	testEnv = &envtest.Environment{
		CRDDirectoryPaths: []string{
			filepath.Join("..", "config", "crd", "bases"),
			filepath.Join("..", "testResources", "mock_crd"),
		},
		BinaryAssetsDirectory: filepath.Join("..", "testbin", "bin"),
		ErrorIfCRDPathMissing: true,
		KubeAPIServerFlags:    apiServerFlags,
		// AttachControlPlaneOutput: true,
	}
	err := drupalwebservicesv1alpha1.AddToScheme(scheme)
	Expect(err).NotTo(HaveOccurred())
	// +kubebuilder:scaffold:scheme

	err = clientgoscheme.AddToScheme(scheme)
	Expect(err).NotTo(HaveOccurred())

	err = dbodv1a1.AddToScheme(scheme)
	Expect(err).NotTo(HaveOccurred())

	err = buildv1.AddToScheme(scheme)
	Expect(err).NotTo(HaveOccurred())

	err = appsv1.AddToScheme(scheme)
	Expect(err).NotTo(HaveOccurred())

	err = routev1.AddToScheme(scheme)
	Expect(err).NotTo(HaveOccurred())

	err = imagev1.AddToScheme(scheme)
	Expect(err).NotTo(HaveOccurred())

	err = authz.AddToScheme(scheme)
	Expect(err).NotTo(HaveOccurred())

	err = velerov1.AddToScheme(scheme)
	Expect(err).NotTo(HaveOccurred())

	cfg, err := testEnv.Start()
	Expect(err).NotTo(HaveOccurred())
	Expect(cfg).NotTo(BeNil())

	k8sManager, err := ctrl.NewManager(cfg, ctrl.Options{
		Scheme: scheme,
	})
	Expect(err).ToNot(HaveOccurred())

	err = (&DrupalSiteReconciler{
		Client: k8sManager.GetClient(),
		Scheme: k8sManager.GetScheme(),
		Log:    ctrl.Log.WithName("controllers").WithName("DrupalSite"),
	}).SetupWithManager(k8sManager)
	Expect(err).ToNot(HaveOccurred())

	go func() {
		err = k8sManager.Start(ctrl.SetupSignalHandler())
		Expect(err).ToNot(HaveOccurred())
	}()

	k8sClient = k8sManager.GetClient()
	Expect(k8sClient).ToNot(BeNil())

	close(done)
}, 100)

var _ = AfterSuite(func() {
	By("tearing down the test environment")
	err := testEnv.Stop()
	Expect(err).NotTo(HaveOccurred())
})
