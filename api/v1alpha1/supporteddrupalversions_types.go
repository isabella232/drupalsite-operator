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
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// NOTE: json tags are required.  Any new fields you add must have json tags for the fields to be serialized.

// SupportedDrupalVersionsSpec defines the desired state of SupportedDrupalVersions
type SupportedDrupalVersionsSpec struct {
	// Optional list of versions to be ignored in Status
	// +optional
	Blacklist []string `json:"blacklist,omitempty"`
	// +kubebuilder:validation:Required
	DefaultVersion string `json:"defaultVersion"`
}

// SupportedDrupalVersionsStatus defines the observed state of SupportedDrupalVersions
type SupportedDrupalVersionsStatus struct {
	// AvailableVersions will list all the current versions present in the Container Registry
	// and supported by our infrastructure
	// + optional
	AvailableVersions []DrupalVersion `json:"versions,omitempty"`
}

// DrupalVersion represents one available version present on the Container Registry
type DrupalVersion struct {
	// Name of the version
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLenght=2
	Name string `json:"name"`
	// ReleaseSpec contains the details for the current DrupalVersion
	// +kubebuilder:validation:Required
	ReleaseSpec `json:"releaseSpec"`
}

// ReleaseSpec contains the details of a release
type ReleaseSpec struct {
	// LatestReleaseSpec will contain the latest version of a specific DrupalVersion
	// +kubebuilder:validation:Required
	LatestReleaseSpec string `json:"latest"`
}

//+kubebuilder:object:root=true
//+kubebuilder:subresource:status
//+kubebuilder:resource:scope=Cluster

// SupportedDrupalVersions is the Schema for the supporteddrupalversions API
type SupportedDrupalVersions struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   SupportedDrupalVersionsSpec   `json:"spec,omitempty"`
	Status SupportedDrupalVersionsStatus `json:"status,omitempty"`
}

//+kubebuilder:object:root=true

// SupportedDrupalVersionsList contains a list of SupportedDrupalVersions
type SupportedDrupalVersionsList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []SupportedDrupalVersions `json:"items"`
}

func init() {
	SchemeBuilder.Register(&SupportedDrupalVersions{}, &SupportedDrupalVersionsList{})
}
