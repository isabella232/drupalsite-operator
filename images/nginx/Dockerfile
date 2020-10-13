ARG SITE_BUILDER_VERSION
ARG NGINX_VERSION

FROM gitlab-registry.cern.ch/drupal/paas/drupalsite-operator/site-builder:${SITE_BUILDER_VERSION} as builder
FROM nginx:${NGINX_VERSION}

LABEL io.k8s.description="Drupal managed infra nginx" \
      io.k8s.display-name="Drupal 8 Managed Infra nginx" \
      io.openshift.tags="managed,drupal,nginx" \
      maintainer="Drupal Admins <drupal-admins@cern.ch>"

ENV DRUPAL_APP_DIR /app
ENV DRUPAL_CODE_DIR /code
ENV DRUPAL_SHARED_VOLUME /drupal-data

COPY --from=builder ${DRUPAL_CODE_DIR} ${DRUPAL_CODE_DIR}
COPY --from=builder /fix-permissions /fix-permissions
COPY --from=builder /init-app.sh /init-app.sh

# NGINX Configuration
ADD config/nginx.conf /etc/nginx/nginx.conf
ADD config/default.conf /etc/nginx/conf.d/default.conf
### n.b.: https://www.redpill-linpro.com/sysadvent/2017/12/10/jekyll-openshift.html
RUN \
  mkdir -p /etc/nginx/ /var/run /var/cache/nginx /var/lib/nginx /var/log/nginx /var/tmp/nginx/ && \
  chgrp -R 0 /etc/nginx/ /var/run /var/cache/nginx /var/lib/nginx /var/log/nginx /var/tmp/nginx/ && \
  chmod -R g=u /etc/nginx/ /var/run /var/cache/nginx /var/lib/nginx /var/log/nginx /var/tmp/nginx/ && \
  sed -i 's,/run/nginx.pid,/lib/nginx/nginx.pid,g' /etc/nginx/nginx.conf && \
  sed -i -e '/^user/d' /etc/nginx/nginx.conf

WORKDIR ${DRUPAL_APP_DIR}
# Fix permissions on target folder to copy there drupal code.
RUN /fix-permissions ${DRUPAL_APP_DIR}

ADD run-nginx.sh /
RUN chmod +x /run-nginx.sh

ENTRYPOINT ["/run-nginx.sh"]
