/*


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

// DrupalSiteRequestSpec defines the desired state of DrupalSiteRequest
type DrupalSiteRequestSpec struct {
	// INSERT ADDITIONAL SPEC FIELDS - desired state of cluster
	// Important: Run "make" to regenerate code after modifying this file

	// Foo is an example field of DrupalSiteRequest. Edit DrupalSiteRequest_types.go to remove/update

	// ApplicationName is used to construct the Application's "display name" following a naming convention:
	// `Web frameworks site <applicationName> (<instance>)` (see also https://gitlab.cern.ch/paas-tools/operators/authz-operator/issues/5 ).
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	ApplicationName string `json:"applicationName" valid:"matches(^[0-9a-z_\\-]+$),length(1|100)"`

	// Publish defines if the site has to be published or not
	// +kubebuilder:validation:Required
	Publish bool `json:"publish"`

	// DrupalVerion defines the version of the Drupal to install
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	DrupalVersion string `json:"drupalVersion"` // Convert to enum

	// DisplayName is the user-facing name of the ApplicationRegistration as stored in the Authzsvc API
	// +optional
	DisplayName string `json:"displayName,omitempty"`
}

// DrupalSiteRequestStatus defines the observed state of DrupalSiteRequest
type DrupalSiteRequestStatus struct {
	// INSERT ADDITIONAL STATUS FIELD - define observed state of cluster
	// Important: Run "make" to regenerate code after modifying this file
	// TODO: ensure empty conditions

	// Created defines if the site has to been created or not
	// +kubebuilder:validation:Required
	Created bool `json:"created"`
	// ProvisioningStatus shows whether:
	// - Created: the ApplicationRegistration has been successfully provisioned
	// - Creating: provisioning is still in progress (including any potentially transient errors
	//   from the Authorization API where reconciliation will be reattempted, like
	//   the API not responding or 5xx HTTP errors)
	// - ProvisioningError: provisioning has failed and won't be retried; look at ErrorMessage for details.
	//   It is set for permanent errors, such as name conflicts, invalid initialOwner, 4xx HTTP errors.
	// +kubebuilder:validation:Enum="Created";"Creating";"ProvisioningError";"DeletedFromAPI"
	// ProvisioningStatus string `json:"provisioningStatus,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status

// DrupalSiteRequest is the Schema for the drupalsiterequests API
type DrupalSiteRequest struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   DrupalSiteRequestSpec   `json:"spec,omitempty"`
	Status DrupalSiteRequestStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// DrupalSiteRequestList contains a list of DrupalSiteRequest
type DrupalSiteRequestList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []DrupalSiteRequest `json:"items"`
}

func init() {
	SchemeBuilder.Register(&DrupalSiteRequest{}, &DrupalSiteRequestList{})
}

// DisplayNameConvention implements the naming convention {ApplicationName -> DisplayName}
func (a DrupalSiteRequest) DisplayNameConvention(clusterInstanceName string) string {
	return "Web frameworks site " + a.Spec.ApplicationName + " (" + clusterInstanceName + ")"
}
