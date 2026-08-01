FROM node:22-alpine AS frontend
WORKDIR /src/frontend
COPY frontend/package.json frontend/package-lock.json ./
RUN npm ci
COPY frontend/ ./
# Site identity baked into the SPA; unset args fall back in code.
ARG VITE_SITE_NAME
ARG VITE_SITE_OPERATOR
ARG VITE_SITE_CONTACT
ARG VITE_SITE_JURISDICTION
ARG VITE_SITE_SOURCE_URL
ARG VITE_SITE_DATA_SOURCES
ARG VITE_SITE_AUTH_PROVIDERS
ARG VITE_BUILD_VERSION
RUN npm run build

FROM golang:1.26-alpine AS build
WORKDIR /src
ENV GOWORK=off CGO_ENABLED=0
COPY libs/go/ libs/go/
COPY services/bff/ services/bff/
COPY --from=frontend /src/frontend/dist/ services/bff/internal/static/dist/
RUN cd services/bff && go mod download
RUN cd services/bff && go build -ldflags='-s -w' -o /out/bff ./cmd/bff
FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/bff /bff
USER nonroot
ENTRYPOINT ["/bff"]
