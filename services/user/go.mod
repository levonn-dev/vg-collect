module github.com/levonn-dev/vgkeep/services/user

go 1.26.2

replace github.com/levonn-dev/vgkeep/libs/go/config => ../../libs/go/config

replace github.com/levonn-dev/vgkeep/libs/go/contract => ../../libs/go/contract

replace github.com/levonn-dev/vgkeep/libs/go/ctrtest => ../../libs/go/ctrtest

replace github.com/levonn-dev/vgkeep/libs/go/httpkit => ../../libs/go/httpkit

replace github.com/levonn-dev/vgkeep/libs/go/jwtauth => ../../libs/go/jwtauth

replace github.com/levonn-dev/vgkeep/libs/go/jwtauthtest => ../../libs/go/jwtauthtest

replace github.com/levonn-dev/vgkeep/libs/go/metrictest => ../../libs/go/metrictest

replace github.com/levonn-dev/vgkeep/libs/go/otel => ../../libs/go/otel

replace github.com/levonn-dev/vgkeep/libs/go/pgkit => ../../libs/go/pgkit

replace github.com/levonn-dev/vgkeep/libs/go/pgtest => ../../libs/go/pgtest

replace github.com/levonn-dev/vgkeep/libs/go/reqtest => ../../libs/go/reqtest

replace github.com/levonn-dev/vgkeep/libs/go/specval => ../../libs/go/specval

tool github.com/oapi-codegen/oapi-codegen/v2/cmd/oapi-codegen

require (
	github.com/getkin/kin-openapi v0.149.0
	github.com/golang-migrate/migrate/v4 v4.19.1
	github.com/google/uuid v1.6.0
	github.com/jackc/pgerrcode v0.0.0-20250907135507-afb5586c32a6
	github.com/jackc/pgx/v5 v5.10.0
	github.com/levonn-dev/vgkeep/libs/go/config v0.0.0-00010101000000-000000000000
	github.com/levonn-dev/vgkeep/libs/go/contract v0.0.0-00010101000000-000000000000
	github.com/levonn-dev/vgkeep/libs/go/httpkit v0.0.0
	github.com/levonn-dev/vgkeep/libs/go/jwtauth v0.0.0-00010101000000-000000000000
	github.com/levonn-dev/vgkeep/libs/go/jwtauthtest v0.0.0-00010101000000-000000000000
	github.com/levonn-dev/vgkeep/libs/go/metrictest v0.0.0-00010101000000-000000000000
	github.com/levonn-dev/vgkeep/libs/go/otel v0.0.0-00010101000000-000000000000
	github.com/levonn-dev/vgkeep/libs/go/pgkit v0.0.0-00010101000000-000000000000
	github.com/levonn-dev/vgkeep/libs/go/pgtest v0.0.0-00010101000000-000000000000
	github.com/levonn-dev/vgkeep/libs/go/reqtest v0.0.0-00010101000000-000000000000
	github.com/levonn-dev/vgkeep/libs/go/specval v0.0.0-00010101000000-000000000000
	github.com/oapi-codegen/runtime v1.7.0
	go.opentelemetry.io/otel v1.46.0
	go.opentelemetry.io/otel/metric v1.46.0
	go.opentelemetry.io/otel/sdk/metric v1.46.0
	golang.org/x/text v0.41.0
)

