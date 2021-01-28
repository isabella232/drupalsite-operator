ARG SITE_BUILDER_VERSION
ARG PHP_BASE_VERSION

FROM gitlab-registry.cern.ch/drupal/paas/drupalsite-operator/site-builder:${SITE_BUILDER_VERSION} as builder
FROM gitlab-registry.cern.ch/drupal/paas/drupalsite-operator/php-base:${PHP_BASE_VERSION}

LABEL io.k8s.description="Drupal managed infra php-fpm" \
      io.k8s.display-name="Drupal 8 Managed Infra php-fpm" \
      io.openshift.tags="managed,drupal,php-fpm" \
      maintainer="Drupal Admins <drupal-admins@cern.ch>"

# DRUPAL specific. Include drupal tools in the PATH
# DRUPAL_APP_DIR path must be in consoncance with Nginx configuration
ENV DRUPAL_APP_DIR /app
# ENV DRUPAL_CODE_DIR /code
ENV DRUPAL_SHARED_VOLUME /drupal-data

COPY --from=builder ${DRUPAL_APP_DIR} ${DRUPAL_APP_DIR}
COPY --from=builder /fix-permissions /fix-permissions
# COPY --from=builder /init-app.sh /init-app.sh

COPY --from=builder /root/.composer /root/.composer
COPY --from=builder /usr/bin/composer /usr/bin/composer

WORKDIR ${DRUPAL_APP_DIR}
# Fix permissions on target folder to copy there drupal code.
# RUN /fix-permissions ${DRUPAL_APP_DIR}

RUN ln -s ${DRUPAL_SHARED_VOLUME}/files ${DRUPAL_APP_DIR}/web/sites/default/files
RUN ln -s ${DRUPAL_SHARED_VOLUME}/modules ${DRUPAL_APP_DIR}/web/sites/default/modules
RUN ln -s ${DRUPAL_SHARED_VOLUME}/themes ${DRUPAL_APP_DIR}/web/sites/default/themes

ENV DRUSH_VERSION 10.3.6

RUN COMPOSER_MEMORY_LIMIT=-1 composer require drush/drush ${DRUSH_VERSION}; composer install;

ENV PATH=$PATH:${DRUPAL_APP_DIR}/vendor/bin