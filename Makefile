KUBERNETES_VERSION ?= 1.32.0
GIT_SHA            := $(shell git rev-parse --short HEAD)
IMAGE_REPOSITORY   ?= quay.io/lburgazzoli/testcontainers-envtest
IMAGE_TAG          ?= $(GIT_SHA)
IMAGE_REF          := $(IMAGE_REPOSITORY):$(IMAGE_TAG)

.PHONY: image/build
image/build:
	docker build \
		--build-arg ENVTEST_VERSION=$(KUBERNETES_VERSION) \
		-t $(IMAGE_REF) \
		image/

.PHONY: image/push
image/push: image/build
	docker push $(IMAGE_REF)

.PHONY: test
test: image/push
	ENVTEST_IMAGE_REPOSITORY=$(IMAGE_REPOSITORY) \
	ENVTEST_KUBERNETES_VERSION=$(IMAGE_TAG) \
	TESTCONTAINERS_RYUK_DISABLED=true \
	go test -v -count=1 -timeout=300s ./pkg/envtest/

.PHONY: build
build:
	go build ./...

.PHONY: lint
lint:
	go vet ./...
