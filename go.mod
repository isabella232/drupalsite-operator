module gitlab.cern.ch/drupal/paas/drupalsite-operator

go 1.16

require (
	cloud.google.com/go/container v1.0.0 // indirect
	cloud.google.com/go/monitoring v1.2.0 // indirect
	cloud.google.com/go/trace v1.0.0 // indirect
	github.com/asaskevich/govalidator v0.0.0-20190424111038-f61b66f89f4a
	github.com/go-logr/logr v0.4.0
	github.com/google/go-containerregistry v0.7.0
	github.com/onsi/ginkgo v1.16.4
	github.com/onsi/gomega v1.13.0
	github.com/openshift/api v0.0.0-20210127195806-54e5e88cf848
	github.com/operator-framework/operator-lib v0.1.0
	github.com/tektoncd/pipeline v0.26.0
	github.com/vmware-tanzu/velero v1.6.1
	gitlab.cern.ch/drupal/paas/dbod-operator v0.0.0-20210525082629-c9e903df3b0e
	gitlab.cern.ch/paas-tools/operators/authz-operator v0.0.0-20210512233547-21c01c7dd5e5
	k8s.io/api v0.21.2
	k8s.io/apimachinery v0.21.2
	k8s.io/client-go v0.21.2
	k8s.io/utils v0.0.0-20210527160623-6fdb442a123b
	knative.dev/pkg v0.0.0-20210510175900-4564797bf3b7
	sigs.k8s.io/controller-runtime v0.9.2
)
