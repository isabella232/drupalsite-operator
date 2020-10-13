<?php
$config = array(
    // This is a authentication source which handles admin authentication.
    'admin' => array(
        // The default is to use core:AdminPassword, but it can be replaced with
        // any authentication source.

        'core:AdminPassword',
    ),
    
    'cern-sp' => array (
        'saml:SP',
        'base64attributes' => true,
        'attributeencodings' => 'base64',
        'entityID' => getenv('HOSTNAME'),
        'idp' => 'https://cern.ch/login'  ,
        'discoURL' => NULL,
        'privatekey' => '/tmp/simplesamlcert/saml.pem',
        'certificate' => '/tmp/simplesamlcert/saml.crt',
        'sign.logout' => true,
        'signature.algorithm' => 'http://www.w3.org/2001/04/xmldsig-more#rsa-sha256'  ,
    ),
);
