# drupalSite-operator

Operator to manage the internal DrupalSite resource that defines a Drupal site in the drupal-containers infrastructure

## Versions

| Tool | Version |
| ----------- | ----------- |
| operator-sdk | 1.0.1 |
| oc | 4.5.0 |

## Operator scaffolding

`operator-sdk init --domain=webservices.cern.ch --repo=gitlab.cern.ch/drupal/paas/drupalsite-operator`

## Building docker images

Refer to the following table for setting CI variables for building drupal 8/9 accordingly

| Variable | Description | Defaults | Drupal 8.x | Drupal 9.x | Remarks
| ----------- | ----------- | ----------- | ----------- | ----------- | ----------- |
| CI_COMPOSER_VERSION | Controls the composer version to be installed | 1.10.15 | 1.10.15 | 1.10.15 | Can be modified |
| CI_DRUPAL_VERSION | Controls the drupal version to be used | 8.9.7 | 8.x.x | 9.x.x | Any desired drupal minor version can be used |
| CI_NGINX_VERSION | Controls the nginx image version to be pulled | 1.17.4 | 1.17.4 | 1.17.4 | Can be modified |
| CI_PHP_BASE_VERSION | Controls the php-base image version to be created | latest | latest | latest | latest | Can be modified |
| CI_PHP_VERSION | Controls the php image version to be pulled | 7.3.23-fpm-alpine3.12 | 7.3.23-fpm-alpine3.12 | 7.3.23-fpm-alpine3.12 | Can be modified. Upgrading to 7.4 or above might require changes |
| CI_PROJECT_ID | Gitlab project ID | 102490 | 102490 | 102490 | Can not be modified
| CI_SETTINGS_FILE_NAME | Settings file to be used based on drupal 8/9 | settings-d8.php | settings-d8.php | settings-d9.php | Only one of the two files can be chosen |
| CI_SITE_BUILDER_VERSION | Controls the site builder image version to be created | latest | latest | latest | latest | Can be modified |

Over the required variables accordingly by running a manual pipeline.

**Be sure to overwrite CI_SETTINGS_FILE_NAME variable if you are also changing DRUPAL_VERSION variable accordingly**

## Running the template
1. Install the template using the command
    `oc create -f spec/template.yaml`
2. From OKD console, go to the path `catalog/instantiate-template?template=drupal-template&template-ns=<Namespace where tempalte is installed`
    1. Select the namespace
    2. Enter an APP_NAME. Do not leave it empty. Do not include capitals or any other characters, that aren't supported with DNS
    3. Enter DRUPAL_VERSION
    4. ENTER CLUSTER_NAME. Which is usually the sub-domain of the console url before `cern.ch`
3. From CLI, run the following command to initiate the template
    `oc process drupal-template -p APP_NAME=<app-name> -p DRUPAL_VERSION=<drupal-version> -p CLUSTER_NAME=<cluster-name> | oc create -f -`