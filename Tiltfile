# vg-collect dev loop. Cluster-agnostic: add your context to the list.
allow_k8s_contexts(['docker-desktop', 'kind-kind', 'k3d-vg-collect', 'minikube'])

# ----- preflights (fail fast with actionable messages) -----
local_resource(
    'preflight-env',
    cmd='test -f .env || (echo ".env missing; run: task bootstrap" && exit 1)',
    labels=['preflight'],
)
local_resource(
    'preflight-platform',
    cmd=' '.join([
        'kubectl get clusterissuer vg-ca >/dev/null 2>&1 &&',
        'kubectl get crd clustersecretstores.external-secrets.io >/dev/null 2>&1 ||',
        '(echo "platform not installed; run: task bootstrap:cluster" && exit 1)',
    ]),
    labels=['preflight'],
)

# ----- namespace -----
k8s_yaml(encode_yaml({
    'apiVersion': 'v1', 'kind': 'Namespace',
    'metadata': {'name': 'vg-collect'},
}))

# ----- ESO fake ClusterSecretStore rendered from .env -----
def env_pairs():
    pairs = {}
    for line in str(read_file('.env')).splitlines():
        line = line.strip()
        if not line or line.startswith('#') or '=' not in line:
            continue
        k, v = line.split('=', 1)
        pairs[k.strip()] = v.strip()
    return pairs

ENV = env_pairs()

# .env var -> ESO remote key. Extend this map as services add secrets.
SECRET_KEYS = {
    'user/pg-password': ENV.get('PG_USER_PASSWORD', ''),
}

k8s_yaml(encode_yaml({
    'apiVersion': 'external-secrets.io/v1beta1',
    'kind': 'ClusterSecretStore',
    'metadata': {'name': 'vg-fake'},
    'spec': {'provider': {'fake': {'data': [
        {'key': k, 'value': v} for k, v in SECRET_KEYS.items()
    ]}}},
}))
k8s_resource(
    new_name='secret-store',
    objects=['vg-fake:ClusterSecretStore'],
    resource_deps=['preflight-platform', 'preflight-env'],
    labels=['platform'],
)

# ----- user service -----
docker_build(
    'vg-collect/user', '.',
    dockerfile='services/user/Dockerfile',
    only=['libs/go', 'services/user'],
)
k8s_yaml(helm('deploy/charts/user', name='user', namespace='vg-collect'))
k8s_resource('user', port_forwards=['8081:8080'],
             resource_deps=['secret-store', 'user-pg'], labels=['services'])
k8s_resource('user-pg', port_forwards=['5433:5432'], labels=['datastores'])
