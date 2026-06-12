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
# Empty values are excluded: the fake store webhook rejects them. The Helm
# chart's providers.*.enabled flags gate the corresponding ExternalSecret
# entries and deployment env vars so nothing tries to read absent keys.
SECRET_KEYS = {k: v for k, v in {
    'user/pg-password': ENV.get('PG_USER_PASSWORD', ''),
    'auth/pg-password': ENV.get('PG_AUTH_PASSWORD', ''),
    'auth/jwt-signing-key': ENV.get('AUTH_JWT_SIGNING_KEY', ''),
    'auth/google-client-id': ENV.get('GOOGLE_CLIENT_ID', ''),
    'auth/google-client-secret': ENV.get('GOOGLE_CLIENT_SECRET', ''),
    'auth/twitch-client-id': ENV.get('TWITCH_CLIENT_ID', ''),
    'auth/twitch-client-secret': ENV.get('TWITCH_CLIENT_SECRET', ''),
}.items() if v != ''}

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

# ----- auth service -----
docker_build(
    'vg-collect/auth', '.',
    dockerfile='services/auth/Dockerfile',
    only=['libs/go', 'services/auth'],
)
# A provider needs BOTH halves of its credential pair (mirrors the auth
# service's own enablement rule); enabling on a half-configured pair
# would point the ExternalSecret at store keys the empty-value filter
# above never published, wedging the sync.
_auth_set = []
if ENV.get('GOOGLE_CLIENT_ID', '') != '' and ENV.get('GOOGLE_CLIENT_SECRET', '') != '':
    _auth_set.append('providers.google.enabled=true')
if ENV.get('TWITCH_CLIENT_ID', '') != '' and ENV.get('TWITCH_CLIENT_SECRET', '') != '':
    _auth_set.append('providers.twitch.enabled=true')
k8s_yaml(helm('deploy/charts/auth', name='auth', namespace='vg-collect', set=_auth_set))
k8s_resource('auth', port_forwards=['8082:8080'],
             resource_deps=['secret-store', 'auth-pg', 'user'], labels=['services'])
k8s_resource('auth-pg', port_forwards=['5434:5432'], labels=['datastores'])
