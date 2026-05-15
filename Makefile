.PHONY: build test clean docker-build docker-push release

BINARY_NAME=prometheus-marstek-exporter
IMAGE_REPO?=ghcr.io/loafoe/prometheus-marstek-exporter
VERSION?=$(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
PLATFORMS?=linux/amd64,linux/arm64,linux/arm/v7

build:
	go build -ldflags "-s -w -X main.version=$(VERSION)" -o $(BINARY_NAME) .

test:
	go test -v ./...

clean:
	rm -f $(BINARY_NAME)
	rm -rf dist/

docker-build:
	KO_DOCKER_REPO=$(IMAGE_REPO) ko build --bare --platform=$(PLATFORMS) .

docker-push:
	KO_DOCKER_REPO=$(IMAGE_REPO) ko build --bare --platform=$(PLATFORMS) --push .

release: docker-push
	@echo "Signing image with cosign..."
	cosign sign --yes $(IMAGE_REPO):$(VERSION)

lint:
	golangci-lint run

fmt:
	go fmt ./...

run:
	go run . -device-ip=$(MARSTEK_DEVICE_IP)
