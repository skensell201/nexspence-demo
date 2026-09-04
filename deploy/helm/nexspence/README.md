# Nexspence Helm Chart

Nexspence — open-source universal artifact repository manager (Nexus OSS alternative).

## Requirements

- Helm 3.x
- Kubernetes >= 1.26
- PersistentVolume provisioner (for local blob storage) or S3-compatible storage

---

## Install from GHCR (OCI) — recommended

Each release publishes the chart as an OCI artifact to GitHub Packages, so you can
install it directly without downloading anything:

```bash
helm install nexspence oci://ghcr.io/nexspence/charts/nexspence \
  --version 1.19.2 \
  -f https://raw.githubusercontent.com/nexspence/nexspence/main/deploy/helm/nexspence/values-examples/nginx.yaml \
  --namespace nexspence --create-namespace
```

`config.jwtSecret` is auto-generated when omitted (see the JWT note below). Pin a
specific chart version with `--version` (omit it to pull the latest). Browse versions:
**[ghcr.io/nexspence/charts/nexspence](https://github.com/nexspence/nexspence/pkgs/container/charts%2Fnexspence)**.

---

## Install from a release bundle

Download the `nexspence-run-essentials-vX.Y.Z.zip` from the latest release and extract it:
**[github.com/nexspence/nexspence/releases](https://github.com/nexspence/nexspence/releases)**

The Helm chart is at `deploy/helm/nexspence/` inside the extracted directory.

Then install with exactly one of the networking options below. The chart
bundles a single-replica PostgreSQL from the official `postgres` image — no
sub-chart download is required.

> **JWT secret:** `config.jwtSecret` is optional. When omitted, the chart auto-generates a unique random secret on first install and reuses it across upgrades (via a `lookup` of the existing Secret). The `--set config.jwtSecret=...` in the examples below is only needed to pin a known value or share the secret across clusters.

### nginx ingress-controller

```bash
helm install nexspence \
  deploy/helm/nexspence \
  -f deploy/helm/nexspence/values-examples/nginx.yaml \
  --set config.jwtSecret="$(openssl rand -hex 32)" \
  --set config.adminPassword="changeme" \
  --namespace nexspence \
  --create-namespace
```

### Traefik (HTTPS via websecure entrypoint)

```bash
# TLS secret: nexspence-tls
helm install nexspence \
  deploy/helm/nexspence \
  -f deploy/helm/nexspence/values-examples/traefik.yaml \
  --set config.jwtSecret="$(openssl rand -hex 32)" \
  --set config.adminPassword="changeme" \
  --namespace nexspence \
  --create-namespace
```

### Cilium ingress-controller (>= 1.12)

```bash
# Requires: Cilium >= 1.12 with ingress controller enabled
helm install nexspence \
  deploy/helm/nexspence \
  -f deploy/helm/nexspence/values-examples/cilium-ingress.yaml \
  --set config.jwtSecret="$(openssl rand -hex 32)" \
  --set config.adminPassword="changeme" \
  --namespace nexspence \
  --create-namespace
```

### Istio Gateway + VirtualService

```bash
# Requires: istioctl install --set profile=default
helm install nexspence \
  deploy/helm/nexspence \
  -f deploy/helm/nexspence/values-examples/istio-gateway.yaml \
  --set config.jwtSecret="$(openssl rand -hex 32)" \
  --set config.adminPassword="changeme" \
  --namespace nexspence \
  --create-namespace
```

### Cilium K8s Gateway API (>= 1.14)

```bash
# Requires: Cilium >= 1.14, Gateway API CRDs installed
helm install nexspence \
  deploy/helm/nexspence \
  -f deploy/helm/nexspence/values-examples/cilium-gateway.yaml \
  --set config.jwtSecret="$(openssl rand -hex 32)" \
  --set config.adminPassword="changeme" \
  --namespace nexspence \
  --create-namespace
```

---

## External PostgreSQL

The bundled database is a single-replica convenience instance (official
`docker.io/library/postgres` image). It is not highly available and ships
without replication, backups, or a PodDisruptionBudget. For production,
disable it and point at CloudNativePG, RDS, or another managed Postgres:

```bash
helm install nexspence \
  deploy/helm/nexspence \
  --set postgresql.enabled=false \
  --set externalDatabase.dsn="postgres://user:pass@pg-host:5432/nexspence" \
  -f deploy/helm/nexspence/values-examples/nginx.yaml \
  --namespace nexspence \
  --create-namespace
```

To keep the DSN out of values, put it in a Secret and set
`externalDatabase.existingSecret` (key `dsn` by default, override with
`existingSecretDsnKey`). The bundled chart can likewise take
`postgresql.auth.existingSecret` / `existingSecretPasswordKey` instead of
`postgresql.auth.password`.

### Rotate the bundled PostgreSQL password

`POSTGRES_PASSWORD` is only applied by the official image while `initdb`
initializes an empty data directory. Changing `postgresql.auth.password` or
the referenced Secret later does not change the database role by itself.
Rotate the role first in the running pod:

```bash
kubectl exec -it -n nexspence statefulset/nexspence-postgres -- \
  psql -U nexspence -d postgres
# In psql, use \password nexspence and enter the new password twice.
```

Then update `postgresql.auth.password` with `helm upgrade`, or update the key
in `postgresql.auth.existingSecret`. Helm rolls the application automatically
for the chart-managed password. After changing an existing Secret, restart it
explicitly so the environment variable is refreshed:

```bash
kubectl rollout restart -n nexspence deployment/nexspence
```

---

## S3 / MinIO Blob Store

Set `storage.type=s3` and provide bucket + endpoint. Use this
for any multi-replica deployment — a single `ReadWriteOnce` PVC does not scale
horizontally.

```bash
helm install nexspence \
  deploy/helm/nexspence \
  --set storage.type=s3 \
  --set storage.s3.endpoint="https://minio.example.com" \
  --set storage.s3.bucket="nexspence-blobs" \
  --set storage.s3.accessKey="minio" \
  --set storage.s3.secretKey="minio123" \
  -f deploy/helm/nexspence/values-examples/nginx.yaml \
  --namespace nexspence \
  --create-namespace
```

---

## Docker Subdomain Connector

Serves each Docker repository on its own hostname, so clients can
`docker pull myrepo.example.com/alpine` instead of
`docker pull nexspence.example.com:8081/repository/myrepo/alpine`. See
[docs/docker-subdomain-connector.md](../../../docs/docker-subdomain-connector.md)
for the full picture.

```bash
helm install nexspence \
  deploy/helm/nexspence \
  --set config.docker.subdomainConnector.enabled=true \
  --set config.docker.subdomainConnector.baseDomain="nexspence.example.com" \
  --namespace nexspence
```

Two things the chart cannot do for you:

- **Wildcard DNS.** `*.nexspence.example.com` has to resolve to the ingress.
- **The `Host` header.** The connector routes on it, so the ingress must pass
  the client's original hostname through rather than rewriting it. Add a
  wildcard host to `ingress.hosts` (and to the TLS certificate) — with nginx
  the default `proxy_set_header Host $host` is already right; Traefik and Istio
  preserve it too.

```yaml
ingress:
  enabled: true
  hosts:
    - host: nexspence.example.com
      paths: [{ path: /, pathType: Prefix }]
    - host: "*.nexspence.example.com"
      paths: [{ path: /, pathType: Prefix }]
```

### Hostname aliases

When a DNS name does not match the repository behind it — a Nexus connector
port being migrated, say — map it explicitly. Any domain works; the hostname
does not have to sit under `baseDomain`:

```yaml
config:
  docker:
    subdomainConnector:
      enabled: true
      baseDomain: "nexspence.example.com"
      aliases:
        docker-hub-proxy.example.com: dockerhub-proxy
        hub.nexspence.example.com: dockerhub-proxy
```

A YAML map has no environment-variable spelling, so setting any alias makes the
chart mount a small config file over the image's `/app/config.yaml`. Every other
setting still arrives as an environment variable, which viper reads last, so
nothing else in the chart changes behaviour.

---

## Scaling (HPA)

```bash
helm install nexspence \
  deploy/helm/nexspence \
  --set autoscaling.enabled=true \
  --set autoscaling.minReplicas=2 \
  --set autoscaling.maxReplicas=10 \
  -f deploy/helm/nexspence/values-examples/nginx.yaml \
  --namespace nexspence \
  --create-namespace
```

For multi-replica deployments, use S3 storage (see above).

---

## Monitoring (Prometheus)

The pod serves `GET /metrics` on the same port as the API, so it requires a
Bearer token by default. Inside a cluster the Service is usually only reachable
by Prometheus anyway, and then the token is one more secret to rotate:

```bash
helm upgrade nexspence deploy/helm/nexspence \
  --set config.metricsPublic=true \
  --namespace nexspence
```

A matching `ServiceMonitor` (the chart does not ship one — port name is `http`):

```yaml
apiVersion: monitoring.coreos.com/v1
kind: ServiceMonitor
metadata:
  name: nexspence
spec:
  selector:
    matchLabels:
      app.kubernetes.io/name: nexspence
  endpoints:
    - port: http
      path: /metrics
      interval: 30s
```

Leaving `config.metricsPublic=false` keeps the token requirement — add an
`authorization:` block pointing at a Secret holding an `nxs_*` token. See
[docs/deployment.md](../../../docs/deployment.md#prometheus).

---

## Image Scanning (Trivy)

The nexspence image contains no scanner. Scanning of Docker and OCI *images*
needs a Trivy binary you supply; package scanning (Maven, npm, PyPI, Cargo)
uses OSV.dev and needs nothing.

```yaml
scanning:
  enabled: true
```

That adds a `trivy-copy` initContainer which copies the binary out of
`aquasec/trivy` into an `emptyDir` shared with the app, and sets
`NEXSPENCE_SCAN_TRIVY_ENABLED` / `NEXSPENCE_SCAN_TRIVY_BIN` for you. Pin the
Trivy version under `scanning.image.tag`, and size the shared volume with
`scanning.volumeSize` (default 300Mi — the binary alone is ~150 MB; the
vulnerability database lands in the existing cache volume).

Check it afterwards: `GET /api/v1/security/scanner` answers `ready` with the
version, or names what is wrong. Full reference: [docs/scanning.md](../../../docs/scanning.md).

---

## Upgrading

```bash
helm upgrade nexspence \
  deploy/helm/nexspence \
  -f your-values.yaml \
  --namespace nexspence
```

Database migrations run automatically on pod start — no manual step needed.

**Leaving the former Bitnami sub-chart:** this chart no longer depends on
Bitnami. The bundled database is now a first-party StatefulSet named
`{release}-postgres` (not `{release}-postgresql`) using
`docker.io/library/postgres:18`. The Bitnami chart 15.5.38 shipped
PostgreSQL **16**; this is a two-major jump, so an in-place upgrade of
`PGDATA` is impossible — use `pg_dump` / `pg_restore`.

A `helm upgrade` of an existing Bitnami install is **not** a valid
migration path. Helm will not patch the old StatefulSet into the new one
(the names differ on purpose, so the Bitnami PVC stays around to dump).
It also will not magically convert the data directory. Do this instead:

1. Dump from the old instance while it is still running:

   ```bash
   kubectl exec -n nexspence statefulset/nexspence-postgresql -- \
     pg_dump -U nexspence -Fc nexspence > nexspence.dump
   ```

2. Uninstall the release, or delete the old Bitnami StatefulSet / Service /
   Secret. Kubernetes keeps volumeClaimTemplates PVCs when the StatefulSet
   goes away, so `data-nexspence-postgresql-0` remains as a dump source.

3. Re-install this chart. The new StatefulSet is `nexspence-postgres`
   (PVC `data-nexspence-postgres-0`). If you previously set Bitnami values
   under `postgresql.primary.persistence`, move them to
   `postgresql.persistence` — the schema rejects the unsupported old keys so
   a storage setting cannot be silently ignored.

4. Restore into the new pod, then let the app start (or restart it if it
   already ran migrations against an empty cluster):

   ```bash
   kubectl exec -i -n nexspence statefulset/nexspence-postgres -- \
     pg_restore -U nexspence -d nexspence --clean --if-exists < nexspence.dump
   ```

Or skip the bundled database and point `externalDatabase.dsn` (or
`externalDatabase.existingSecret`) at CloudNativePG / RDS / your existing
cluster.

**Upgrading to 2.0.0:** the image no longer bundles Trivy. Image scanning
stops until you set `scanning.enabled: true` (see above); everything else
upgrades unchanged, and scan results already in the database stay visible.

---

## Uninstall

```bash
helm uninstall nexspence -n nexspence
```

This removes all chart-managed resources. Persistent volumes are **not**
deleted by default. To also remove data:

```bash
kubectl delete pvc -l app.kubernetes.io/instance=nexspence -n nexspence
```

---

## Values Reference

See `values.yaml` — every key is annotated inline.
