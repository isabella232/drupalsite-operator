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
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// EDIT THIS FILE!  THIS IS SCAFFOLDING FOR YOU TO OWN!
// NOTE: json tags are required.  Any new fields you add must have json tags for the fields to be serialized.

// DBODRegistrationSpec defines the desired state of DBODRegistration
type DBODRegistrationSpec struct {
	DbodClass          string            `json:"dbodClass,omitempty"`
	DbodInstanceName   string            `json:"dbodInstanceName,omitempty"`
	DbName             string            `json:"dbName"`
	DbUser             string            `json:"dbUser"`
	RegistrationLabels map[string]string `json:"registrationLabels,omitempty"`
}

// DBODRegistrationStatus defines the observed state of DBODRegistration
type DBODRegistrationStatus struct {
	DbodInstance        string `json:"dbodInstance,omitempty"`
	DbCredentialsSecret string `json:"dbCredentialsSecret,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status

// DBODRegistration is the Schema for the dbodregistrations API
type DBODRegistration struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   DBODRegistrationSpec   `json:"spec,omitempty"`
	Status DBODRegistrationStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// DBODRegistrationList contains a list of DBODRegistration
type DBODRegistrationList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []DBODRegistration `json:"items"`
}

func init() {
	SchemeBuilder.Register(&DBODRegistration{}, &DBODRegistrationList{})
}
