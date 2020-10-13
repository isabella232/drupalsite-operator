#!/bin/sh
set -x

# Check if database connection exists
mysql -h $DB_HOST -P $DB_PORT -u $DB_USER -p$DB_PASSWORD $DB_NAME -e "select 1" 2>&1 1>/dev/null
if [ $? -ne 0 ]; then
  exit 1;
fi

if drush status --fields=bootstrap | grep -q 'Successful'; then

    echo "--> Cleaning caches..."
    drush -y cr

    echo "--> Updating database..."
    drush -y updb

    echo "--> Importing configuration..."
    drush -y cim
fi

echo "--> Running..."