package v1alpha1

import (
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// LocalObjectRef references another object in the same namespace by name.
type LocalObjectRef struct {
	// +kubebuilder:validation:MinLength=1
	Name string `json:"name"`
}

// RegionSpec identifies the OpenStack region the service is registered in.
type RegionSpec struct {
	// +kubebuilder:validation:MinLength=1
	Name string `json:"name"`
}

// IngressSpec mirrors the ingress block used by the upstream Yaook operators.
type IngressSpec struct {
	// FQDN is the outer fully-qualified domain name of the Ingress.
	// +kubebuilder:validation:MinLength=1
	FQDN string `json:"fqdn"`

	// Port is the port the Ingress is reachable on. It is required to build the
	// public KeystoneEndpoint URL, which may differ from the container port.
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Maximum=65535
	Port int32 `json:"port"`

	// IngressClassName selects the ingress controller.
	// +kubebuilder:default=nginx
	// +optional
	IngressClassName string `json:"ingressClassName,omitempty"`

	// CreateIngress controls whether the Ingress object is created at all.
	// +kubebuilder:default=true
	// +optional
	CreateIngress *bool `json:"createIngress,omitempty"`

	// ExternalCertificateSecretRef points at an existing TLS secret (for example
	// one issued by cert-manager from a public ACME issuer). When unset the
	// Ingress is served with the certificate issued by IssuerRef.
	// +optional
	ExternalCertificateSecretRef *LocalObjectRef `json:"externalCertificateSecretRef,omitempty"`
}

// DatabaseSpec configures the MariaDB instance provisioned via the Yaook
// infra-operator (infra.yaook.cloud/v1 MySQLService).
type DatabaseSpec struct {
	// +kubebuilder:default=1
	// +kubebuilder:validation:Minimum=1
	// +optional
	Replicas int32 `json:"replicas,omitempty"`

	// +kubebuilder:validation:MinLength=1
	StorageClassName string `json:"storageClassName"`

	// +kubebuilder:default="2Gi"
	// +optional
	StorageSize resource.Quantity `json:"storageSize,omitempty"`

	// ProxyReplicas sets the number of MySQL proxy replicas.
	// +kubebuilder:default=1
	// +kubebuilder:validation:Minimum=1
	// +optional
	ProxyReplicas int32 `json:"proxyReplicas,omitempty"`

	// TargetRelease is the MariaDB release provisioned by the infra-operator.
	// +kubebuilder:default="11.4"
	// +optional
	TargetRelease string `json:"targetRelease,omitempty"`

	// BackupSchedule is a crontab expression for database backups.
	// +kubebuilder:default="0 * * * *"
	// +optional
	BackupSchedule string `json:"backupSchedule,omitempty"`
}

// MessageQueueSpec configures the RabbitMQ instance provisioned via the Yaook
// infra-operator (infra.yaook.cloud/v1 AMQPServer).
type MessageQueueSpec struct {
	// +kubebuilder:default=1
	// +kubebuilder:validation:Minimum=1
	// +optional
	Replicas int32 `json:"replicas,omitempty"`

	// +kubebuilder:validation:MinLength=1
	StorageClassName string `json:"storageClassName"`

	// +kubebuilder:default="2Gi"
	// +optional
	StorageSize resource.Quantity `json:"storageSize,omitempty"`

	// TargetRelease is the RabbitMQ release provisioned by the infra-operator.
	// +kubebuilder:default="4.2"
	// +optional
	TargetRelease string `json:"targetRelease,omitempty"`
}

// APISpec configures the magnum-api deployment.
type APISpec struct {
	// +kubebuilder:default=1
	// +kubebuilder:validation:Minimum=1
	// +optional
	Replicas int32 `json:"replicas,omitempty"`

	Ingress IngressSpec `json:"ingress"`

	// +optional
	Resources corev1.ResourceRequirements `json:"resources,omitempty"`
}

// ConductorSpec configures the magnum-conductor deployment.
type ConductorSpec struct {
	// +kubebuilder:default=1
	// +kubebuilder:validation:Minimum=1
	// +optional
	Replicas int32 `json:"replicas,omitempty"`

	// +optional
	Resources corev1.ResourceRequirements `json:"resources,omitempty"`
}

// ImagesSpec allows overriding the container images used for Magnum.
//
// Yaook's own registry does not publish Magnum images, so the defaults point at
// the OpenStack Kolla images on quay.io.
type ImagesSpec struct {
	// +optional
	API string `json:"api,omitempty"`
	// +optional
	Conductor string `json:"conductor,omitempty"`
}

// TrustSpec configures the Keystone domain Magnum uses to hold the trustee
// users it creates on behalf of cluster owners.
//
// Magnum resolves the trustee domain on every policy check. If DomainID is set
// it is used directly; otherwise Magnum falls back to authenticating as the
// domain admin to look it up, which requires DomainAdminSecretRef.
type TrustSpec struct {
	// DomainName is the name of the trustee domain, e.g. "magnum".
	// +optional
	DomainName string `json:"domainName,omitempty"`

	// DomainID short-circuits Keystone auto-discovery. Strongly recommended:
	// without it every policy check performs a domain-admin authentication.
	// +optional
	DomainID string `json:"domainID,omitempty"`

	// DomainAdminSecretRef references a Secret with "username" and "password"
	// keys for the trustee domain administrator. Required for creating clusters.
	// +optional
	DomainAdminSecretRef *LocalObjectRef `json:"domainAdminSecretRef,omitempty"`
}

// MagnumDeploymentSpec defines the desired state of a Magnum deployment.
type MagnumDeploymentSpec struct {
	// KeystoneRef points at the KeystoneDeployment this service authenticates against.
	KeystoneRef LocalObjectRef `json:"keystoneRef"`

	// IssuerRef is the cert-manager Issuer used for internal TLS, matching the
	// issuerRef used by the upstream Yaook deployments.
	IssuerRef LocalObjectRef `json:"issuerRef"`

	// BackendCAIssuerRef is the Issuer the infra-operator uses for the CA behind
	// the MariaDB/RabbitMQ backends. Yaook's own deployments use the
	// self-signed issuer created by the quickstart. Defaults to
	// "selfsigned-issuer".
	// +optional
	BackendCAIssuerRef *LocalObjectRef `json:"backendCAIssuerRef,omitempty"`

	Region RegionSpec `json:"region"`

	// TargetRelease is the OpenStack release to deploy, e.g. "2025.1".
	// +kubebuilder:default="2025.1"
	// +optional
	TargetRelease string `json:"targetRelease,omitempty"`

	Database     DatabaseSpec     `json:"database"`
	MessageQueue MessageQueueSpec `json:"messageQueue"`
	API          APISpec          `json:"api"`

	// +optional
	Conductor ConductorSpec `json:"conductor,omitempty"`

	// +optional
	Images ImagesSpec `json:"images,omitempty"`

	// +optional
	Trust TrustSpec `json:"trust,omitempty"`

	// MagnumConfig is merged into the generated magnum.conf. Outer keys are
	// section names, inner keys are options. Values set here win over the
	// operator's generated defaults, except for credentials.
	// +optional
	MagnumConfig map[string]map[string]string `json:"magnumConfig,omitempty"`
}

// MagnumDeploymentStatus reports the observed state of a Magnum deployment.
type MagnumDeploymentStatus struct {
	// +optional
	Phase string `json:"phase,omitempty"`

	// +optional
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`

	// +optional
	// +listType=map
	// +listMapKey=type
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// Condition types reported on a MagnumDeployment.
const (
	ConditionDatabaseReady = "DatabaseReady"
	ConditionMessageQueue  = "MessageQueueReady"
	ConditionKeystoneReady = "KeystoneReady"
	ConditionAPIReady      = "APIReady"
	ConditionReady         = "Ready"
)

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:shortName=magnum;magnumdeploy,categories=yaook;openstack
// +kubebuilder:printcolumn:name="Phase",type=string,JSONPath=`.status.phase`
// +kubebuilder:printcolumn:name="Release",type=string,JSONPath=`.spec.targetRelease`
// +kubebuilder:printcolumn:name="FQDN",type=string,JSONPath=`.spec.api.ingress.fqdn`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// MagnumDeployment deploys the OpenStack Container Infrastructure Management
// service (Magnum) on top of a Yaook-managed OpenStack control plane.
type MagnumDeployment struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   MagnumDeploymentSpec   `json:"spec,omitempty"`
	Status MagnumDeploymentStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// MagnumDeploymentList contains a list of MagnumDeployment.
type MagnumDeploymentList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []MagnumDeployment `json:"items"`
}

func init() {
	SchemeBuilder.Register(&MagnumDeployment{}, &MagnumDeploymentList{})
}
