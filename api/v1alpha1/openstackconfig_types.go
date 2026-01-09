/*
Copyright 2026.

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

// OpenstackConfigSpec defines the desired state of OpenstackConfig
type OpenstackConfigSpec struct {
	// subnetID is the OpenStack subnet ID to target. If set, subnetName is ignored.
	// subnetID가 우선이며, 없으면 subnetName을 사용한다.
	// +optional
	SubnetID string `json:"subnetID,omitempty"`

	// subnetName is the OpenStack subnet name to target when subnetID is empty.
	// 서브넷명이 중복되면 오류가 발생할 수 있으므로 subnetID 사용을 권장한다.
	// +optional
	SubnetName string `json:"subnetName,omitempty"`

	// vmNames is the list of OpenStack VM names to configure.
	// +kubebuilder:validation:MinItems=1
	VmNames []string `json:"vmNames"`

	// credentials contains provider and project identifiers.
	Credentials OpenstackCredentials `json:"credentials"`
}

// OpenstackCredentials defines the identifiers needed to resolve OpenStack access.
type OpenstackCredentials struct {
	// openstackProviderID is the provider ID used by Contrabass API.
	// +kubebuilder:validation:MinLength=1
	OpenstackProviderID string `json:"openstackProviderID"`

	// k8sProviderID is optional and used for downstream cluster routing.
	// +optional
	K8sProviderID string `json:"k8sProviderID,omitempty"`

	// projectID is the OpenStack project (tenant) ID.
	// +kubebuilder:validation:MinLength=1
	ProjectID string `json:"projectID"`
}

// OpenstackConfigStatus defines the observed state of OpenstackConfig.
type OpenstackConfigStatus struct {
	// INSERT ADDITIONAL STATUS FIELD - define observed state of cluster
	// Important: Run "make" to regenerate code after modifying this file

	// For Kubernetes API conventions, see:
	// https://github.com/kubernetes/community/blob/master/contributors/devel/sig-architecture/api-conventions.md#typical-status-properties

	// conditions represent the current state of the OpenstackConfig resource.
	// Each condition has a unique type and reflects the status of a specific aspect of the resource.
	//
	// Standard condition types include:
	// - "Available": the resource is fully functional
	// - "Progressing": the resource is being created or updated
	// - "Degraded": the resource failed to reach or maintain its desired state
	//
	// The status of each condition is one of True, False, or Unknown.
	// +listType=map
	// +listMapKey=type
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`

	// lastSyncedAt is the last time the operator successfully synced data.
	// +optional
	LastSyncedAt *metav1.Time `json:"lastSyncedAt,omitempty"`

	// lastError records the latest error message if the reconcile failed.
	// +optional
	LastError string `json:"lastError,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status

// OpenstackConfig is the Schema for the openstackconfigs API
type OpenstackConfig struct {
	metav1.TypeMeta `json:",inline"`

	// metadata is a standard object metadata
	// +optional
	metav1.ObjectMeta `json:"metadata,omitzero"`

	// spec defines the desired state of OpenstackConfig
	// +required
	Spec OpenstackConfigSpec `json:"spec"`

	// status defines the observed state of OpenstackConfig
	// +optional
	Status OpenstackConfigStatus `json:"status,omitzero"`
}

// +kubebuilder:object:root=true

// OpenstackConfigList contains a list of OpenstackConfig
type OpenstackConfigList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitzero"`
	Items           []OpenstackConfig `json:"items"`
}

func init() {
	SchemeBuilder.Register(&OpenstackConfig{}, &OpenstackConfigList{})
}
