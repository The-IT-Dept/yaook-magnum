package controller

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"strings"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	netv1 "k8s.io/api/networking/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log"

	magnumv1alpha1 "github.com/the-it-dept/yaook-magnum/api/v1alpha1"
	"github.com/the-it-dept/yaook-magnum/internal/magnum"
)

// MagnumDeploymentReconciler reconciles a MagnumDeployment object.
type MagnumDeploymentReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

// +kubebuilder:rbac:groups=magnum.yaook.cloud,resources=magnumdeployments,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=magnum.yaook.cloud,resources=magnumdeployments/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=magnum.yaook.cloud,resources=magnumdeployments/finalizers,verbs=update
// +kubebuilder:rbac:groups=infra.yaook.cloud,resources=mysqlservices;mysqlusers;amqpservers;amqpusers,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=yaook.cloud,resources=keystoneusers;keystoneendpoints,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups="",resources=secrets;configmaps;services,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=apps,resources=deployments,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=networking.k8s.io,resources=ingresses,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=coordination.k8s.io,resources=leases,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups="",resources=events,verbs=create;patch

const requeueWaiting = 15 * time.Second

func (r *MagnumDeploymentReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	l := log.FromContext(ctx)

	cr := &magnumv1alpha1.MagnumDeployment{}
	if err := r.Get(ctx, req.NamespacedName, cr); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	// 1. Credentials the infra-operator consumes. We own the passwords; the
	//    infra-operator reads them and provisions the DB/MQ users.
	dbPass, err := r.ensurePassword(ctx, cr, nameDBPasswordSecret(cr))
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("db password: %w", err)
	}
	mqPass, err := r.ensurePassword(ctx, cr, nameMQPasswordSecret(cr))
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("mq password: %w", err)
	}

	// 2. Infra layer: MariaDB + RabbitMQ, provisioned by the Yaook infra-operator.
	for _, obj := range []*unstructured.Unstructured{
		buildMySQLService(cr), buildMySQLUser(cr),
		buildAMQPServer(cr), buildAMQPUser(cr),
		buildKeystoneUser(cr),
	} {
		if err := r.applyUnstructured(ctx, cr, obj); err != nil {
			return ctrl.Result{}, fmt.Errorf("apply %s/%s: %w", obj.GetKind(), obj.GetName(), err)
		}
	}

	// 3. The KeystoneUser controller creates the service account credentials in
	//    a secret it owns. It is not named in the status, so we find it by
	//    ownerReference.
	idCreds, err := r.resolveKeystoneCredentials(ctx, cr)
	if err != nil {
		l.Info("waiting for keystone service credentials", "reason", err.Error())
		r.setCondition(cr, magnumv1alpha1.ConditionKeystoneReady, metav1.ConditionFalse, "Pending", err.Error())
		return r.finish(ctx, cr, "Pending", ctrl.Result{RequeueAfter: requeueWaiting})
	}
	r.setCondition(cr, magnumv1alpha1.ConditionKeystoneReady, metav1.ConditionTrue, "Success", "service credentials available")

	// 4. Internal CA bundle, so Magnum trusts the Yaook-issued certificates on
	//    Keystone, MariaDB and RabbitMQ.
	if err := r.ensureCABundle(ctx, cr); err != nil {
		l.Info("waiting for internal CA", "reason", err.Error())
		return r.finish(ctx, cr, "Pending", ctrl.Result{RequeueAfter: requeueWaiting})
	}

	// 5. Render magnum.conf and store it.
	// Trustee domain admin credentials, if provided. Only needed for creating
	// clusters; listing works with trust.domainID alone.
	var trustUser, trustPass string
	if ref := cr.Spec.Trust.DomainAdminSecretRef; ref != nil {
		sec := &corev1.Secret{}
		if err := r.Get(ctx, types.NamespacedName{Name: ref.Name, Namespace: cr.Namespace}, sec); err != nil {
			l.Info("waiting for trustee domain admin secret", "secret", ref.Name)
			return r.finish(ctx, cr, "Pending", ctrl.Result{RequeueAfter: requeueWaiting})
		}
		trustUser = string(sec.Data["username"])
		trustPass = string(sec.Data["password"])
	}

	cfg := magnum.Render(magnum.CredentialInput{
		DBUser: "api", DBPassword: dbPass, DBHost: dbHost(cr), DBName: "magnum",
		MQUser: "api", MQPassword: mqPass, MQHost: mqHost(cr),
		KeystoneAuthURL:    fmt.Sprintf("https://keystone.%s.svc:5000/v3", cr.Namespace),
		KeystoneUsername:   idCreds["OS_USERNAME"],
		KeystonePassword:   idCreds["OS_PASSWORD"],
		KeystoneProject:    valueOr(idCreds["OS_PROJECT_NAME"], "service"),
		KeystoneUserDomain: valueOr(idCreds["OS_USER_DOMAIN_NAME"], "Default"),
		KeystoneProjDomain: valueOr(idCreds["OS_PROJECT_DOMAIN_NAME"], "Default"),
		RegionName:         cr.Spec.Region.Name,
		CAFile:             caFilePath(),
		TrustDomainName:    cr.Spec.Trust.DomainName,
		TrustDomainID:      cr.Spec.Trust.DomainID,
		TrustAdminUser:     trustUser,
		TrustAdminPassword: trustPass,
	}, cr.Spec.MagnumConfig)

	configHash := hashString(cfg)
	if err := r.ensureConfigSecret(ctx, cr, cfg); err != nil {
		return ctrl.Result{}, fmt.Errorf("config secret: %w", err)
	}

	// 6. API deployment, service and ingress.
	if err := r.applyOwned(ctx, cr, buildAPIService(cr)); err != nil {
		return ctrl.Result{}, fmt.Errorf("api service: %w", err)
	}
	if err := r.applyOwned(ctx, cr, buildAPIDeployment(cr, configHash)); err != nil {
		return ctrl.Result{}, fmt.Errorf("api deployment: %w", err)
	}
	if cr.Spec.API.Ingress.CreateIngress == nil || *cr.Spec.API.Ingress.CreateIngress {
		if err := r.applyOwned(ctx, cr, buildIngress(cr)); err != nil {
			return ctrl.Result{}, fmt.Errorf("ingress: %w", err)
		}
	}

	// 7. The conductor is only rolled out once the API deployment is available.
	//    The API's init container owns the schema migration, so this ordering
	//    keeps alembic single-writer.
	apiReady, err := r.deploymentAvailable(ctx, cr.Namespace, nameAPIDeployment(cr))
	if err != nil {
		return ctrl.Result{}, err
	}
	if !apiReady {
		r.setCondition(cr, magnumv1alpha1.ConditionAPIReady, metav1.ConditionFalse, "Progressing", "waiting for magnum-api")
		return r.finish(ctx, cr, "Progressing", ctrl.Result{RequeueAfter: requeueWaiting})
	}
	r.setCondition(cr, magnumv1alpha1.ConditionAPIReady, metav1.ConditionTrue, "Success", "magnum-api available")

	if err := r.applyOwned(ctx, cr, buildConductorDeployment(cr, configHash)); err != nil {
		return ctrl.Result{}, fmt.Errorf("conductor deployment: %w", err)
	}

	// 8. Register in the Keystone service catalog only once the API can serve.
	if err := r.applyUnstructured(ctx, cr, buildKeystoneEndpoint(cr)); err != nil {
		return ctrl.Result{}, fmt.Errorf("keystone endpoint: %w", err)
	}

	r.setCondition(cr, magnumv1alpha1.ConditionReady, metav1.ConditionTrue, "Success", "magnum deployed")
	return r.finish(ctx, cr, "Updated", ctrl.Result{RequeueAfter: 5 * time.Minute})
}

