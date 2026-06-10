FROM golang:1.26-alpine AS build
WORKDIR /src
ENV GOWORK=off CGO_ENABLED=0
COPY libs/go/ libs/go/
COPY services/user/ services/user/
RUN cd services/user && go mod download
RUN cd services/user && go build -ldflags='-s -w' -o /out/user ./cmd/user

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/user /user
USER nonroot
ENTRYPOINT ["/user"]
