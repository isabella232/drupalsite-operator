#!/bin/sh
set -x

# Modules
git clone --depth=1 --single-branch --branch "v1.1.0" https://gitlab.cern.ch/web-team/drupal/public/d8/modules/twig-functions-library.git ${DRUPAL_APP_DIR}/web/modules/contrib/twig_functions_library    

git clone --depth=1 --single-branch --branch "v2.1.1" https://gitlab.cern.ch/web-team/drupal/public/d8/modules/paragraph-types.git ${DRUPAL_APP_DIR}/web/modules/contrib/cern_paragraph_types
    
git clone --depth=1 --single-branch --branch "v2.0" https://gitlab.cern.ch/web-team/drupal/public/d8/modules/cern-loading.git ${DRUPAL_APP_DIR}/web/modules/contrib/cern_loading
    
git clone --depth=1 --single-branch --branch "v2.2.0" https://gitlab.cern.ch/web-team/drupal/public/d8/modules/cern-landing-page.git ${DRUPAL_APP_DIR}/web/modules/contrib/cern_landing_page
    
git clone --depth=1 --single-branch --branch "v2.0" https://gitlab.cern.ch/web-team/drupal/public/d8/modules/cern-full-html-format.git ${DRUPAL_APP_DIR}/web/modules/contrib/cern_full_html_format

git clone --depth=1 --single-branch --branch "v2.6.1" https://gitlab.cern.ch/web-team/drupal/public/d8/modules/cern-components.git ${DRUPAL_APP_DIR}/web/modules/contrib/cern_components    

git clone --depth=1 --single-branch --branch "v1.4.0" https://gitlab.cern.ch/web-team/drupal/public/d8/modules/cern-display-formats.git ${DRUPAL_APP_DIR}/web/modules/contrib/cern_display_formats
    
git clone --depth=1 --single-branch --branch "v2.2.0" https://gitlab.cern.ch/web-team/drupal/public/d8/modules/cern-toolbar.git ${DRUPAL_APP_DIR}/web/modules/contrib/cern_toolbar    

git clone --depth=1 --single-branch --branch "v2.1.1" https://gitlab.cern.ch/web-team/drupal/public/d8/modules/cern-cds-media.git ${DRUPAL_APP_DIR}/web/modules/contrib/cern_cds_media    

git clone --depth=1 --single-branch --branch "v2.1.0" https://gitlab.cern.ch/web-team/drupal/public/d8/modules/cern-dev-status.git ${DRUPAL_APP_DIR}/web/modules/contrib/cern_dev_status    

# Themes
git clone --depth=1 --single-branch --branch "v2.5.1" https://gitlab.cern.ch/web-team/drupal/public/d8/themes/cernbase.git ${DRUPAL_APP_DIR}/web/themes/custom/cernbase    

git clone --depth=1 --single-branch --branch "v2.4.3" https://gitlab.cern.ch/web-team/drupal/public/d8/themes/cern.git ${DRUPAL_APP_DIR}/web/themes/custom/cernclean    
