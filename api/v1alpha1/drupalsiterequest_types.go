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
	"github.com/operator-framework/operator-lib/status"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// EDIT THIS FILE!  THIS IS SCAFFOLDING FOR YOU TO OWN!
// NOTE: json tags are required.  Any new fields you add must have json tags for the fields to be serialized.

// DrupalSiteSpec defines the desired state of DrupalSite
type DrupalSiteSpec struct {
	// INSERT ADDITIONAL SPEC FIELDS - desired state of cluster
	// Important: Run "make" to regenerate code after modifying this file

	// Foo is an example field of DrupalSite. Edit DrupalSite_types.go to remove/update

	// Publish defines if the site has to be published or not
	// +kubebuilder:validation:Required
	Publish bool `json:"publish"`

	// DrupalVerion defines the version of the Drupal to install
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	DrupalVersion string `json:"drupalVersion"` // Convert to enum
}

// DrupalSiteStatus defines the observed state of DrupalSite
type DrupalSiteStatus struct {
	// Phase aggregates the information from all the conditions and reports on the lifecycle phase of the resource
	// Enum: {Creating,Created,Deleted}
	Phase string `json:"phase,omitempty"`

	// TODO conditions
	// +kubebuilder:validation:type=array
	Conditions status.Conditions `json:"conditions,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status

// DrupalSite is the Schema for the drupalsiterequests API
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
