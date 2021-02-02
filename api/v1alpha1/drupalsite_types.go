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

// DrupalSiteSpec defines the desired state of DrupalSite
type DrupalSiteSpec struct {
	// Publish defines if the site has to be published or not
	// +kubebuilder:validation:Required
	Publish bool `json:"publish"`

	// DrupalVersion defines the version of the Drupal to install
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	DrupalVersion string `json:"drupalVersion"` // Convert to enum

	// Environment defines the drupal site environments
	Environment EnvironmentSpec `json:"environment"`
}

// EnvironmentSpec defines the environment field in DrupalSite
type EnvironmentSpec struct {
	// Name specifies the environment name for the DrupalSite. The name will be used for resource lables and route name
	// +kubebuilder:validation:Pattern=[a-z0-9]([-a-z0-9]*[a-z0-9])?
	Name string `json:"name"`
	// ExtraConfigRepo passes on the git url with advanced configuration to the DrupalSite S2I functionality
	// +kubebuilder:validation:Pattern=`[(http(s)?):\/\/(www\.)?a-zA-Z0-9@:%._\+~#=]{2,256}\.[a-z]{2,6}\b([-a-zA-Z0-9@:%_\+.~#?&//=]*)`
	ExtraConfigRepo string `json:"extraConfigRepo,omitempty"`
	// ImageOverride overrides the image urls in the DrupalSite deployment for the fields that are set
	ImageOverride ImageOverrideSpec `json:"imageOverride,omitempty"`
}

// ImageSpec defines the image field in EnvironmentSpec
type ImageOverrideSpec struct {
	// Sitebuilder overrides the Sitebuilder image url in the DrupalSite deployment
	// +kubebuilder:validation:Pattern=`[a-z0-9]+(?:[\/._-][a-z0-9]+)*.`
	Sitebuilder string `json:"sitebuilder,omitempty"`
	// Nginx overrides the Nginx image url in the DrupalSite deployment
	// +kubebuilder:validation:Pattern=`[a-z0-9]+(?:[\/._-][a-z0-9]+)*.`
	Nginx string `json:"nginx,omitempty"`
	// PhpFPM overrides the PHP-FPM image url in the DrupalSite deployment
	// +kubebuilder:validation:Pattern=`[a-z0-9]+(?:[\/._-][a-z0-9]+)*.`
	PhpFPM string `json:"phpFpm,omitempty"`
}

// DrupalSiteStatus defines the observed state of DrupalSite
type DrupalSiteStatus struct {
	// Phase aggregates the information from all the conditions and reports on the lifecycle phase of the resource
	// Enum: {Creating,Created,Deleted}
	Phase string `json:"phase,omitempty"`

	// Conditions specifies different conditions based on the DrupalSite status
	// +kubebuilder:validation:type=array
	Conditions status.Conditions `json:"conditions,omitempty"`
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
