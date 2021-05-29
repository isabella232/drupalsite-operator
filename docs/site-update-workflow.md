# Workflow for updating a DrupalSite

## To update a site version

1. Modify the `DrupalSite` CR spec `DrupalVersion` field to the desired version
    -  A difference in the `DrupalVersion` of the CR spec and the `drupalVersion` annotation on the running pod will trigger the update workflow in the operator
2. Upon the start of the update workflow, the operator adds an annotation `updateInProgress: true` on the CR to notify users about the update process
3. The operator then rolls out a new deployment with the new version
4. Once the new pod is running, operator checks if any update to the DB schema is required. If there are any, a status field `DBUpdatesPending` will be set to true on the CR and the update process on the DB schema is initiated

### Successful update

1. If the version provided is correct and if there aren't any errors in the process, the `updateInProgress` annotation and the `DBUpdatesPending` status field will be removed
2. The status field `FailsafeDrupalVersion` will be updated with the new version set in the `DrupalVersion` field of the spec

### Failed update

1. If there is an error and if the update process fails, the `updateInProgress` annotation will be removed and a new status field either `CodeUpdateFailed` or `DBUpdatesFailed` will be set accordingly, with the error message in `Reason` sub-field
2. `DBUpdatesPending` status field will still be intact, if the update failed during the 'DB scheme update' stage
3. The `FailsafeDrupalVersion` field in the status indicates the previously running version

## Recovering from a failed update
1. To recover from a failed update, the `DrupalVersion` field in the CR spec should be updated to the value of the `FailsafeDrupalVersion` field on the CR status
2. This will, restore the status fields (`DBUpdatesFailed` or `CodeUpdateFailed`) set on the CR and will allow the users to trigger a new update if needed