// ensurePassword creates a random password secret once and never rotates it
// implicitly, since the infra-operator provisions the DB/MQ user from it.
func (r *MagnumDeploymentReconciler) ensurePassword(ctx context.Context, cr *magnumv1alpha1.MagnumDeployment, name string) (string, error) {
	sec := &corev1.Secret{}
	err := r.Get(ctx, types.NamespacedName{Name: name, Namespace: cr.Namespace}, sec)
	if err == nil {
		if pw, ok := sec.Data["password"]; ok && len(pw) > 0 {
			return string(pw), nil
		}
	} else if !apierrors.IsNotFound(err) {
		return "", err
	}

	pw, err := randomPassword()
	if err != nil {
		return "", err
	}
	sec = &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: cr.Namespace, Labels: selectorLabels(cr, "credentials")},
		Type:       corev1.SecretTypeOpaque,
		StringData: map[string]string{"password": pw},
	}
	if err := controllerutil.SetControllerReference(cr, sec, r.Scheme); err != nil {
		return "", err
	}
	if err := r.Create(ctx, sec); err != nil {
		if apierrors.IsAlreadyExists(err) {
			// Lost a race; re-read.
			if err := r.Get(ctx, types.NamespacedName{Name: name, Namespace: cr.Namespace}, sec); err != nil {
				return "", err
			}
			return string(sec.Data["password"]), nil
		}
		return "", err
	}
	return pw, nil
}

