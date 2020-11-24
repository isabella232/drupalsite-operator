#!/bin/sh
set -ex

# Setup drupal site
# /init-app.sh

# Run Nginx
# For debugging change nginx to nginx-debug
exec nginx -g "daemon off;"