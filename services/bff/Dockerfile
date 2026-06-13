FROM node:22-alpine AS frontend
WORKDIR /src/frontend
COPY frontend/package.json frontend/package-lock.json ./
RUN npm ci
COPY frontend/ ./
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