// resolveKeystoneCredentials finds the secret the KeystoneUser controller
// created for us. Yaook names it "<keystoneuser>-idcreds-<random>" and sets an
// ownerReference back to the KeystoneUser, which is what we match on.
func (r *MagnumDeploymentReconciler) resolveKeystoneCredentials(ctx context.Context, cr *magnumv1alpha1.MagnumDeployment) (map[string]string, error) {
	ku := &unstructured.Unstructured{}
	ku.SetGroupVersionKind(gvkKeystoneUser)
	if err := r.Get(ctx, types.NamespacedName{Name: nameKeystoneUser(cr), Namespace: cr.Namespace}, ku); err != nil {
		return nil, fmt.Errorf("KeystoneUser not found yet")
	}

	secrets := &corev1.SecretList{}
	if err := r.List(ctx, secrets, client.InNamespace(cr.Namespace)); err != nil {
		return nil, err
	}
	for i := range secrets.Items {
		s := &secrets.Items[i]
		if !strings.Contains(s.Name, "-idcreds-") {
			continue
		}
		for _, ref := range s.OwnerReferences {
			if ref.UID == ku.GetUID() {
				out := map[string]string{}
				for k, v := range s.Data {
					out[k] = string(v)
				}
				if out["OS_USERNAME"] == "" || out["OS_PASSWORD"] == "" {
					return nil, fmt.Errorf("credentials secret %s incomplete", s.Name)
				}
				return out, nil
			}
		}
	}
	return nil, fmt.Errorf("no idcreds secret owned by KeystoneUser %s yet", nameKeystoneUser(cr))
}

// ensureCABundle copies the Yaook internal CA into a ConfigMap the pods mount.
func (r *MagnumDeploymentReconciler) ensureCABundle(ctx context.Context, cr *magnumv1alpha1.MagnumDeployment) error {
	caSecret := &corev1.Secret{}
	// cert-manager stores the CA under the name of the Certificate's secretName,
	// which for the Yaook quickstart issuer is "yaook-internal-ca".
	if err := r.Get(ctx, types.NamespacedName{Name: "yaook-internal-ca", Namespace: cr.Namespace}, caSecret); err != nil {
		return fmt.Errorf("internal CA secret not available: %w", err)
	}
	ca, ok := caSecret.Data["ca.crt"]
	if !ok || len(ca) == 0 {
		ca, ok = caSecret.Data["tls.crt"]
		if !ok || len(ca) == 0 {
			return fmt.Errorf("internal CA secret has no ca.crt or tls.crt")
		}
	}
	cm := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Name: nameCAConfigMap(cr), Namespace: cr.Namespace, Labels: selectorLabels(cr, "ca")},
		Data:       map[string]string{caFileName: string(ca)},
	}
	return r.applyOwned(ctx, cr, cm)
}

func (r *MagnumDeploymentReconciler) ensureConfigSecret(ctx context.Context, cr *magnumv1alpha1.MagnumDeployment, cfg string) error {
	sec := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: nameConfigSecret(cr), Namespace: cr.Namespace, Labels: selectorLabels(cr, "config")},
		Type:       corev1.SecretTypeOpaque,
		StringData: map[string]string{"magnum.conf": cfg},
	}
	return r.applyOwned(ctx, cr, sec)
}

func (r *MagnumDeploymentReconciler) deploymentAvailable(ctx context.Context, ns, name string) (bool, error) {
	d := &appsv1.Deployment{}
	if err := r.Get(ctx, types.NamespacedName{Name: name, Namespace: ns}, d); err != nil {
		if apierrors.IsNotFound(err) {
			return false, nil
		}
		return false, err
	}
	return d.Status.ReadyReplicas > 0, nil
}

