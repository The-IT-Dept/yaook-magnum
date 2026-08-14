package controller

import (
	"fmt"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	netv1 "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/utils/ptr"

	magnumv1alpha1 "github.com/the-it-dept/yaook-magnum/api/v1alpha1"
)

const (
	magnumAPIPort = 9511

	// The Kolla images run as the unprivileged "magnum" user.
	magnumUID int64 = 42428
	magnumGID int64 = 42428

	configMountPath = "/etc/magnum"
	caMountPath     = "/etc/magnum/ca"
	caFileName      = "ca-bundle.crt"

	defaultAPIImage       = "quay.io/openstack.kolla/magnum-api:2025.1-ubuntu-noble"
	defaultConductorImage = "quay.io/openstack.kolla/magnum-conductor:2025.1-ubuntu-noble"
)

// caFilePath is where the internal CA bundle is mounted inside the containers.
func caFilePath() string { return fmt.Sprintf("%s/%s", caMountPath, caFileName) }

func apiImage(cr *magnumv1alpha1.MagnumDeployment) string {
	if cr.Spec.Images.API != "" {
		return cr.Spec.Images.API
	}
	return defaultAPIImage
}

func conductorImage(cr *magnumv1alpha1.MagnumDeployment) string {
	if cr.Spec.Images.Conductor != "" {
		return cr.Spec.Images.Conductor
	}
	return defaultConductorImage
}

func selectorLabels(cr *magnumv1alpha1.MagnumDeployment, component string) map[string]string {
	return map[string]string{
		"app.kubernetes.io/name":       "magnum",
		"app.kubernetes.io/instance":   cr.Name,
		"app.kubernetes.io/component":  component,
		"app.kubernetes.io/managed-by": "yaook-magnum",
	}
}

// podVolumes mounts the rendered config and the CA bundle. configHash is
// stamped as an annotation so a config change rolls the pods.
func podVolumes(cr *magnumv1alpha1.MagnumDeployment) []corev1.Volume {
	return []corev1.Volume{
		{
			Name: "config",
			VolumeSource: corev1.VolumeSource{
				Secret: &corev1.SecretVolumeSource{
					SecretName:  nameConfigSecret(cr),
					DefaultMode: ptr.To(int32(0o440)),
				},
			},
		},
		{
			Name: "ca",
			VolumeSource: corev1.VolumeSource{
				ConfigMap: &corev1.ConfigMapVolumeSource{
					LocalObjectReference: corev1.LocalObjectReference{Name: nameCAConfigMap(cr)},
					DefaultMode:          ptr.To(int32(0o444)),
				},
			},
		},
	}
}

func podVolumeMounts() []corev1.VolumeMount {
	return []corev1.VolumeMount{
		{Name: "config", MountPath: configMountPath + "/magnum.conf", SubPath: "magnum.conf", ReadOnly: true},
		{Name: "ca", MountPath: caMountPath, ReadOnly: true},
	}
}

func podSecurityContext() *corev1.PodSecurityContext {
	return &corev1.PodSecurityContext{
		RunAsUser:    ptr.To(magnumUID),
		RunAsGroup:   ptr.To(magnumGID),
		FSGroup:      ptr.To(magnumGID),
		RunAsNonRoot: ptr.To(true),
	}
}

func containerSecurityContext() *corev1.SecurityContext {
	return &corev1.SecurityContext{
		AllowPrivilegeEscalation: ptr.To(false),
		ReadOnlyRootFilesystem:   ptr.To(false),
		Capabilities:             &corev1.Capabilities{Drop: []corev1.Capability{"ALL"}},
	}
}

// dbSyncContainer runs the alembic migrations. It is an initContainer on the
// API deployment; the conductor is only created once the API is available, so
// the migration never runs concurrently with itself.
func dbSyncContainer(cr *magnumv1alpha1.MagnumDeployment) corev1.Container {
	return corev1.Container{
		Name:            "db-sync",
		Image:           apiImage(cr),
		Args:            []string{"magnum-db-manage", "--config-file", configMountPath + "/magnum.conf", "upgrade"},
		VolumeMounts:    podVolumeMounts(),
		SecurityContext: containerSecurityContext(),
	}
}

