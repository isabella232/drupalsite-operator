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
`DEFAULT_DOMAIN`  | `webtest.cern.ch`           | Route's Host field
`RUNTIME_REPO` | `https://gitlab.cern.ch/drupal/paas/drupal-runtime.git@master` | Specify the git repo and commit that the operator will use to populate configmaps for nginx/php/drupal. `@<COMMIT_SHA>` or any git refspec can be specified, and the operator will use the the configuration specified there
