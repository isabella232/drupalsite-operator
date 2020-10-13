<?php
/**
 * SAML 2.0 remote IdP metadata for SimpleSAMLphp.
 *
 * Remember to remove the IdPs you don't use from this file.
 *
 * See: https://simplesamlphp.org/docs/stable/simplesamlphp-reference-idp-remote
 */

$metadata['https://cern.ch/login'] = array (
  'entityid' => 'https://cern.ch/login',
  'description' => 
  array (
    'en-US' => 'CERN',
  ),
  'OrganizationName' => 
  array (
    'en-US' => 'CERN',
  ),
  'name' => 
  array (
    'en-US' => 'CERN',
  ),
  'OrganizationDisplayName' => 
  array (
    'en-US' => 'CERN',
  ),
  'url' => 
  array (
    'en-US' => 'http://cern.ch/',
  ),
  'OrganizationURL' => 
  array (
    'en-US' => 'http://cern.ch/',
  ),
  'contacts' => 
  array (
    0 => 
    array (
      'contactType' => 'support',
      'company' => 'CERN',
      'givenName' => 'Service',
      'surName' => 'Desk',
      'emailAddress' => 
      array (
        0 => 'service-desk@cern.ch',
      ),
      'telephoneNumber' => 
      array (
        0 => '+41227678888',
      ),
    ),
  ),
  'metadata-set' => 'saml20-idp-remote',
  'SingleSignOnService' => 
  array (
    0 => 
    array (
      'Binding' => 'urn:oasis:names:tc:SAML:2.0:bindings:HTTP-Redirect',
      'Location' => 'https://login.cern.ch/adfs/ls/',
    ),
    1 => 
    array (
      'Binding' => 'urn:oasis:names:tc:SAML:2.0:bindings:HTTP-POST',
      'Location' => 'https://login.cern.ch/adfs/ls/',
    ),
  ),
  'SingleLogoutService' => 
  array (
    0 => 
    array (
      'Binding' => 'urn:oasis:names:tc:SAML:2.0:bindings:HTTP-Redirect',
      'Location' => 'https://login.cern.ch/adfs/ls/',
    ),
    1 => 
    array (
      'Binding' => 'urn:oasis:names:tc:SAML:2.0:bindings:HTTP-POST',
      'Location' => 'https://login.cern.ch/adfs/ls/',
    ),
  ),
  'ArtifactResolutionService' => 
  array (
    0 => 
    array (
      'Binding' => 'urn:oasis:names:tc:SAML:2.0:bindings:SOAP',
      'Location' => 'https://login.cern.ch/adfs/services/trust/artifactresolution',
      'index' => 0,
    ),
  ),
  'NameIDFormats' => 
  array (
    0 => 'urn:oasis:names:tc:SAML:1.1:nameid-format:emailAddress',
    1 => 'urn:oasis:names:tc:SAML:2.0:nameid-format:persistent',
    2 => 'urn:oasis:names:tc:SAML:2.0:nameid-format:transient',
  ),
  'keys' => 
  array (
    0 => 
    array (
      'encryption' => true,
      'signing' => false,
      'type' => 'X509Certificate',
      'X509Certificate' => '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',
    ),
    1 => 
    array (
      'encryption' => false,
      'signing' => true,
      'type' => 'X509Certificate',
      'X509Certificate' => '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',
    ),
  ),
);