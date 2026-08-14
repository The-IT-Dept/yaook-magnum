# Cluster API on this Yaook deployment: what's needed

Status as of the initial Magnum bring-up. Written against the live cluster, not
from documentation.

> **Update:** the networking blocker described below is resolved. A flat
> provider network is live on VLAN 100 (`157.20.112.128/25`), floating IPs work,
> and Octavia is running with the **OVN provider driver** — no amphora VMs. See
> "Resolved" at the end.

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


## Resolved

### Provider network (was the blocker)

VLAN 100 is trunked to the hypervisors on `ae1.100`. Each node has a dedicated
NIC on it (enumerated as `enp6s19`), brought up with no addressing; OVN plugs it
into `br-ex` via Yaook's `bridgeConfig`:

```yaml
setup:
  ovn:
    controller:
      configTemplates:
        - nodeSelectors:
            - matchLabels: {network.yaook.cloud/neutron-ovn-agent: "true"}
          bridgeConfig:
            - {bridgeName: br-ex, uplinkDevice: enp6s19, openstackPhysicalNetwork: physnet1}
```

Network `public` is flat on `physnet1`, external + shared, `157.20.112.128/25`,
gateway `.129`, pool `.130-.254`. Instances and floating IPs are both reachable
from the Juniper — note the subnet lives in the `PUBLIC` VRF, so tests need
`ping ... routing-instance PUBLIC`.

**Gotcha:** the OVN rollout deadlocks. Yaook serialises hypervisor disruption
behind an "l2 lock" and one node can sit in `WaitingForDependency` forever.
Restarting `nova-compute-operator` and `neutron-ovn-operator` clears it. Expect
this on any future OVN config change.

### Octavia via the OVN provider — no amphorae

Yaook already ships and configures `ovn-octavia-provider` (8.1.0): the `ovn`
driver is registered, `enabled_provider_agents = ovn`, and `[ovn]` has the
NB/SB connections and certificates wired up. The only reason load balancers
were failing is that **amphora is the default provider** and the amphora path
is broken here (the amphora presents a cert whose Authority Key Id does not
match Octavia's CA, despite Octavia's own signing key and CA cert matching).

The OVN driver sidesteps all of it — no amphora VM, no management network, no
certificates, no image, no flavor:

```yaml
octaviaConfig:
  api_settings:
    default_provider_driver: ovn
    enabled_provider_drivers: "ovn:Octavia OVN driver."
```

A load balancer now reaches `ACTIVE/ONLINE` in about 20 seconds, and OVN
programs the datapath directly:

```
vips: {"192.168.100.250:80"="192.168.100.204:80"}
```

Verified with real traffic from outside: floating IP -> router -> OVN VIP ->
backend returns HTTP 200.

Trade-off worth knowing: the OVN provider does TCP/UDP/SCTP only. No
TERMINATED_HTTPS, no L7 policies, and limited health monitoring. That is fine
for a Kubernetes API endpoint, which is what CAPI needs.

## Remaining risks for CAPI

**The metadata service is unreliable, and cloud-init depends on it.** The OVN
metadata agent proxies to `nova-metadata:8775`, which cannot reach the cell1
RabbitMQ because RabbitMQ's readiness probe times out
(`rabbitmq-diagnostics` exceeding its 20s limit), so Kubernetes drops it from
the Service endpoints and callers get ECONNREFUSED. RabbitMQ itself is healthy
and authenticating clients.

The underlying cause is CPU exhaustion: the Proxmox host runs at load ~42 on 40
CPUs with 6-9% steal inside the guests. The same pressure produced a Nova
`MessagingTimeout` during an amphora build.

**Workaround that works today:** boot with `config_drive: true`. User-data is
then delivered via an attached config drive and the metadata service is not
involved. This was verified — a cirros instance booted with config drive ran
its user-data and served traffic, where the same instance without it failed
20/20 metadata attempts. CAPO supports config drive, so CAPI clusters should
set it.

The real fix is capacity. Nested KVM on a box that is already saturated will
keep producing timeouts, and a CAPI cluster build is far more demanding than a
cirros instance.
