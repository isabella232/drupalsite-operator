ARG PHP_BASE_VERSION

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

COPY ./drupal-files/app ${DRUPAL_APP_DIR}
# COPY --from=builder /fix-permissions /fix-permissions
# COPY --from=builder /init-app.sh /init-app.sh

WORKDIR ${DRUPAL_APP_DIR}
# Fix permissions on target folder to copy there drupal code.
# RUN /fix-permissions ${DRUPAL_APP_DIR}
RUN ls
# Add extra configurations
# At this point, composer has created the required settings.php through post-update-cmd: DrupalProject\composer\ScriptHandler::createRequiredFiles
# Overwrite settings.php with ours.
# - php-fpm
ADD config/php-fpm/ /usr/local/etc/php-fpm.d/
# - Simplesamlphp
ADD config/simplesamlphp/ ${DRUPAL_APP_DIR}/vendor/simplesamlphp/simplesamlphp/
# - opcache
ADD config/opcache/ /usr/local/etc/php/conf.d/


ADD run-php-fpm.sh hooks/post-hook.sh /
RUN chmod +x /run-php-fpm.sh /post-hook.sh

RUN ln -s ${DRUPAL_SHARED_VOLUME}/files ${DRUPAL_APP_DIR}/web/sites/default/files
RUN ln -s ${DRUPAL_SHARED_VOLUME}/modules ${DRUPAL_APP_DIR}/web/sites/default/modules
RUN ln -s ${DRUPAL_SHARED_VOLUME}/themes ${DRUPAL_APP_DIR}/web/sites/default/themes

# The directory sites/default is not protected from modifications and poses a security risk.
# Change the directory's permissions to be non-writable is needed.
RUN chmod -R 555 ${DRUPAL_APP_DIR}/web/sites/default
RUN chmod 444 ${DRUPAL_APP_DIR}/web/sites/default/settings.php

ENTRYPOINT ["/run-php-fpm.sh"]