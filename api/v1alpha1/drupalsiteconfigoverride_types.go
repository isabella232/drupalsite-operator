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
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// DrupalSiteConfigOverrideSpec defines the desired state of DrupalSiteConfigOverride
type DrupalSiteConfigOverrideSpec struct {
	// Php includes configuration for the PHP container of the DrupalSite server pods
	Php Resources `json:"php,omitempty"`
	// Nginx includes configuration for the Nginx container of the DrupalSite server pods
	Nginx Resources `json:"nginx,omitempty"`
	// Webdav includes configuration for the Webdav container of the DrupalSite server pods
	Webdav Resources `json:"webdav,omitempty"`
	// PhpExporter includes configuration for the PhpExporter container of the DrupalSite server pods
	PhpExporter Resources `json:"phpexporter,omitempty"`
	// DrupalLogs includes configuration for the DrupalLogs container of the DrupalSite server pods
	DrupalLogs Resources `json:"drupallogs,omitempty"`
}

type Resources struct {
	Resources corev1.ResourceRequirements `json:"resources,omitempty"`
}

// DrupalSiteConfigOverrideStatus defines the observed state of DrupalSiteConfigOverride
type DrupalSiteConfigOverrideStatus struct {
}

//+kubebuilder:object:root=true
//+kubebuilder:subresource:status

// DrupalSiteConfigOverride is the Schema for the drupalsiteconfigoverrides API
type DrupalSiteConfigOverride struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   DrupalSiteConfigOverrideSpec   `json:"spec,omitempty"`
	Status DrupalSiteConfigOverrideStatus `json:"status,omitempty"`
}

//+kubebuilder:object:root=true

// DrupalSiteConfigOverrideList contains a list of DrupalSiteConfigOverride
type DrupalSiteConfigOverrideList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []DrupalSiteConfigOverride `json:"items"`
}

func init() {
	SchemeBuilder.Register(&DrupalSiteConfigOverride{}, &DrupalSiteConfigOverrideList{})
}
