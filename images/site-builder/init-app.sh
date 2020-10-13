#!/bin/sh
set -ex

# Set drupal under target directory
cp -Rf ${DRUPAL_CODE_DIR}/. ${DRUPAL_APP_DIR}/

### Pre create folders under ${DRUPAL_SHARED_VOLUME}
# Check if files folder exists, otherwise create it.    
if [ ! -d "${DRUPAL_SHARED_VOLUME}/files" ]; then
    mkdir -p ${DRUPAL_SHARED_VOLUME}/files
fi

# Check if private folder exists, otherwise create it.
if [ ! -d "${DRUPAL_SHARED_VOLUME}/private" ]; then
    mkdir -p ${DRUPAL_SHARED_VOLUME}/private
fi

if [ ! -d "${DRUPAL_SHARED_VOLUME}/modules" ]; then
    mkdir -p ${DRUPAL_SHARED_VOLUME}/modules
fi

if [ ! -d "${DRUPAL_SHARED_VOLUME}/themes" ]; then
    mkdir -p ${DRUPAL_SHARED_VOLUME}/themes
fi

# Set symlinks
ln -s ${DRUPAL_SHARED_VOLUME}/files ${DRUPAL_APP_DIR}/web/sites/default/files
ln -s ${DRUPAL_SHARED_VOLUME}/modules ${DRUPAL_APP_DIR}/web/sites/default/modules
ln -s ${DRUPAL_SHARED_VOLUME}/themes ${DRUPAL_APP_DIR}/web/sites/default/themes

# The directory sites/default is not protected from modifications and poses a security risk.
# Change the directory's permissions to be non-writable is needed.
chmod -R 555 ${DRUPAL_APP_DIR}/web/sites/default
chmod 444 ${DRUPAL_APP_DIR}/web/sites/default/settings.php