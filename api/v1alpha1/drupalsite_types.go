/*
Copyright 2021 CERN.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package v1alpha1

import (
	"github.com/operator-framework/operator-lib/status"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (
	QoSStandard  QoSClass      = "standard"
	QoSCritical  QoSClass      = "critical"
	QoSTest      QoSClass      = "test"
	DBODStandard DatabaseClass = "standard"
	DBODCritical DatabaseClass = "critical"
	DBODSSD      DatabaseClass = "ssd"
)

// DrupalSiteSpec defines the desired state of DrupalSite
type DrupalSiteSpec struct {
	// SiteURL is the URL where the site should be made available.
	// Recommended to set `<environmentName>-<projectname>.web.cern.ch`
	// or `<projectname>.web.cern.ch` if this is the "live" site
	// +kubebuilder:validation:Required
	SiteURL []Url `json:"siteUrl"`

	// Version refers to the version and release of the CERN Drupal Distribution that will be deployed to serve this website.
	// Changing this value triggers the website's update process.
	// +kubebuilder:validation:Required
	Version `json:"version"`

	// Configuration of the DrupalSite for specific needs. A typical default value is given for every setting, so usually these won't need to change.
	// +kubebuilder:default={"databaseClass":"standard","qosClass":"standard","diskSize":"2000Mi"}
	// +optional
	Configuration `json:"configuration,omitempty"`
}

// Version refers to the version and release of the CERN Drupal Distribution that will be deployed to serve this website
type Version struct {
	// Name specifies the "version" branch of CERN Drupal Distribution that will be deployed, eg `v8.9-1`
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	Name string `json:"name"`
	// ReleaseSpec is the concrete release of the specified version,
	// typically of the format `RELEASE.<timestamp>`.
	// CERN Drupal image tags take the form `<version.name>-<version.releaseSpec>`,
	// for example `v8.9-1-RELEASE.2021.05.25T16-00-33Z`
	// +optional
	ReleaseSpec string `json:"releaseSpec"`
}

// Configuration of the DrupalSite for specific needs. A typical default value is given for every setting, so usually these won't need to change.
type Configuration struct {
	// ExtraConfigurationRepo injects the composer project and other supported configuration from the given git repo to the site,
	// by building an image specific to this site from the generic CERN one.
	// Add extra modules to your website with Composer through a Git repo, following these docs
	// +kubebuilder:validation:Pattern=`[(http(s)?):\/\/(www\.)?a-zA-Z0-9@:%._\+~#=]{2,256}\.[a-z]{2,6}\b([-a-zA-Z0-9@:%_\+.~#?&//=]*)`
	// +optional
	ExtraConfigurationRepo string `json:"extraConfigurationRepo,omitempty"`
	// TODO: support branches https://gitlab.cern.ch/drupal/paas/drupalsite-operator/-/issues/28

	// QoSClass specifies the website's performance and availability requirements.  The default value is "standard".
	// +kubebuilder:validation:Enum:=critical;eco;standard
	// +kubebuilder:default=standard
	// +optional
	QoSClass `json:"qosClass,omitempty"`

	// DatabaseClass specifies the kind of database that the website needs, among those supported by the cluster. The default value is "standard".
	// +kubebuilder:validation:Enum:=critical;ssd;standard
	// +kubebuilder:default=standard
	// +optional
	DatabaseClass `json:"databaseClass,omitempty"`

	// CloneFrom initializes this environment by cloning the specified DrupalSite (usually the "live" site),
	// instead of installing an empty CERN-themed website.
	// Immutable.
	// +optional
	CloneFrom `json:"cloneFrom,omitempty"`

	// DiskSize is the max size of the site's files directory. The default value is "2000Mi".
	// When `cloneFrom` is set, if this value is less than the source's, it will be *overwritten*,
	// since the size has to be at least as large as the source.
	// +kubebuilder:default="2000Mi"
	// +optional
	// +kubebuilder:validation:Pattern=`^([+-]?[0-9.]+)([eEinumkKMGTP]*[-+]?[0-9]*)$`
	DiskSize string `json:"diskSize,omitempty"`

	// WebDAVPassword sets the HTTP basic auth password for WebDAV file access.
	// A default is auto-generated if a value isn't given.
	// Changing this field updates the password.
	// +optional
	WebDAVPassword string `json:"webDAVPassword,omitempty"`
}

// QoSClass specifies the website's performance and availability requirements
type QoSClass string

// DatabaseClass specifies the kind of database that the website needs, among those supported by the cluster.
type DatabaseClass string

// CloneFrom specifies the string that the CloneFrom field acts on.
type CloneFrom string

// Url refers to where the site should be made available.
// +kubebuilder:validation:Pattern=`[(http(s)?):\/\/(www\.)?a-zA-Z0-9@:%._\+~#=]{2,256}\.[a-z]{2,6}\b([-a-zA-Z0-9@:%_\+.~#?&//=]*)`
type Url string

// DrupalSiteStatus defines the observed state of DrupalSite
type DrupalSiteStatus struct {
	// Conditions specifies different conditions based on the DrupalSite status
	// +kubebuilder:validation:type=array
	// +optional
	Conditions status.Conditions `json:"conditions,omitempty"`

	// ReleaseID reports the actual release of CERN Drupal Distribution that is being used in the deployment.
	// +optional
	ReleaseID `json:"releaseID,omitempty"`

	// ServingPodImage reports the complete image name of the PHP-FPM container that is being used in the deployment.
	// +optional
	ServingPodImage string `json:"servingPodImage,omitempty"`

	// AvailableBackups lists all the velero 'Backup' objects created for the current DrupalSite
	// +optional
	AvailableBackups []Backup `json:"availableBackups,omitempty"`

	// ExpectedDeploymentReplicas specifies the deployment replicas for the current DrupalSite
	// +optional
	ExpectedDeploymentReplicas *int32 `json:"expectedDeploymentReplicas,omitempty"`

	// GitlabWebhookURL is the URL that triggers a new build of the site's image after changes on its source Gitlab "extraConfigurationRepo".
	// It should be copied to Gitlab.
	// +optional
	GitlabWebhookURL string `json:"gitlabWebhookURL,omitempty"`
}

// ReleaseID reports the actual release of CERN Drupal Distribution that is being used in the deployment.
type ReleaseID struct {
	// Current releaseID is the image tag that is in use by the site's deployment now
	// +optional
	// +kubebuilder:validation:MinLength=1
	Current string `json:"current,omitempty"`
	// Failsafe releaseID stores the image tag during the upgrade process to allow rollback operations
	// +optional
	// +kubebuilder:validation:MinLength=1
	Failsafe string `json:"failsafe,omitempty"`
}

// Backup item represents information of a single velero 'Backup' object
type Backup struct {
	// BackupName represents the name of a given velero 'Backup' resource
	// +optional
	BackupName string `json:"backupName,omitempty"`

	// Date represents the created date of a given velero 'Backup' resource
	// +optional
	Date *metav1.Time `json:"date,omitempty"`

	// Expires represents the expiry date of a given velero 'Backup' resource
	// +optional
	Expires *metav1.Time `json:"expires,omitempty"`

	// DrupalSiteName represents the name of the drupalSite for the given velero 'Backup' resource
	// +optional
	DrupalSiteName string `json:"drupalSiteName,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status

// DrupalSite is a website that deploys the CERN Drupal Distribution
type DrupalSite struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   DrupalSiteSpec   `json:"spec"`
	Status DrupalSiteStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// DrupalSiteList contains a list of DrupalSite
type DrupalSiteList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []DrupalSite `json:"items"`
}

func init() {
	SchemeBuilder.Register(&DrupalSite{}, &DrupalSiteList{})
}

// ConditionTrue reports if the condition is true
func (drp DrupalSite) ConditionTrue(condition status.ConditionType) (update bool) {
	init := drp.Status.Conditions.GetCondition(condition)
	return init != nil && init.Status == v1.ConditionTrue
}

// ConditionFalse reports if the condition is false
func (drp DrupalSite) ConditionFalse(condition status.ConditionType) (update bool) {
	init := drp.Status.Conditions.GetCondition(condition)
	return init != nil && init.Status == v1.ConditionFalse
}

// ConditionReasonSet reports if the condition Reason is not empty
func (drp DrupalSite) ConditionReasonSet(condition status.ConditionType) (update bool) {
	init := drp.Status.Conditions.GetCondition(condition)
	return init != nil && init.Reason != ""
}
