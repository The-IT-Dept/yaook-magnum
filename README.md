# yaook-magnum

A Kubernetes operator that deploys **OpenStack Magnum** (Container Infrastructure
Management) onto a [Yaook](https://yaook.cloud)-managed OpenStack control plane.

Yaook ships operators for Keystone, Glance, Nova, Neutron, Cinder, Octavia, Heat
and others, but as of Yaook **3.3.0** there is no Magnum operator. This project
fills that gap while reusing Yaook's own infrastructure layer rather than
reinventing it.

## How it works

`MagnumDeployment` is reconciled into the resources below. Anything Yaook already
solves is delegated to Yaook's operators:

| Concern | Delegated to |
|---|---|
| MariaDB (Galera) + DB user | `infra.yaook.cloud/v1` `MySQLService`, `MySQLUser` |
| RabbitMQ + MQ user | `infra.yaook.cloud/v1` `AMQPServer`, `AMQPUser` |
| Keystone service account | `yaook.cloud/v1` `KeystoneUser` |
| Service catalog entry | `yaook.cloud/v1` `KeystoneEndpoint` |
| Internal PKI | cert-manager via `issuerRef` |

The operator itself owns only what is Magnum-specific: the rendered
`magnum.conf`, the CA bundle, the `magnum-api` and `magnum-conductor`
Deployments, the Service and the Ingress.

Reconcile order matters in two places:

- The **schema migration** (`magnum-db-manage upgrade`) runs as an init container
  on `magnum-api`, and the **conductor is only created once the API reports
  Available**. That keeps alembic single-writer without a distributed lock.
- The **KeystoneEndpoint is registered last**, so Magnum only appears in the
  service catalog once it can actually serve requests.

### Credential handling

The operator generates the DB and MQ passwords into Secrets that the Yaook
infra-operator consumes. Keystone service credentials flow the other way: the
`KeystoneUser` controller creates a Secret it owns, named
`<keystoneuser>-idcreds-<random>`. That name is not published in the resource's
status, so the operator locates it by matching the Secret's `ownerReferences`
back to the `KeystoneUser` UID.

Values in `spec.magnumConfig` are merged into the generated config, but
credentials and connection strings are re-asserted afterwards so a bad override
cannot break authentication.

## Install

```bash
kubectl apply -f config/crd/bases/
kubectl apply -f deploy/operator.yaml
kubectl apply -f examples/magnumdeployment.yaml
```

The operator expects an existing Yaook deployment in the target namespace: a
`KeystoneDeployment`, a cert-manager `Issuer`, and the internal CA Secret
(`yaook-internal-ca`).

## Configuration

| Field | Description |
|---|---|
| `keystoneRef.name` | The `KeystoneDeployment` to authenticate against |
| `issuerRef.name` | cert-manager Issuer used for internal TLS |
| `region.name` | OpenStack region for the catalog entry |
| `database` / `messageQueue` | Sizing and storage class for MariaDB / RabbitMQ |
| `api.ingress.fqdn` / `.port` | Public endpoint; the port is used to build the catalog URL |
| `api.ingress.externalCertificateSecretRef` | Serve the Ingress with an existing TLS Secret (e.g. an ACME cert) |
| `images.api` / `images.conductor` | Override container images |
| `magnumConfig` | Extra `magnum.conf` sections merged over the generated config |

## Images

Yaook's registry publishes no Magnum images, so the defaults are the OpenStack
Kolla images:

- `quay.io/openstack.kolla/magnum-api:2025.1-ubuntu-noble`
- `quay.io/openstack.kolla/magnum-conductor:2025.1-ubuntu-noble`

These bundle `magnum-cluster-api-proxy`, so the Cluster API driver is available
in-image.

## Known limitations

- **TLS terminates at the Ingress.** The internal endpoints are registered as
  plain HTTP inside the cluster, unlike the upstream Yaook services which run an
  `ssl-terminator` sidecar. Traffic to Keystone, MariaDB and RabbitMQ *is* TLS.
- **No Keystone trustee domain is provisioned.** Magnum needs a dedicated domain
  (`[trust] trustee_domain_name`) before it can create clusters on a user's
  behalf. Yaook exposes no `KeystoneDomain` CRD, so this must currently be
  created out of band.
- Cluster creation additionally requires Octavia, a working external/provider
  network and a suitable Glance image. Those are deployment concerns, not
  operator concerns.

## Development

```bash
make test          # fmt, vet, unit tests
make generate      # regenerate deepcopy, CRD and RBAC
make docker-build IMG=...
```

## Licence

Apache 2.0. See [LICENSE](LICENSE).