require (
	dario.cat/mergo v1.0.2 // indirect
	github.com/Azure/go-ansiterm v0.0.0-20250102033503-faa5f7b0171c // indirect
	github.com/Microsoft/go-winio v0.6.2 // indirect
	github.com/apapsch/go-jsonmerge/v2 v2.0.0 // indirect
	github.com/caarlos0/env/v11 v11.4.1 // indirect
	github.com/cenkalti/backoff/v4 v4.3.0 // indirect
	github.com/cenkalti/backoff/v5 v5.0.3 // indirect
	github.com/cespare/xxhash/v2 v2.3.0 // indirect
	github.com/containerd/errdefs v1.0.0 // indirect
	github.com/containerd/errdefs/pkg v0.3.0 // indirect
	github.com/containerd/log v0.1.0 // indirect
	github.com/containerd/platforms v0.2.1 // indirect
	github.com/cpuguy83/dockercfg v0.3.2 // indirect
	github.com/distribution/reference v0.6.0 // indirect
	github.com/docker/go-connections v0.7.0 // indirect
	github.com/docker/go-units v0.5.0 // indirect
	github.com/dprotaso/go-yit v0.0.0-20220510233725-9ba8df137936 // indirect
	github.com/ebitengine/purego v0.10.1 // indirect
	github.com/exaring/otelpgx v0.11.1 // indirect
	github.com/felixge/httpsnoop v1.1.0 // indirect
	github.com/go-logr/logr v1.4.4 // indirect
	github.com/go-logr/stdr v1.2.2 // indirect
	github.com/go-ole/go-ole v1.3.0 // indirect
	github.com/go-openapi/jsonpointer v1.0.0 // indirect
	github.com/golang-jwt/jwt/v5 v5.3.1 // indirect
	github.com/gorilla/mux v1.8.1 // indirect
	github.com/grpc-ecosystem/grpc-gateway/v2 v2.30.0 // indirect
	github.com/jackc/pgpassfile v1.0.0 // indirect
	github.com/jackc/pgservicefile v0.0.0-20240606120523-5a60cdf6a761 // indirect
	github.com/jackc/puddle/v2 v2.2.2 // indirect
	github.com/klauspost/compress v1.18.7 // indirect
	github.com/levonn-dev/vgkeep/libs/go/ctrtest v0.0.0-00010101000000-000000000000 // indirect
	github.com/lufia/plan9stats v0.0.0-20260330125221-c963978e514e // indirect
	github.com/magiconair/properties v1.8.10 // indirect
	github.com/moby/docker-image-spec v1.3.1 // indirect
	github.com/moby/go-archive v0.2.0 // indirect
	github.com/moby/moby/api v1.55.0 // indirect
	github.com/moby/moby/client v0.5.0 // indirect
	github.com/moby/patternmatcher v0.6.1 // indirect
	github.com/moby/sys/sequential v0.7.0 // indirect
	github.com/moby/sys/user v0.4.1 // indirect
	github.com/moby/sys/userns v0.1.0 // indirect
	github.com/moby/term v0.5.2 // indirect
	github.com/oapi-codegen/nethttp-middleware v1.2.0 // indirect
	github.com/oapi-codegen/oapi-codegen/v2 v2.8.0 // indirect
	github.com/oasdiff/yaml v0.1.1 // indirect
	github.com/oasdiff/yaml3 v0.0.14 // indirect
	github.com/onsi/gomega v1.27.6 // indirect
	github.com/opencontainers/go-digest v1.0.0 // indirect
	github.com/opencontainers/image-spec v1.1.1 // indirect
	github.com/power-devops/perfstat v0.0.0-20240221224432-82ca36839d55 // indirect
	github.com/santhosh-tekuri/jsonschema/v6 v6.0.3 // indirect
	github.com/shirou/gopsutil/v4 v4.26.6 // indirect
	github.com/sirupsen/logrus v1.9.4 // indirect
	github.com/speakeasy-api/jsonpath v0.6.3 // indirect
	github.com/speakeasy-api/openapi v1.24.0 // indirect
	github.com/stretchr/testify v1.12.1 // indirect
	github.com/testcontainers/testcontainers-go v0.44.0 // indirect
	github.com/testcontainers/testcontainers-go/modules/postgres v0.44.0 // indirect
	github.com/tklauser/go-sysconf v0.4.0 // indirect
	github.com/tklauser/numcpus v0.12.0 // indirect
	github.com/vmware-labs/yaml-jsonpath v0.3.2 // indirect
	github.com/yusufpapurcu/wmi v1.2.4 // indirect
	go.opentelemetry.io/auto/sdk v1.2.1 // indirect
	go.opentelemetry.io/contrib/bridges/otelslog v0.20.1 // indirect
	go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp v0.71.0 // indirect
	go.opentelemetry.io/contrib/instrumentation/runtime v0.71.0 // indirect
	go.opentelemetry.io/otel/exporters/otlp/otlplog/otlploggrpc v0.22.0 // indirect
	go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetricgrpc v1.46.0 // indirect
	go.opentelemetry.io/otel/exporters/otlp/otlptrace v1.46.0 // indirect
	go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc v1.46.0 // indirect
	go.opentelemetry.io/otel/log v0.22.0 // indirect
	go.opentelemetry.io/otel/sdk v1.46.0 // indirect
	go.opentelemetry.io/otel/sdk/log v0.22.0 // indirect
	go.opentelemetry.io/otel/trace v1.46.0 // indirect
	go.opentelemetry.io/proto/otlp v1.11.0 // indirect
	go.yaml.in/yaml/v3 v3.0.5 // indirect
	golang.org/x/crypto v0.55.0 // indirect
	golang.org/x/mod v0.38.0 // indirect
	golang.org/x/net v0.58.0 // indirect
	golang.org/x/sync v0.22.0 // indirect
	golang.org/x/sys v0.47.0 // indirect
	golang.org/x/tools v0.48.0 // indirect
	google.golang.org/genproto/googleapis/api v0.0.0-20260819154853-08b0e4226688 // indirect
	google.golang.org/genproto/googleapis/rpc v0.0.0-20260819154853-08b0e4226688 // indirect
	google.golang.org/grpc v1.83.1 // indirect
	google.golang.org/protobuf v1.36.12 // indirect
	gopkg.in/yaml.v3 v3.0.1 // indirect
)
