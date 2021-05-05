# drupalSite-operator

Kubernetes operator that controls the main API of the Drupal service: the DrupalSite CRD.

For an introduction to the Operator pattern and how we use it, take a look at our presentation at Kubecon EU 2021!

### [Building a Kubernetes infrastructure for CERN's Content Management Systems](https://zenodo.org/record/4730874)

This [paper](https://zenodo.org/record/4730874) describes the use case served with the `drupalsite-operator`.
Flip through it to get some context!

### Drupal service architecture

The Drupal service is designed around the concept of the DrupalSite.
The deployment looks like this:

![architecture diagram](docs/drupal-architecture-full.png)

![images diagram](docs/drupal-images.svg)

The [architecture description](docs/README.md) explains in more detail.

## CRDs

### [DrupalSite](config/samples/)

A `DrupalSite` defines all the necessary info for the operator to instantiate a Drupal website, integrated with the CERN environment.
Example:

```yaml
apiVersion: drupal.webservices.cern.ch/v1alpha1
kind: DrupalSite
metadata:
  name: drupalsite-sample
spec:
  # Create an ingress route?
  publish: true
  # Optional: URL to request in the route.
  # If omitted, the default URL is `<spec.environment.name>-<meta.namespace>.<operatorConfig.defaultDomain>`
  # And for `environment.name == "production"`, it is simplified to `<meta.namespace>.<operatorConfig.defaultDomain>`
  siteUrl: mysite.webtest.cern.ch
  # Generates the image tags. Changing this triggers the upgrade workflow.
  drupalVersion: "8.9.14"
  environment:
    # Non-production environments can be specified to test changes starting from the current state of another DrupalSite
    name: "dev"
    # Name of the DrupalSite (in the same namespace) to clone
    initCloneFrom: "myproductionsite"
    qosClass: "standard"
    dbodClass: "test"
  diskSize: "1Gi"
```

## Running the operator

### Deployment

The operator is packaged with a [helm chart](chart/drupalsite-operator).
However, we **deploy [CRDs](config/crd/bases) separately**. Both must be deployed for the operator to function.
In our infrastructure, we deploy the operator and its CRD with 2 separate ArgoCD Applications.

### Configuration

hen deploying the Helm chart, operator configuration is exposed as Helm values.
This reference is useful to run the operator locally.

#### environment variables

 env var | example | description
 --- | --- | ---
`DEFAULT_DOMAIN`  | `webtest.cern.ch`           | Route's Host field
`RUNTIME_REPO` | `https://gitlab.cern.ch/drupal/paas/drupal-runtime.git@master` | Specify the git repo and commit that the operator will use to populate configmaps for nginx/php/drupal. `@<COMMIT_SHA>` or any git refspec can be specified, and the operator will use the the configuration specified there

#### cmdline arguments

argument | example | description
--- | --- | ---
`release-channel` | "stable" | If specified, the operator will append this value to image tags for server deployments

## Developed with [operator-sdk](https://sdk.operatorframework.io/)

This project was generated with the [operator-sdk](https://sdk.operatorframework.io/)
and has been updated to `operator-sdk-v1.3`.
