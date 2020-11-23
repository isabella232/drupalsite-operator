ARG PHP_BASE_VERSION

FROM gitlab-registry.cern.ch/drupal/paas/drupalsite-operator/php-base:${PHP_BASE_VERSION}

ARG DRUPAL_VERSION
ARG COMPOSER_VERSION
ARG SETTINGS_FILE_NAME

LABEL io.k8s.description="Drupal managed infra site-builder" \
      io.k8s.display-name="Drupal 8 Managed Infra site-builder" \
      io.openshift.tags="managed,drupal,site-builder" \
      maintainer="Drupal Admins <drupal-admins@cern.ch>"

ADD fix-permissions /fix-permissions
RUN chmod +x /fix-permissions

# Install composer
# Get releases from https://github.com/composer/composer/releases
ENV COMPOSER_VERSION ${COMPOSER_VERSION:-1.9.0}
ENV COMPOSER_HOME ${DRUPAL_APP_DIR}
RUN curl -sS https://getcomposer.org/installer | php -- --install-dir=/usr/bin --filename=composer --version=${COMPOSER_VERSION}

# DRUPAL specific. Include drupal tools in the PATH
# DRUPAL_APP_DIR is the drupal final destination. Must be in consonance with nginx.
# DRUPAL_CODE_DIR is used to pre-set drupal code.
ENV DRUPAL_APP_DIR /app

# From here we will manage all central/non central modules + drupal version
# RUN git clone -b okd-manual-deployment https://gitlab.cern.ch/drupal/paas/composer-drupal-project ${DRUPAL_CODE_DIR}    

ENV COMPOSER_MEMORY_LIMIT -1
ENV DRUPAL_VERSION=${DRUPAL_VERSION:-8}
# Note: to review if we will use the project `drupal/paas/composer-drupal-project` again to specify the standard modules we provide
RUN composer create-project drupal/recommended-project:${DRUPAL_VERSION} ${DRUPAL_APP_DIR} --no-interaction

ENV HOME ${DRUPAL_APP_DIR}
WORKDIR ${DRUPAL_APP_DIR}
# Composer as root/super user! See https://getcomposer.org/root for details
# Set up drupal minimum stack
# RUN composer install --optimize-autoloader --no-cache -vv	

# TO BE REMOVED
# Web Team modules and themes. This needs to be removed in favour of composer
ADD toremove.sh /
RUN chmod +x /toremove.sh && /toremove.sh
# TO BE REMOVED

# Add extra configurations
# At this point, composer has created the required settings.php through:
# post-update-cmd: DrupalProject\composer\ScriptHandler::createRequiredFiles
# Overwrite settings.php with ours.
# - settings.php
ADD config/sites/default/${SETTINGS_FILE_NAME} ${DRUPAL_APP_DIR}/web/sites/default/settings.php
# Remove ${DRUPAL_APP_DIR}/web/sites/default/files, preparing it to be symbolic link after init-app.sh;
RUN rm -rf ${DRUPAL_APP_DIR}/web/sites/default/files
# Fix-permissions
RUN /fix-permissions ${DRUPAL_APP_DIR}

# Add init-container for every container that contains drupal code
# ADD init-app.sh /
# RUN chmod +x /init-app.sh