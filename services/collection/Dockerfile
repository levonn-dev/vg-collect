FROM golang:1.26-alpine AS build
WORKDIR /src
ENV GOWORK=off CGO_ENABLED=0
COPY libs/go/ libs/go/
COPY services/collection/ services/collection/
RUN cd services/collection && go mod download
RUN cd services/collection && go build -ldflags='-s -w' -o /out/collection ./cmd/collection

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/collection /collection
USER nonroot
ENTRYPOINT ["/collection"]
