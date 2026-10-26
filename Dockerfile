# syntax=docker/dockerfile:1

ARG GO_VERSION=1.26

FROM --platform=$BUILDPLATFORM golang:${GO_VERSION}-alpine AS build

ARG TARGETOS
ARG TARGETARCH
ARG VERSION=dev

WORKDIR /src

# Cache module downloads separately from the source code.
COPY go.mod go.sum ./
RUN go mod download

COPY . .

RUN CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} \
    go build -trimpath -ldflags "-s -w -X main.version=${VERSION}" \
    -o /out/prometheus-marstek-exporter .

FROM cgr.dev/chainguard/static:latest

COPY --from=build /out/prometheus-marstek-exporter /usr/bin/prometheus-marstek-exporter

USER 65532:65532

EXPOSE 9141

ENTRYPOINT ["/usr/bin/prometheus-marstek-exporter"]
