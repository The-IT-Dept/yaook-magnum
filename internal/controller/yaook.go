package controller

import (
	"fmt"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"

	magnumv1alpha1 "github.com/the-it-dept/yaook-magnum/api/v1alpha1"
)

// Yaook GroupVersionKinds. Yaook's operators are written in Python and ship no
// Go types, so these resources are driven as unstructured objects.
var (
	gvkMySQLService     = schema.GroupVersionKind{Group: "infra.yaook.cloud", Version: "v1", Kind: "MySQLService"}
	gvkMySQLUser        = schema.GroupVersionKind{Group: "infra.yaook.cloud", Version: "v1", Kind: "MySQLUser"}
	gvkAMQPServer       = schema.GroupVersionKind{Group: "infra.yaook.cloud", Version: "v1", Kind: "AMQPServer"}
	gvkAMQPUser         = schema.GroupVersionKind{Group: "infra.yaook.cloud", Version: "v1", Kind: "AMQPUser"}
	gvkKeystoneUser     = schema.GroupVersionKind{Group: "yaook.cloud", Version: "v1", Kind: "KeystoneUser"}
	gvkKeystoneEndpoint = schema.GroupVersionKind{Group: "yaook.cloud", Version: "v1", Kind: "KeystoneEndpoint"}
)

// Naming scheme for the resources this operator owns. Kept in one place so the
// generated config and the provisioned objects cannot drift apart.

func nameDatabase(cr *magnumv1alpha1.MagnumDeployment) string     { return cr.Name + "-magnum" }
func nameMessageQueue(cr *magnumv1alpha1.MagnumDeployment) string { return cr.Name + "-magnum" }
func nameDBUser(cr *magnumv1alpha1.MagnumDeployment) string       { return cr.Name + "-db-api" }
func nameMQUser(cr *magnumv1alpha1.MagnumDeployment) string       { return cr.Name + "-mq-api" }
func nameDBPasswordSecret(cr *magnumv1alpha1.MagnumDeployment) string {
	return cr.Name + "-db-api-password"
}
func nameMQPasswordSecret(cr *magnumv1alpha1.MagnumDeployment) string {
	return cr.Name + "-mq-api-password"
}
func nameKeystoneUser(cr *magnumv1alpha1.MagnumDeployment) string { return cr.Name + "-magnum" }
func nameKeystoneEndpoint(cr *magnumv1alpha1.MagnumDeployment) string {
	return cr.Name + "-api-endpoint"
}
func nameConfigSecret(cr *magnumv1alpha1.MagnumDeployment) string  { return cr.Name + "-config" }
func nameCAConfigMap(cr *magnumv1alpha1.MagnumDeployment) string   { return cr.Name + "-ca" }
func nameAPIDeployment(cr *magnumv1alpha1.MagnumDeployment) string { return cr.Name + "-api" }
func nameConductorDeployment(cr *magnumv1alpha1.MagnumDeployment) string {
	return cr.Name + "-conductor"
}
func nameAPIService(cr *magnumv1alpha1.MagnumDeployment) string { return cr.Name + "-api" }

// dbHost is the MariaDB frontend Service created by the infra-operator for a
// MySQLService named n: "<n>-db-frontend".
func dbHost(cr *magnumv1alpha1.MagnumDeployment) string {
	return fmt.Sprintf("%s-db-frontend.%s", nameDatabase(cr), cr.Namespace)
}

// mqHost is the RabbitMQ frontend Service created by the infra-operator for an
// AMQPServer named n: "<n>-mq-frontend".
func mqHost(cr *magnumv1alpha1.MagnumDeployment) string {
	return fmt.Sprintf("%s-mq-frontend.%s", nameMessageQueue(cr), cr.Namespace)
}

func apiServiceHost(cr *magnumv1alpha1.MagnumDeployment) string {
	return fmt.Sprintf("%s.%s.svc", nameAPIService(cr), cr.Namespace)
}

func newUnstructured(gvk schema.GroupVersionKind, name, namespace string, spec map[string]any) *unstructured.Unstructured {
	u := &unstructured.Unstructured{}
	u.SetGroupVersionKind(gvk)
	u.SetName(name)
	u.SetNamespace(namespace)
	if spec != nil {
		_ = unstructured.SetNestedMap(u.Object, spec, "spec")
	}
	return u
}

