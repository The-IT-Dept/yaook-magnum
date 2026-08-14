# Cluster API on this Yaook deployment: what's needed

Status as of the initial Magnum bring-up. Written against the live cluster, not
from documentation.

## Where things stand

Working:

- `magnum-api` and `magnum-conductor` deployed by this operator, registered in
  Keystone as service type `container-infra`. `/v1/clustertemplates`,
  `/v1/clusters` and `/v1/stats` all return 200; `magnum-conductor` reports
  `state=up`.
- The Magnum panels render in Horizon.
- The Kolla 2025.1 conductor image already ships **`magnum-cluster-api`
  0.38.1**, and its drivers are registered: `k8s_cluster_api_ubuntu`,
  `k8s_cluster_api_ubuntu_focal`, `k8s_cluster_api_debian`,
  `k8s_cluster_api_flatcar`, `k8s_cluster_api_rockylinux`. No custom image is
  needed to get the CAPI driver.

Not present. Measured against the running deployment:

| Thing | State |
|---|---|
| Glance images | 0 |
| Neutron networks | 0 |
| Octavia | not deployed |
| Barbican | not deployed |
| Cinder | not deployed |
| Heat | not deployed (and **not needed** for the CAPI driver) |
| Cluster API / CAPO | not installed |
| External / provider network | none — OVN `controller.configTemplates` is `[]`, so there are no bridge mappings |

## How the driver works

`magnum-cluster-api` does not orchestrate VMs itself. It translates a Magnum
cluster into Cluster API objects — `cluster.x-k8s.io`,
`infrastructure.cluster.x-k8s.io` (CAPO), `controlplane.cluster.x-k8s.io`
(`KubeadmControlPlane`) and `addons.cluster.x-k8s.io` (`ClusterResourceSet`) —
and writes them into a **management Kubernetes cluster**. Cluster API and the
OpenStack provider then build the workload cluster by calling Nova, Neutron,
Cinder and Octavia.

It finds that management cluster via `pykube.KubeConfig.from_env()`, which
resolves `KUBECONFIG`, then `~/.kube/config`, then the in-cluster service
account.

That last point matters: `magnum-conductor` already runs inside this
Kubernetes cluster, so **the yanook cluster can be its own CAPI management
cluster**. No second cluster is required — it needs a service account with
rights over the Cluster API CRDs.

## What's needed

### 1. External networking — the real blocker

Everything else is installation; this one is a design decision.

Neutron is deployed with OVN, but `spec.setup.ovn.controller.configTemplates`
is empty, so no bridge mappings exist. There is no provider network, so no
external connectivity and no floating IPs.

CAPI needs floating IPs (or a routed provider network) to reach each workload
cluster's API server. Without this, clusters will build and then never become
reachable.

This needs a VLAN and subnet on the Juniper trunked to the hypervisors, an OVN
bridge mapping to `physnet1`, and a Neutron provider network with a floating IP
pool. Worth doing deliberately, since it also determines how tenant workloads
get off the box at all.

### 2. Octavia (+ Barbican)

CAPO puts the workload cluster's API server behind a load balancer. Yaook ships
`octavia-operator` and `barbican-operator`, so this is a deployment exercise
rather than new code. Octavia also wants a management network for its amphorae
and an amphora image in Glance.

It is possible to run CAPO without a load balancer (single control-plane node,
API server on a floating IP), which is a reasonable first milestone to prove the
pipeline before committing to Octavia.

### 3. Images and flavors

- A CAPI-compatible image per Kubernetes version — the driver's own
  `magnum-cluster-api-image-builder` produces these, or use the prebuilt
  Ubuntu/Flatcar images. They must be uploaded to Glance and tagged with the
  Kubernetes version.
- Flavors sized for control plane and workers. Currently there are none.
- Note the hypervisors are nested KVM on an E5-2660 v3 with ~77 GB RAM free on
  the host, shared with production VMs. A workload cluster of 1 control plane +
  2 workers is realistic; anything larger will not fit.

### 4. Cluster API in this cluster

Install `clusterctl`-managed components into the yanook cluster: cluster-api
core, the kubeadm bootstrap and control-plane providers, and
`cluster-api-provider-openstack`. CAPO needs a `clouds.yaml` credential for the
OpenStack side.

### 5. Operator work

To make this first-class rather than hand-wired, `yaook-magnum` needs:

- RBAC for the conductor's service account over `cluster.x-k8s.io`,
  `infrastructure.cluster.x-k8s.io`, `controlplane.cluster.x-k8s.io`,
  `bootstrap.cluster.x-k8s.io` and `addons.cluster.x-k8s.io`, plus a dedicated
  ServiceAccount for the conductor pod (it currently runs with the default one).
- A `spec.clusterAPI` block: whether to use the in-cluster service account or a
  referenced kubeconfig Secret, and the `[capi_client]` options
  (`endpoint_type`, `ca_file`, `insecure`).
- Optionally the `[auto_scaling] image_repository` option for the autoscaler.

### 6. Trustee domain admin

Already wired (`spec.trust`), but note that *creating* clusters exercises the
domain admin credentials, which listing does not. This path is configured but
unproven.

## Suggested order

1. Provider network + floating IPs. Nothing else can be validated without it.
2. Flavors and one CAPI image in Glance.
3. Cluster API + CAPO into the yanook cluster; conductor RBAC.
4. Prove one cluster without Octavia (single control plane, floating IP).
5. Octavia + Barbican, then multi-control-plane clusters.
6. Fold the wiring from 3 into the operator as `spec.clusterAPI`.

Steps 2–4 are a day's work once step 1 exists. Step 1 is the one that needs a
decision about how this rack gets tenant traffic in and out.
