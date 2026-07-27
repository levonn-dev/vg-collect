FROM golang:1.26-alpine AS build
WORKDIR /src
ENV GOWORK=off CGO_ENABLED=0
COPY libs/go/ libs/go/
COPY services/social/ services/social/
RUN cd services/social && go mod download
RUN cd services/social && go build -ldflags='-s -w' -o /out/social ./cmd/social

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/social /social
USER nonroot
ENTRYPOINT ["/social"]
