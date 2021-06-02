module gitlab.cern.ch/drupal/paas/drupalsite-operator

go 1.15

require (
	github.com/asaskevich/govalidator v0.0.0-20190424111038-f61b66f89f4a
	github.com/go-logr/logr v0.3.0
	github.com/onsi/ginkgo v1.14.1
	github.com/onsi/gomega v1.10.2
	github.com/openshift/api v0.0.0-20210127195806-54e5e88cf848
	github.com/operator-framework/operator-lib v0.1.0
	gitlab.cern.ch/drupal/paas/dbod-operator v0.0.0-20210525082629-c9e903df3b0e
	gitlab.cern.ch/paas-tools/operators/authz-operator v0.0.0-20210512233547-21c01c7dd5e5
	k8s.io/api v0.19.2
	k8s.io/apimachinery v0.19.2
	k8s.io/client-go v0.19.2
	k8s.io/utils v0.0.0-20200912215256-4140de9c8800
	sigs.k8s.io/controller-runtime v0.7.2
)