// applyOwned creates or updates a typed object owned by the CR.
func (r *MagnumDeploymentReconciler) applyOwned(ctx context.Context, cr *magnumv1alpha1.MagnumDeployment, obj client.Object) error {
	if err := controllerutil.SetControllerReference(cr, obj, r.Scheme); err != nil {
		return err
	}
	existing, ok := obj.DeepCopyObject().(client.Object)
	if !ok {
		return fmt.Errorf("object is not a client.Object")
	}
	err := r.Get(ctx, client.ObjectKeyFromObject(obj), existing)
	if apierrors.IsNotFound(err) {
		return r.Create(ctx, obj)
	}
	if err != nil {
		return err
	}
	obj.SetResourceVersion(existing.GetResourceVersion())
	// Services own their spec.clusterIP; preserve it across updates.
	if svc, isSvc := obj.(*corev1.Service); isSvc {
		if cur, isCur := existing.(*corev1.Service); isCur {
			svc.Spec.ClusterIP = cur.Spec.ClusterIP
			svc.Spec.ClusterIPs = cur.Spec.ClusterIPs
		}
	}
	return r.Update(ctx, obj)
}

// applyUnstructured creates or updates a Yaook CRD instance owned by the CR.
// Only spec is reconciled; the Yaook operators own status and their own
// annotations, so those are left untouched.
func (r *MagnumDeploymentReconciler) applyUnstructured(ctx context.Context, cr *magnumv1alpha1.MagnumDeployment, obj *unstructured.Unstructured) error {
	if err := controllerutil.SetControllerReference(cr, obj, r.Scheme); err != nil {
		return err
	}
	existing := &unstructured.Unstructured{}
	existing.SetGroupVersionKind(obj.GroupVersionKind())
	err := r.Get(ctx, client.ObjectKeyFromObject(obj), existing)
	if apierrors.IsNotFound(err) {
		return r.Create(ctx, obj)
	}
	if err != nil {
		return err
	}
	desiredSpec, _, _ := unstructured.NestedMap(obj.Object, "spec")
	currentSpec, _, _ := unstructured.NestedMap(existing.Object, "spec")

	// The Yaook operators continuously write their own annotations and status
	// onto these objects. Writing unconditionally would race them and produce a
	// stream of "object has been modified" conflicts, so only update when the
	// spec we own has actually drifted.
	sameSpec := equality.Semantic.DeepEqual(desiredSpec, currentSpec)
	sameOwner := equality.Semantic.DeepEqual(existing.GetOwnerReferences(), obj.GetOwnerReferences())
	if sameSpec && sameOwner {
		return nil
	}

	if err := unstructured.SetNestedMap(existing.Object, desiredSpec, "spec"); err != nil {
		return err
	}
	existing.SetOwnerReferences(obj.GetOwnerReferences())
	if err := r.Update(ctx, existing); err != nil {
		// A conflict here just means the Yaook operator wrote first; the next
		// reconcile will settle it.
		if apierrors.IsConflict(err) {
			return nil
		}
		return err
	}
	return nil
}

func (r *MagnumDeploymentReconciler) setCondition(cr *magnumv1alpha1.MagnumDeployment, condType string, status metav1.ConditionStatus, reason, msg string) {
	meta := metav1.Condition{
		Type:               condType,
		Status:             status,
		Reason:             reason,
		Message:            msg,
		LastTransitionTime: metav1.Now(),
		ObservedGeneration: cr.Generation,
	}
	for i, c := range cr.Status.Conditions {
		if c.Type == condType {
			if c.Status == status && c.Reason == reason && c.Message == msg {
				return
			}
			cr.Status.Conditions[i] = meta
			return
		}
	}
	cr.Status.Conditions = append(cr.Status.Conditions, meta)
}

func (r *MagnumDeploymentReconciler) finish(ctx context.Context, cr *magnumv1alpha1.MagnumDeployment, phase string, res ctrl.Result) (ctrl.Result, error) {
	cr.Status.Phase = phase
	cr.Status.ObservedGeneration = cr.Generation
	if err := r.Status().Update(ctx, cr); err != nil {
		if apierrors.IsConflict(err) {
			return ctrl.Result{Requeue: true}, nil
		}
		return ctrl.Result{}, err
	}
	return res, nil
}

func (r *MagnumDeploymentReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&magnumv1alpha1.MagnumDeployment{}).
		Owns(&appsv1.Deployment{}).
		Owns(&corev1.Secret{}).
		Owns(&corev1.Service{}).
		Owns(&netv1.Ingress{}).
		Named("magnumdeployment").
		Complete(r)
}

func randomPassword() (string, error) {
	buf := make([]byte, 24)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	// URL-safe and free of characters that need escaping in a connection URL.
	return strings.NewReplacer("-", "", "_", "", "=", "").Replace(base64.URLEncoding.EncodeToString(buf)), nil
}

func hashString(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:16])
}

func valueOr(v, fallback string) string {
	if v == "" {
		return fallback
	}
	return v
}
