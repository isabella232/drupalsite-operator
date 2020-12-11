#!/bin/sh
set -x

drush site-install -y --config-dir=../config/sync --account-name=admin --account-pass=pass --account-mail=admin@example.com