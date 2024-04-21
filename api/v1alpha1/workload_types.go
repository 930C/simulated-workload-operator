/*
Copyright 2024.

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
	"k8s.io/apimachinery/pkg/runtime"
)

// EDIT THIS FILE!  THIS IS SCAFFOLDING FOR YOU TO OWN!
// NOTE: json tags are required.  Any new fields you add must have json tags for the fields to be serialized.

// WorkloadSpec defines the desired state of Workload
type WorkloadSpec struct {
	// INSERT ADDITIONAL SPEC FIELDS - desired state of cluster
	// Important: Run "make" to regenerate code after modifying this file

	// Type of simulation to perform: CPU, Memory, IO, Sleep
	SimulationType string `json:"simulationType,omitempty"`

	// Duration for the simulation or sleep (in seconds)
	Duration int `json:"duration,omitempty"`

	// Intensity of the workload for CPU or Memory or IO
	// This could be percentage for CPU, size in MB for Memory, and IO speed in MB/s
	Intensity int `json:"intensity,omitempty"`

	// Additional parameters might be needed depending on the simulation type
	Parameters map[string]string `json:"parameters,omitempty"`

	// List of dependent resources this controller should manage
	DependentResources []DependentResourceSpec `json:"dependentResources,omitempty"`
}

// WorkloadStatus defines the observed state of Workload
type WorkloadStatus struct {
	// Reflects the current state of the simulation
	Phase string `json:"phase,omitempty"`

	// Detailed message about the current status of the workload simulation
	Message string `json:"message,omitempty"`

	// StartTime is the timestamp representing the server time when this Workload started being reconciled by the operator
	StartTime string `json:"startTime,omitempty"`
	// EndTime is the timestamp representing the server time when this Workload ended being reconciled by the operator
	EndTime string `json:"endTime,omitempty"`

	// Conditions represent the latest available observations of an object's state
	Conditions []metav1.Condition `json:"conditions,omitempty" patchStrategy:"merge" patchMergeKey:"type" protobuf:"bytes,1,rep,name=conditions"`
}

//+kubebuilder:object:root=true
//+kubebuilder:subresource:status

// Workload is the Schema for the workloads API
type Workload struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   WorkloadSpec   `json:"spec,omitempty"`
	Status WorkloadStatus `json:"status,omitempty"`
}

//+kubebuilder:object:root=true

// WorkloadList contains a list of Workload
type WorkloadList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []Workload `json:"items"`
}

func init() {
	SchemeBuilder.Register(&Workload{}, &WorkloadList{})
}

type DependentResourceSpec struct {
	// Type of the Kubernetes resource (e.g., Pod, ConfigMap)
	ResourceType string `json:"resourceType"`

	// Quantity of the resources to create
	Quantity int `json:"quantity"`

	// Template of the resource if applicable (e.g., for creating specific types of Pods or ConfigMaps)
	Template runtime.RawExtension `json:"template,omitempty"`
}
