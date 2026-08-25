# vgkeep dev loop. Cluster-agnostic: add your context to the list.
allow_k8s_contexts(['docker-desktop', 'kind-kind', 'k3d-vgkeep', 'minikube'])

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
        'kubectl get crd clustersecretstores.external-secrets.io >/dev/null 2>&1 &&',
        'kubectl get crd apisixroutes.apisix.apache.org >/dev/null 2>&1 ||',
        '(echo "platform not installed or outdated; run: task bootstrap:cluster" && exit 1)',
    ]),
    labels=['preflight'],
)

# ----- vg-lib (shared helm library chart) -----
# Each service chart vendors vg-lib via helm dependency build; re-vendor
# when the library changes so helm() below renders current templates.
listdir('deploy/charts/vg-lib', recursive=True)
local('task helm:deps')

# ----- namespace -----
k8s_yaml(encode_yaml({
    'apiVersion': 'v1', 'kind': 'Namespace',
    'metadata': {'name': 'vgkeep'},
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
# Empty values are excluded (webhook rejects them); providers.*.enabled
# flags then gate ExternalSecret/env entries against absent keys.
SECRET_KEYS = {k: v for k, v in {
    'user/pg-password': ENV.get('PG_USER_PASSWORD', ''),
    'auth/pg-password': ENV.get('PG_AUTH_PASSWORD', ''),
    'auth/jwt-signing-key': ENV.get('AUTH_JWT_SIGNING_KEY', ''),
    'auth/google-client-id': ENV.get('GOOGLE_CLIENT_ID', ''),
    'auth/google-client-secret': ENV.get('GOOGLE_CLIENT_SECRET', ''),
    'auth/twitch-client-id': ENV.get('TWITCH_CLIENT_ID', ''),
    'auth/twitch-client-secret': ENV.get('TWITCH_CLIENT_SECRET', ''),
    # The catalog-refresh and entry-rematch CronJobs' shared bootstrap
    # secret (POST /internal/service-token); auth is its only consumer.
    'auth/internal-service-token': ENV.get('AUTH_INTERNAL_SERVICE_TOKEN', ''),
    'auth/internal-service-token-previous': ENV.get('AUTH_INTERNAL_SERVICE_TOKEN_PREVIOUS', ''),
    'bff/cookie-key': ENV.get('BFF_COOKIE_KEY', ''),
    'enrichment/mongo-password': ENV.get('MONGO_ENRICHMENT_PASSWORD', ''),
    'enrichment/igdb-client-id': ENV.get('IGDB_CLIENT_ID', ''),
    'enrichment/igdb-client-secret': ENV.get('IGDB_CLIENT_SECRET', ''),
    'enrichment/pricecharting-api-key': ENV.get('PRICECHARTING_API_KEY', ''),
    'collection/pg-password': ENV.get('PG_COLLECTION_PASSWORD', ''),
    'social/pg-password': ENV.get('PG_SOCIAL_PASSWORD', ''),
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
    'vgkeep/user', '.',
    dockerfile='services/user/Dockerfile',
    only=['libs/go', 'services/user'],
)
k8s_yaml(helm('deploy/charts/user', name='user', namespace='vgkeep',
              set=['env.handleChangeCooldown=5s']))
k8s_resource('user', port_forwards=['8081:8080'],
             resource_deps=['secret-store', 'user-pg'], labels=['services'])
k8s_resource('user-pg', port_forwards=['5433:5432'], labels=['datastores'])

# ----- auth service -----
docker_build(
    'vgkeep/auth', '.',
    dockerfile='services/auth/Dockerfile',
    only=['libs/go', 'services/auth'],
)
# A provider needs BOTH credential halves (mirrors auth's own enablement
# rule); a half-configured pair would wedge the ExternalSecret sync.
_auth_set = []
if ENV.get('GOOGLE_CLIENT_ID', '') != '' and ENV.get('GOOGLE_CLIENT_SECRET', '') != '':
    _auth_set.append('providers.google.enabled=true')
if ENV.get('TWITCH_CLIENT_ID', '') != '' and ENV.get('TWITCH_CLIENT_SECRET', '') != '':
    _auth_set.append('providers.twitch.enabled=true')
if _auth_set:
    _auth_set.append('env.oauthRedirectUrl=' + ENV.get('OAUTH_REDIRECT_URL', 'http://localhost:8090/api/auth/callback'))
# A staged previous value in .env means a rotation is in flight, so
# tell the chart to accept it alongside the current one.
if ENV.get('AUTH_INTERNAL_SERVICE_TOKEN_PREVIOUS', '') != '':
    _auth_set.append('previousTokenEnabled=true')
k8s_yaml(helm('deploy/charts/auth', name='auth', namespace='vgkeep', set=_auth_set))
k8s_resource('auth', port_forwards=['8082:8080'],
             resource_deps=['secret-store', 'auth-pg', 'user'], labels=['services'])
k8s_resource('auth-pg', port_forwards=['5434:5432'], labels=['datastores'])

# ----- edge -----
# The gateway is the browser's entrypoint; everything else stays
# cluster-internal. kubectl port-forward keeps this cluster-agnostic.
local_resource(
    'gateway',
    serve_cmd='kubectl port-forward -n vg-platform svc/vg-platform-apisix-gateway 8090:80',
    resource_deps=['preflight-platform'],
    labels=['platform'],
)

# ----- observability UIs (platform-owned; installed by bootstrap:cluster) -----
local_resource(
    'grafana',
    serve_cmd='kubectl port-forward -n vg-platform svc/grafana 3000:80',
    resource_deps=['preflight-platform'], labels=['platform'],
)
local_resource(
    'prometheus',
    serve_cmd='kubectl port-forward -n vg-platform svc/kps-prometheus 9090:9090',
    resource_deps=['preflight-platform'], labels=['platform'],
)
local_resource(
    'jaeger',
    serve_cmd='kubectl port-forward -n vg-platform svc/jaeger 16686:16686',
    resource_deps=['preflight-platform'], labels=['platform'],
)

# ----- bff service -----
# Frontend site-identity build args: the two CSV lists mirror the backend
# enablement blocks (a provider appears in credits/legal text only when
# its backend half runs real). Explicit VITE_SITE_* values in .env win.
_site_args = {k: v for k, v in ENV.items() if k.startswith('VITE_SITE_')}
if 'VITE_SITE_DATA_SOURCES' not in ENV:
    _sources = []
    if ENV.get('IGDB_CLIENT_ID', '') != '' and ENV.get('IGDB_CLIENT_SECRET', '') != '':
        _sources.append('igdb')
    if ENV.get('PRICECHARTING_API_KEY', '') != '':
        _sources.append('pricecharting')
    # fx is credential-less and the chart defaults fx.mode=real.
    _sources.append('frankfurter')
    _site_args['VITE_SITE_DATA_SOURCES'] = ','.join(_sources)
if 'VITE_SITE_AUTH_PROVIDERS' not in ENV:
    _providers = []
    if ENV.get('GOOGLE_CLIENT_ID', '') != '' and ENV.get('GOOGLE_CLIENT_SECRET', '') != '':
        _providers.append('google')
    if ENV.get('TWITCH_CLIENT_ID', '') != '' and ENV.get('TWITCH_CLIENT_SECRET', '') != '':
        _providers.append('twitch')
    _site_args['VITE_SITE_AUTH_PROVIDERS'] = ','.join(_providers)
# VITE_BUILD_VERSION is explicit-only: Tilt never derives a git SHA, so
# an unset value stays absent, same as every VITE_SITE_* arg above.
if 'VITE_BUILD_VERSION' in ENV:
    _site_args['VITE_BUILD_VERSION'] = ENV['VITE_BUILD_VERSION']
docker_build(
    'vgkeep/bff', '.',
    dockerfile='services/bff/Dockerfile',
    only=['libs/go', 'services/bff', 'frontend'],
    ignore=['frontend/node_modules', 'frontend/dist', 'frontend/playwright-report', 'frontend/test-results'],
    build_args=_site_args,
)
k8s_yaml(helm('deploy/charts/bff', name='bff', namespace='vgkeep'))
k8s_resource('bff', port_forwards=['8083:8080'],
             resource_deps=['secret-store', 'bff-valkey', 'auth', 'collection'], labels=['services'])
k8s_resource('bff-valkey', labels=['datastores'])

# ----- enrichment service -----
docker_build(
    'vgkeep/enrichment', '.',
    dockerfile='services/enrichment/Dockerfile',
    only=['libs/go', 'services/enrichment'],
)
# Provider modes flip to real only with the full credential set in .env
# (mirrors auth's flags); a partial set would wedge the ExternalSecret sync.
_enrichment_set = []
if ENV.get('IGDB_CLIENT_ID', '') != '' and ENV.get('IGDB_CLIENT_SECRET', '') != '':
    _enrichment_set.append('igdb.mode=real')
if ENV.get('PRICECHARTING_API_KEY', '') != '':
    _enrichment_set.append('pricecharting.mode=real')
k8s_yaml(helm('deploy/charts/enrichment', name='enrichment', namespace='vgkeep', set=_enrichment_set))
k8s_resource('enrichment', port_forwards=['8084:8080'],
             resource_deps=['secret-store', 'enrichment-mongo', 'enrichment-valkey', 'auth'], labels=['services'])
k8s_resource('enrichment-mongo', port_forwards=['27018:27017'], labels=['datastores'])
k8s_resource('enrichment-valkey', labels=['datastores'])
k8s_resource('enrichment-refresh', resource_deps=['enrichment'], labels=['services'])

# ----- collection service -----
docker_build(
    'vgkeep/collection', '.',
    dockerfile='services/collection/Dockerfile',
    only=['libs/go', 'services/collection'],
)
k8s_yaml(helm('deploy/charts/collection', name='collection', namespace='vgkeep'))
k8s_resource('collection', port_forwards=['8085:8080'],
             resource_deps=['secret-store', 'collection-pg', 'collection-valkey', 'auth', 'enrichment'],
             labels=['services'])
k8s_resource('collection-pg', port_forwards=['5435:5432'], labels=['datastores'])
k8s_resource('collection-valkey', labels=['datastores'])
k8s_resource('collection-rematch', resource_deps=['collection'], labels=['services'])

# ----- social service -----
docker_build(
    'vgkeep/social', '.',
    dockerfile='services/social/Dockerfile',
    only=['libs/go', 'services/social'],
)
k8s_yaml(helm('deploy/charts/social', name='social', namespace='vgkeep'))
k8s_resource('social', port_forwards=['8086:8080'],
             resource_deps=['secret-store', 'social-pg', 'auth', 'user', 'collection'],
             labels=['services'])
k8s_resource('social-pg', port_forwards=['5436:5432'], labels=['datastores'])

# ----- frontend dev loop (manual: trigger when iterating on the SPA;
# the in-cluster bff serves the built bundle either way) -----
local_resource(
    'frontend-dev',
    cmd='test -d node_modules || npm ci',
    serve_cmd='npm run dev',
    dir='frontend',
    serve_dir='frontend',
    resource_deps=['gateway'],
    auto_init=False,
    trigger_mode=TRIGGER_MODE_MANUAL,
    labels=['frontend'],
)
