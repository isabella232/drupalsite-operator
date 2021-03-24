/*
Copyright 2021.

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
	QoSStandard  QoSClass  = "standard"
	DBODStandard DBODClass = "standard"
	DBODSSD      DBODClass = "ssd"
)

// DrupalSiteSpec defines the desired state of DrupalSite
type DrupalSiteSpec struct {
	// Publish defines if the site has to be published or not
	// +kubebuilder:validation:Required
	Publish bool `json:"publish"`

	// SiteURL is the URL where the site should be made available.
	// Defaults to <envName>-<projectname>.<defaultDomain>, where <defaultDomain> is configured per cluster (typically `web.cern.ch`)
	// +kubebuilder:validation:Pattern=`[(http(s)?):\/\/(www\.)?a-zA-Z0-9@:%._\+~#=]{2,256}\.[a-z]{2,6}\b([-a-zA-Z0-9@:%_\+.~#?&//=]*)`
	// +optional
	SiteURL string `json:"siteUrl,omitempty"`

	// DrupalVersion defines the version of the Drupal to install
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	DrupalVersion string `json:"drupalVersion"` // Convert to enum

	// Environment defines the drupal site environments
	// +kubebuilder:validation:Required
	Environment `json:"environment"`
}

// Environment defines the environment field in DrupalSite
type Environment struct {
	// Name specifies the environment name for the DrupalSite. The name will be used for resource lables and route name
	// +kubebuilder:validation:Pattern=[a-z0-9]([-a-z0-9]*[a-z0-9])?
	// +kubebuilder:validation:Required
	Name string `json:"name"`
	// ExtraConfigRepo passes on the git url with advanced configuration to the DrupalSite S2I functionality
	// TODO: support branches https://gitlab.cern.ch/drupal/paas/drupalsite-operator/-/issues/28
	// +kubebuilder:validation:Pattern=`[(http(s)?):\/\/(www\.)?a-zA-Z0-9@:%._\+~#=]{2,256}\.[a-z]{2,6}\b([-a-zA-Z0-9@:%_\+.~#?&//=]*)`
	// +optional
	ExtraConfigRepo string `json:"extraConfigRepo,omitempty"`
	// ImageOverride overrides the image urls in the DrupalSite deployment for the fields that are set
	// +optional
	ImageOverride `json:"imageOverride,omitempty"`
	// QoSClass specifies the website's performance and availability requirements
	// +kubebuilder:validation:Enum:=critical;eco;standard
	// +kubebuilder:validation:Required
	QoSClass `json:"qosClass"`
	// DBODClass requests a specific kind of DBOD resources for the website. If omitted, it is derived from QoSClass.
	// +optional
	DBODClass `json:"dbodClass,omitempty"`
}

// QoSClass specifies the website's performance and availability requirements
type QoSClass string

// DBODClass requests a specific kind of DBOD resources for the website. If omitted, it is derived from QoSClass.
type DBODClass string

// ImageOverride lets the website admin bypass the operator's buildconfigs and inject custom images.
// Envisioned primarily for the sitebuilder, this could allow an advanced developer to deploy their own
// custom version of Drupal or different PHP versions.
type ImageOverride struct {
	// Sitebuilder overrides the Sitebuilder image url in the DrupalSite deployment
	// +kubebuilder:validation:Pattern=`[a-z0-9]+(?:[\/._-][a-z0-9]+)*.`
	// +optional
	Sitebuilder string `json:"siteBuilder,omitempty"`
	// Note: Overrides for the nginx and php images might be added if needed
}

// DrupalSiteStatus defines the observed state of DrupalSite
type DrupalSiteStatus struct {
	// Conditions specifies different conditions based on the DrupalSite status
	// +kubebuilder:validation:type=array
	// +optional
	Conditions status.Conditions `json:"conditions,omitempty"`

	// PreviousDrupalVersion stores the `drupalVersion` string during the upgrade process to alow erollback operations
	// +optional
	// +kubebuilder:validation:MinLength=1
	PreviousDrupalVersion string `json:"previousDrupalVersion,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status

// DrupalSite is the Schema for the drupalsites API
type DrupalSite struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   DrupalSiteSpec   `json:"spec,omitempty"`
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

func (drp DrupalSite) ConditionTrue(condition status.ConditionType) (update bool) {
	init := drp.Status.Conditions.GetCondition(condition)
	return init != nil && init.Status == v1.ConditionTrue
}

func (drp DrupalSite) ErrorTrue(condition status.ConditionType) (update bool) {
	init := drp.Status.Conditions.GetCondition(condition)
	return init != nil && init.Reason == "Error"
}
