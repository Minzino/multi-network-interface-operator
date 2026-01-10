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

	// vmNames is the list of OpenStack VM IDs to configure.
	// +kubebuilder:validation:MinItems=1
	VmNames []string `json:"vmNames"`

	// credentials contains provider and project identifiers.
	Credentials OpenstackCredentials `json:"credentials"`

	// settings overrides operator-level defaults for this CR.
	// +optional
	Settings *OpenstackConfigSettings `json:"settings,omitempty"`

	// secrets references sensitive values required by this CR.
	// +optional
	Secrets *OpenstackConfigSecrets `json:"secrets,omitempty"`
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

// OpenstackConfigSettings defines per-CR settings.
type OpenstackConfigSettings struct {
	// contrabassEndpoint is the base URL for Contrabass API.
	// +optional
	ContrabassEndpoint string `json:"contrabassEndpoint,omitempty"`

	// contrabassEncryptKey is used for decrypting adminPw.
	// NOTE: SecretRef 사용을 권장한다.
	// +optional
	ContrabassEncryptKey string `json:"contrabassEncryptKey,omitempty"`

	// contrabassTimeout is the HTTP timeout (e.g. 30s).
	// +optional
	ContrabassTimeout string `json:"contrabassTimeout,omitempty"`

	// contrabassInsecureTLS allows insecure TLS.
	// +optional
	ContrabassInsecureTLS *bool `json:"contrabassInsecureTLS,omitempty"`

	// violaEndpoint is the base URL for Viola API.
	// +optional
	ViolaEndpoint string `json:"violaEndpoint,omitempty"`

	// violaTimeout is the HTTP timeout (e.g. 30s).
	// +optional
	ViolaTimeout string `json:"violaTimeout,omitempty"`

	// violaInsecureTLS allows insecure TLS.
	// +optional
	ViolaInsecureTLS *bool `json:"violaInsecureTLS,omitempty"`

	// openstackTimeout is the HTTP timeout (e.g. 30s).
	// +optional
	OpenstackTimeout string `json:"openstackTimeout,omitempty"`

	// openstackInsecureTLS allows insecure TLS.
	// +optional
	OpenstackInsecureTLS *bool `json:"openstackInsecureTLS,omitempty"`

	// openstackNeutronEndpoint overrides neutron endpoint.
	// +optional
	OpenstackNeutronEndpoint string `json:"openstackNeutronEndpoint,omitempty"`

	// openstackNovaEndpoint overrides nova endpoint.
	// +optional
	OpenstackNovaEndpoint string `json:"openstackNovaEndpoint,omitempty"`

	// openstackEndpointInterface selects endpoint interface (public/internal/admin).
	// +optional
	OpenstackEndpointInterface string `json:"openstackEndpointInterface,omitempty"`

	// openstackEndpointRegion selects endpoint region.
	// +optional
	OpenstackEndpointRegion string `json:"openstackEndpointRegion,omitempty"`

	// openstackNodeNameMetadataKey overrides nodeName mapping metadata key.
	// +optional
	OpenstackNodeNameMetadataKey string `json:"openstackNodeNameMetadataKey,omitempty"`

	// openstackPortAllowedStatuses filters port statuses (e.g. ACTIVE, DOWN).
	// +optional
	OpenstackPortAllowedStatuses []string `json:"openstackPortAllowedStatuses,omitempty"`

	// downPortFastRetryMax controls fast retry count for DOWN ports.
	// +optional
	DownPortFastRetryMax *int32 `json:"downPortFastRetryMax,omitempty"`

	// pollFastInterval is the fast polling interval.
	// +optional
	PollFastInterval string `json:"pollFastInterval,omitempty"`

	// pollSlowInterval is the slow polling interval.
	// +optional
	PollSlowInterval string `json:"pollSlowInterval,omitempty"`

	// pollErrorInterval is the retry interval on error.
	// +optional
	PollErrorInterval string `json:"pollErrorInterval,omitempty"`

	// pollFastWindow is the fast polling window after changes.
	// +optional
	PollFastWindow string `json:"pollFastWindow,omitempty"`
}

// SecretKeyRef defines a secret reference.
type SecretKeyRef struct {
	// name is the Secret name.
	// +kubebuilder:validation:MinLength=1
	Name string `json:"name"`

	// key is the Secret data key.
	// +kubebuilder:validation:MinLength=1
	Key string `json:"key"`
}

// OpenstackConfigSecrets defines secret references for this CR.
type OpenstackConfigSecrets struct {
	// contrabassEncryptKeySecretRef provides encrypt key via Secret.
	// +optional
	ContrabassEncryptKeySecretRef *SecretKeyRef `json:"contrabassEncryptKeySecretRef,omitempty"`
}

// DownPortRetryStatus는 DOWN 포트 재전송 상태를 저장한다.
type DownPortRetryStatus struct {
	// hash는 DOWN 포트 목록(정렬된 포트 ID)의 해시값이다.
	// +optional
	Hash string `json:"hash,omitempty"`

	// lastAttempt는 마지막 전송 시각이다.
	// +optional
	LastAttempt *metav1.Time `json:"lastAttempt,omitempty"`

	// fastAttempts는 빠른 재시도 구간에서 누적된 시도 횟수이다.
	// +optional
	FastAttempts int32 `json:"fastAttempts,omitempty"`
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

	// downPortRetry는 DOWN 포트 재전송 상태를 기록한다.
	// +optional
	DownPortRetry *DownPortRetryStatus `json:"downPortRetry,omitempty"`
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