func buildAPIDeployment(cr *magnumv1alpha1.MagnumDeployment, configHash string) *appsv1.Deployment {
	labels := selectorLabels(cr, "api")
	return &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: nameAPIDeployment(cr), Namespace: cr.Namespace, Labels: labels},
		Spec: appsv1.DeploymentSpec{
			Replicas: ptr.To(cr.Spec.API.Replicas),
			Selector: &metav1.LabelSelector{MatchLabels: labels},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels:      labels,
					Annotations: map[string]string{"magnum.yaook.cloud/config-hash": configHash},
				},
				Spec: corev1.PodSpec{
					SecurityContext: podSecurityContext(),
					InitContainers:  []corev1.Container{dbSyncContainer(cr)},
					Containers: []corev1.Container{{
						Name:  "magnum-api",
						Image: apiImage(cr),
						Args:  []string{"magnum-api", "--config-file", configMountPath + "/magnum.conf"},
						Ports: []corev1.ContainerPort{{Name: "api", ContainerPort: magnumAPIPort}},
						ReadinessProbe: &corev1.Probe{
							ProbeHandler: corev1.ProbeHandler{
								TCPSocket: &corev1.TCPSocketAction{Port: intstr.FromInt32(magnumAPIPort)},
							},
							InitialDelaySeconds: 10,
							PeriodSeconds:       10,
						},
						LivenessProbe: &corev1.Probe{
							ProbeHandler: corev1.ProbeHandler{
								TCPSocket: &corev1.TCPSocketAction{Port: intstr.FromInt32(magnumAPIPort)},
							},
							InitialDelaySeconds: 30,
							PeriodSeconds:       20,
						},
						Resources:       cr.Spec.API.Resources,
						VolumeMounts:    podVolumeMounts(),
						SecurityContext: containerSecurityContext(),
					}},
					Volumes: podVolumes(cr),
				},
			},
		},
	}
}

func buildConductorDeployment(cr *magnumv1alpha1.MagnumDeployment, configHash string) *appsv1.Deployment {
	labels := selectorLabels(cr, "conductor")
	replicas := cr.Spec.Conductor.Replicas
	if replicas == 0 {
		replicas = 1
	}
	return &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: nameConductorDeployment(cr), Namespace: cr.Namespace, Labels: labels},
		Spec: appsv1.DeploymentSpec{
			Replicas: ptr.To(replicas),
			Selector: &metav1.LabelSelector{MatchLabels: labels},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels:      labels,
					Annotations: map[string]string{"magnum.yaook.cloud/config-hash": configHash},
				},
				Spec: corev1.PodSpec{
					SecurityContext: podSecurityContext(),
					Containers: []corev1.Container{{
						Name:            "magnum-conductor",
						Image:           conductorImage(cr),
						Args:            []string{"magnum-conductor", "--config-file", configMountPath + "/magnum.conf"},
						Resources:       cr.Spec.Conductor.Resources,
						VolumeMounts:    podVolumeMounts(),
						SecurityContext: containerSecurityContext(),
					}},
					Volumes: podVolumes(cr),
				},
			},
		},
	}
}

func buildAPIService(cr *magnumv1alpha1.MagnumDeployment) *corev1.Service {
	labels := selectorLabels(cr, "api")
	return &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{Name: nameAPIService(cr), Namespace: cr.Namespace, Labels: labels},
		Spec: corev1.ServiceSpec{
			Selector: labels,
			Ports: []corev1.ServicePort{{
				Name:       "api",
				Port:       magnumAPIPort,
				TargetPort: intstr.FromInt32(magnumAPIPort),
				Protocol:   corev1.ProtocolTCP,
			}},
		},
	}
}

func buildIngress(cr *magnumv1alpha1.MagnumDeployment) *netv1.Ingress {
	ing := cr.Spec.API.Ingress
	pathType := netv1.PathTypePrefix
	obj := &netv1.Ingress{
		ObjectMeta: metav1.ObjectMeta{
			Name:      cr.Name,
			Namespace: cr.Namespace,
			Labels:    selectorLabels(cr, "api"),
		},
		Spec: netv1.IngressSpec{
			IngressClassName: ptr.To(ing.IngressClassName),
			Rules: []netv1.IngressRule{{
				Host: ing.FQDN,
				IngressRuleValue: netv1.IngressRuleValue{
					HTTP: &netv1.HTTPIngressRuleValue{
						Paths: []netv1.HTTPIngressPath{{
							Path:     "/",
							PathType: &pathType,
							Backend: netv1.IngressBackend{
								Service: &netv1.IngressServiceBackend{
									Name: nameAPIService(cr),
									Port: netv1.ServiceBackendPort{Number: magnumAPIPort},
								},
							},
						}},
					},
				},
			}},
		},
	}
	if ing.ExternalCertificateSecretRef != nil {
		obj.Spec.TLS = []netv1.IngressTLS{{
			Hosts:      []string{ing.FQDN},
			SecretName: ing.ExternalCertificateSecretRef.Name,
		}}
	}
	return obj
}