func buildMySQLService(cr *magnumv1alpha1.MagnumDeployment) *unstructured.Unstructured {
	db := cr.Spec.Database
	return newUnstructured(gvkMySQLService, nameDatabase(cr), cr.Namespace, map[string]any{
		"database":         "magnum",
		"implementation":   "MariaDB",
		"targetRelease":    db.TargetRelease,
		"replicas":         int64(db.Replicas),
		"storageClassName": db.StorageClassName,
		"storageSize":      db.StorageSize.String(),
		"frontendIssuerRef": map[string]any{
			"name": cr.Spec.IssuerRef.Name,
		},
		"proxy": map[string]any{
			"replicas": int64(db.ProxyReplicas),
		},
		"backup": map[string]any{
			"schedule": db.BackupSchedule,
		},
	})
}

func buildMySQLUser(cr *magnumv1alpha1.MagnumDeployment) *unstructured.Unstructured {
	return newUnstructured(gvkMySQLUser, nameDBUser(cr), cr.Namespace, map[string]any{
		"user":               "api",
		"databasePrivileges": []any{"ALL PRIVILEGES"},
		"globalPrivileges":   []any{},
		"serviceRef":         map[string]any{"name": nameDatabase(cr)},
		"passwordSecretKeyRef": map[string]any{
			"name": nameDBPasswordSecret(cr),
			"key":  "password",
		},
	})
}

func buildAMQPServer(cr *magnumv1alpha1.MagnumDeployment) *unstructured.Unstructured {
	mq := cr.Spec.MessageQueue
	return newUnstructured(gvkAMQPServer, nameMessageQueue(cr), cr.Namespace, map[string]any{
		"implementation":   "RabbitMQ",
		"targetRelease":    mq.TargetRelease,
		"replicas":         int64(mq.Replicas),
		"storageClassName": mq.StorageClassName,
		"storageSize":      mq.StorageSize.String(),
		"enableExporter":   true,
		"enabledPlugins":   "rabbitmq_management,rabbitmq_prometheus",
		"frontendIssuerRef": map[string]any{
			"name": cr.Spec.IssuerRef.Name,
		},
		"backendCAIssuerRef": map[string]any{
			"name": "selfsigned-issuer",
		},
	})
}

func buildAMQPUser(cr *magnumv1alpha1.MagnumDeployment) *unstructured.Unstructured {
	return newUnstructured(gvkAMQPUser, nameMQUser(cr), cr.Namespace, map[string]any{
		"user":      "api",
		"serverRef": map[string]any{"name": nameMessageQueue(cr)},
		"passwordSecretKeyRef": map[string]any{
			"name": nameMQPasswordSecret(cr),
			"key":  "password",
		},
	})
}

func buildKeystoneUser(cr *magnumv1alpha1.MagnumDeployment) *unstructured.Unstructured {
	return newUnstructured(gvkKeystoneUser, nameKeystoneUser(cr), cr.Namespace, map[string]any{
		"keystoneRef": map[string]any{
			"kind": "KeystoneDeployment",
			"name": cr.Spec.KeystoneRef.Name,
		},
	})
}

// buildKeystoneEndpoint registers Magnum in the service catalog. Magnum's
// service type is "container-infra"; the OpenStack client subcommand is
// `openstack coe ...`.
func buildKeystoneEndpoint(cr *magnumv1alpha1.MagnumDeployment) *unstructured.Unstructured {
	internal := fmt.Sprintf("http://%s:%d", apiServiceHost(cr), magnumAPIPort)
	public := fmt.Sprintf("https://%s:%d", cr.Spec.API.Ingress.FQDN, cr.Spec.API.Ingress.Port)
	return newUnstructured(gvkKeystoneEndpoint, nameKeystoneEndpoint(cr), cr.Namespace, map[string]any{
		"servicename": "magnum",
		"servicetype": "container-infra",
		"description": "OpenStack Container Infrastructure Management",
		"keystoneRef": map[string]any{
			"kind": "KeystoneDeployment",
			"name": cr.Spec.KeystoneRef.Name,
		},
		"region": map[string]any{"name": cr.Spec.Region.Name},
		"endpoints": map[string]any{
			"admin":    internal,
			"internal": internal,
			"public":   public,
		},
	})
}
