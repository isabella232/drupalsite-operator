# from https://www.drupal.org/docs/8/system-requirements/drupal-8-php-requirements
# from https://github.com/docker-library/docs/blob/master/php/README.md#supported-tags-and-respective-dockerfile-links
ARG PHP_VERSION

FROM php:${PHP_VERSION}

LABEL io.k8s.description="Drupal managed infra base" \
      io.k8s.display-name="Drupal 8 Managed Infra base" \
      io.openshift.tags="managed,drupal,php,nginx" \
      maintainer="Drupal Admins <drupal-admins@cern.ch>"

# install some utils
RUN apk --update add \
    # Some composer packages need git    
    git \
    patch \
    curl \
    gettext \
    zip \
    unzip \
    mysql-client \
    jq \
    tzdata

# Configured timezone.
ENV TZ=Europe/Zurich
RUN touch /usr/share/zoneinfo/$TZ \
	&& cp /usr/share/zoneinfo/$TZ /etc/localtime \
	&& echo $TZ > /etc/timezone && \
	apk del tzdata \
	&& rm -rf /var/cache/apk/*

# PHP FPM
RUN set -eux; \
	\
	apk add --no-cache --virtual .build-deps autoconf g++ make \
		coreutils \
		freetype-dev \
		libjpeg-turbo-dev \
		libpng-dev \
		libzip-dev \
	; \
    \
    pecl install -o -f redis \
    ; \
    \
    rm -rf /tmp/pear \
    ; \
	\
	docker-php-ext-configure gd \
		--with-freetype-dir=/usr/include \
		--with-jpeg-dir=/usr/include \
		--with-png-dir=/usr/include \
	; \
	\
	docker-php-ext-install -j "$(nproc)" \
		gd \
		opcache \
		pdo_mysql \
		zip \
	; \
	\
	runDeps="$( \
		scanelf --needed --nobanner --format '%n#p' --recursive /usr/local \
			| tr ',' '\n' \
			| sort -u \
			| awk 'system("[ -e /usr/local/lib/" $1 " ]") == 0 { next } { print "so:" $1 }' \
	)"; \
	apk add --virtual .drupal-phpexts-rundeps $runDeps; \
	apk del .build-deps