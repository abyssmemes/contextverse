# Helm chart — contextd (template)

> **WIP / no auto-deploy.** Chart templates only. There is **no** CI job to package the chart to an OCI registry or deploy to a cluster yet.

## Prerequisites

1. Build a local image (see [`../../docker/`](../../docker/)):

```bash
docker build -f deploy/docker/Dockerfile -t contextd:local .
# kind/minikube: load the image into the cluster
kind load docker-image contextd:local
```

2. Helm 3.x

## Install

```bash
helm upgrade --install contextd ./deploy/helm/contextd \
  --namespace contextverse --create-namespace \
  --set image.repository=contextd \
  --set image.tag=local \
  --set image.pullPolicy=IfNotPresent
```

```bash
kubectl -n contextverse port-forward svc/contextd 8743:8743
curl -sf http://127.0.0.1:8743/health
```

## Important limits (for now)

- Default **`replicaCount: 1`** with a ReadWriteOnce PVC and **local** backend.
- For HA: use [`values-ha-s3.yaml`](./values-ha-s3.yaml) (`replicaCount: 2`, `persistence.enabled: false`) and point `backend` at shared S3/SQL/git. See also Compose lab [`../../compose/ha-minio/`](../../compose/ha-minio/).
- Ingress is **off** by default (`ingress.enabled: false`).
- Image tags like `contextd:local` must already exist in the cluster — nothing pulls from GHCR yet.

## Values

See [`values.yaml`](./values.yaml) and [`values-ha-s3.yaml`](./values-ha-s3.yaml). Override listen/admin/space via `env.*`.
