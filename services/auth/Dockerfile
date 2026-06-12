FROM golang:1.26-alpine AS build
WORKDIR /src
ENV GOWORK=off CGO_ENABLED=0
COPY libs/go/ libs/go/
COPY services/auth/ services/auth/
RUN cd services/auth && go mod download
RUN cd services/auth && go build -ldflags='-s -w' -o /out/auth ./cmd/auth
FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/auth /auth
USER nonroot
ENTRYPOINT ["/auth"]
