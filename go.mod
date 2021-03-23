module gitlab.cern.ch/drupal/paas/drupalsite-operator

go 1.15

require (
	github.com/asaskevich/govalidator v0.0.0-20190424111038-f61b66f89f4a
	github.com/go-logr/logr v0.4.0
	github.com/onsi/ginkgo v1.14.1
	github.com/onsi/gomega v1.10.3
	github.com/openshift/api v0.0.0-20201126092428-04abbec6c099
	github.com/operator-framework/operator-lib v0.1.0
	github.com/tektoncd/pipeline v0.22.0
	gitlab.cern.ch/drupal/paas/dbod-operator v0.0.0-20210312144809-29eb48685cf7
	k8s.io/api v0.19.7
	k8s.io/apimachinery v0.19.7
	k8s.io/client-go v0.19.7
	k8s.io/utils v0.0.0-20210111153108-fddb29f9d009
	sigs.k8s.io/controller-runtime v0.7.0
)
