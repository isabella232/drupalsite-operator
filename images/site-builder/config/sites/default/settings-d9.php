<?php
// @codingStandardsIgnoreFile
// Database settings.
$databases['default']['default'] = [
  'host' => getenv('DB_HOST'),
  'port' => getenv('DB_PORT'),
  'database' =>  getenv('DB_NAME'),
  'username' => getenv('DB_USER'),
  'password' => getenv('DB_PASSWORD'),
  'namespace' => 'Drupal\Core\Database\Driver\mysql',
  'driver' => 'mysql',
  'prefix' => '',
  'collation' => 'utf8mb4_general_ci',
];

// Location of the site configuration files.
$settings['config_sync_directory'] = '../config/sync';

// Configure file paths
// Private file paths.
$settings['file_private_path'] = getenv('DRUPAL_SHARED_VOLUME') . '/private';
// Configure tmp folder
$settings['file_temp_path'] = '/tmp';

// Configure feeds tmp folder
// Fix: https://www.drupal.org/project/feeds/issues/2912130
if (isset($config['system.file']) && !is_null($config['system.file']['path']['temporary']))
{
    $config['system.file']['path']['temporary'] = getenv('DRUPAL_SHARED_VOLUME') . "/private/feeds/tmp";
}

// Config trusted host pattern
$trusted_host_pattern="^". str_replace(".","\.",getenv('HOSTNAME')) . "$";
$settings['trusted_host_patterns'] = [ '.*' ];

// Salt for one-time login links, cancel links, form tokens, etc.
$settings['hash_salt'] = hash("sha256",getenv('DB_NAME') . getenv('DB_USER') . getenv('DB_PASS'));
/**
 * Load services definition file.
 */
$settings['container_yamls'][] = $app_root . '/' . $site_path . '/services.yml';
/**
 * The default list of directories that will be ignored by Drupal's file API.
 *
 * By default ignore node_modules folders to avoid issues
 * with common frontend tools and recursive scanning of directories looking for
 * extensions.
 *
 * @see file_scan_directory()
 * @see \Drupal\Core\Extension\ExtensionDiscovery::scanDirectory()
 */
$settings['file_scan_ignore_directories'] = [
  'node_modules',
];

/**
 * Redis Configuration.
 * @See https://pantheon.io/docs/redis/ > No Keys Found
 * @See also https://gist.github.com/keopx/7d5fe4d7a890c792c43bb79cf56718e0
 * @ See also https://docs.platform.sh/frameworks/drupal8/redis.html
 */

if (extension_loaded('redis')) {
  // Set Redis as the default backend for any cache bin not otherwise specified.
  $settings['cache']['default'] = 'cache.backend.redis';
  $settings['redis.connection']['interface'] = 'PhpRedis';
  $settings['redis.connection']['host'] = 'redis.' . getenv('NAMESPACE') . '.svc.cluster.local';
  $settings['redis.connection']['port'] = getenv('REDIS_SERVICE_PORT');

  // Allow the services to work before the Redis module itself is enabled.
  $settings['container_yamls'][] = 'modules/contrib/redis/redis.services.yml';
  // Manually add the classloader path, this is required for the container cache bin definition below
  // and allows to use it without the redis module being enabled.
  $class_loader->addPsr4('Drupal\\redis\\', 'modules/contrib/redis/src');
  // Use redis for container cache.
  // The container cache is used to load the container definition itself, and
  // thus any configuration stored in the container itself is not available
  // yet. These lines force the container cache to use Redis rather than the
  // default SQL cache.
  $settings['bootstrap_container_definition'] = [
    'parameters' => [],
    'services' => [
      'redis.factory' => [
        'class' => 'Drupal\redis\ClientFactory',
      ],
      'cache.backend.redis' => [
        'class' => 'Drupal\redis\Cache\CacheBackendFactory',
        'arguments' => ['@redis.factory', '@cache_tags_provider.container', '@serialization.phpserialize'],
      ],
      'cache.container' => [
        'class' => '\Drupal\redis\Cache\PhpRedis',
        'factory' => ['@cache.backend.redis', 'get'],
        'arguments' => ['container'],
      ],
      'cache_tags_provider.container' => [
        'class' => 'Drupal\redis\Cache\RedisCacheTagsChecksum',
        'arguments' => ['@redis.factory'],
      ],
      'serialization.phpserialize' => [
        'class' => 'Drupal\Component\Serialization\PhpSerialize',
      ],
    ],
  ];
  
  /** Optional prefix for cache entries */
  $settings['cache_prefix'] = getenv('NAMESPACE');
  
  /** @see: https://pantheon.io/docs/redis/ */
  // Always set the fast backend for bootstrap, discover and config, otherwise
  // this gets lost when redis is enabled.
  $settings['cache']['bins']['bootstrap'] = 'cache.backend.chainedfast';
  $settings['cache']['bins']['discovery'] = 'cache.backend.chainedfast';
  $settings['cache']['bins']['config'] = 'cache.backend.chainedfast';
  /** @see: https://github.com/md-systems/redis */
  // Use for all bins otherwise specified.
  $settings['cache']['default'] = 'cache.backend.redis';
  // Use this to only use it for specific cache bins.
  $settings['cache']['bins']['render'] = 'cache.backend.redis';
  // Use for all queues unless otherwise specified for a specific queue.
  $settings['queue_default'] = 'queue.redis';
  // Or if you want to use reliable queue implementation.
  $settings['queue_default'] = 'queue.redis_reliable';
  // Use this to only use Redis for a specific queue (aggregator_feeds in this case).
  $settings['queue_service_aggregator_feeds'] = 'queue.redis';
  // Or if you want to use reliable queue implementation.
  $settings['queue_service_aggregator_feeds'] = 'queue.redis_reliable';
}

/**
 * Load local development override configuration, if available.
 *
 * Use settings.local.php to override variables on secondary (staging,
 * development, etc) installations of this site. Typically used to disable
 * caching, JavaScript/CSS compression, re-routing of outgoing emails, and
 * other things that should not happen on development and testing sites.
 *
 * Keep this code block at the end of this file to take full effect.
 */
if (getenv('ENVIRONMENT') != 'production' && file_exists($app_root . '/' . $site_path . '/settings.' . getenv('ENVIRONMENT') . '.php')) {
  include $app_root . '/' . $site_path . '/settings.' . getenv('ENVIRONMENT') . '.php';
}
