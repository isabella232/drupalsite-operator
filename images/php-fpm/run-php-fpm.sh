#!/bin/sh
set -x

# Setup drupal site
# /init-app.sh

## OPERATOR ACTIONS
# If site does not exist, install it.
if [[ ${ENVIRONMENT} == 'production' ] && [ ! drush status --fields=bootstrap | grep -q 'Successful' ]]; then
    
    sitename=$NAMESPACE.web.cern.ch
    drush -y si cern \
    --db-url=mysql://$DB_USER:$DB_PASSWORD@$DB_HOST:$DB_PORT/$DB_NAME \
    --account-name=admin \
    --site-name=$sitename \
    $VERBOSITY

    # Check if provisioning was successful, otherwise exit.
    if [ $? -ne 0 ]; then
      exit 1;
    fi

    # Clean caches
    drush -y cr
fi

## TESTING
# If there are files in /tmp/configmap that are not empty
# (overriden by a ConfigMap) copy them
if [ -n "$(ls -A /tmp/settings)" ]; then
      cat /tmp/settings/settings.${ENVIRONMENT}.php > ${DRUPAL_APP_DIR}/web/sites/default/settings.${ENVIRONMENT}.php
      #chmod 444 ${DRUPAL_APP_DIR}/web/sites/default/settings.${ENVIRONMENT}.php
fi
## TESTING

echo "---> Running PHP-FPM..."
exec php-fpm
