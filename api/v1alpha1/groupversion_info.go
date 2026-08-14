// Package v1alpha1 contains the MagnumDeployment API for the Yaook Magnum operator.
//
// The group follows the Yaook sub-group convention (infra.yaook.cloud,
// compute.yaook.cloud, network.yaook.cloud, ...) so that Magnum slots in
// alongside the upstream operators without colliding with their CRDs.
//
// +kubebuilder:object:generate=true
// +groupName=magnum.yaook.cloud
package v1alpha1

import (
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/scheme"
)

var (
	// GroupVersion is the group/version used to register these objects.
	GroupVersion = schema.GroupVersion{Group: "magnum.yaook.cloud", Version: "v1alpha1"}

	// SchemeBuilder registers the Go types with a scheme.
	SchemeBuilder = &scheme.Builder{GroupVersion: GroupVersion}

	// AddToScheme adds the types in this group-version to a scheme.
	AddToScheme = SchemeBuilder.AddToScheme
)
