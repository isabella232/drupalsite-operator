<?php
// CONSIDER USING THIS AS CONFIG MAP
//
//https://api.drupal.org/api/drupal/sites%21example.settings.local.php/8.7.x
/**
 * Show all error messages, with backtrace information.
 *
 * In case the error level could not be fetched from the database, as for
 * example the database connection failed, we rely only on this value.
 */
# Check https://jimconte.com/blog/web/drupal-8-environment-specific-configurations
# ================================================================
# disable page cache and other cache bins
# ================================================================
#$cache_bins = array('bootstrap','config','data','default','discovery','dynamic_page_cache','entity','menu','migrate','render','rest','static','toolbar');
#foreach ($cache_bins as $bin) {
#  $settings['cache']['bins'][$bin] = 'cache.backend.null';
#}

$config['system.logging']['error_level'] = 'verbose';

$trusted_host_pattern="^" . getenv('NAMESPACE') . "\.apptest\.cern\.ch$";
$settings['trusted_host_patterns'] = array(
    $trusted_host_pattern,
);
