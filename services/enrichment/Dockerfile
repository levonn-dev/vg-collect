FROM golang:1.26-alpine AS build
WORKDIR /src
ENV GOWORK=off CGO_ENABLED=0
COPY libs/go/ libs/go/
COPY services/enrichment/ services/enrichment/
RUN cd services/enrichment && go mod download
RUN cd services/enrichment && go build -ldflags='-s -w' -o /out/enrichment ./cmd/enrichment

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/enrichment /enrichment
USER nonroot
ENTRYPOINT ["/enrichment"]
