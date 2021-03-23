# drupalSite-operator

Kubernetes operator that controls the main API of the Drupal service: the DrupalSite CRD.

## Drupal service architecture

The Drupal service is designed around the concept of the DrupalSite.
The deployment looks like this:

![architecture diagram](docs/drupal-design.svg)

The [architecture description](docs/README.md) explains in more detail.

## Operator scaffolding

This project was generated with the [operator-sdk](https://sdk.operatorframework.io/)
and has been updated to `operator-sdk-v1.3`.


## Setup to run the Operator Locally

Required environment variables:

 env var | example | description
 --- | --- | ---
`DEFAULT_DOMAIN`  | `webtest.cern.ch`           | TODO.
`RUNTIME_REPO` | `https://gitlab.cern.ch/drupal/paas/drupal-runtime.git@master` | Allows to select the images that will be used by the operator, if `@feature-branch` will use the images set on the feature branch (Can be used commit hash as well)
