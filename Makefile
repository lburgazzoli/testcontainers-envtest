KUBERNETES_VERSION ?= 1.32.0
GIT_SHA            := $(shell git rev-parse --short HEAD)
IMAGE_REPOSITORY   ?= quay.io/lburgazzoli/testcontainers-envtest
IMAGE_TAG          ?= $(GIT_SHA)
IMAGE_REF          := $(IMAGE_REPOSITORY):$(IMAGE_TAG)

LINT_TIMEOUT := 10m
GOLANGCI_VERSION ?= v2.8.0
GOLANGCI ?= go run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@$(GOLANGCI_VERSION)

## Container Runtime Detection
define configure_container_runtime
	@if [ -n "$$DOCKER_HOST" ]; then \
		case "$$DOCKER_HOST" in \
			*podman*) \
				echo "✓ Using Podman (pre-configured via DOCKER_HOST)"; \
				export TESTCONTAINERS_RYUK_DISABLED=true; \
				;; \
			*) \
				echo "✓ Using Docker (pre-configured via DOCKER_HOST)"; \
				;; \
		esac; \
	elif docker info >/dev/null 2>&1; then \
		echo "✓ Using Docker (auto-detected)"; \
	elif command -v podman >/dev/null 2>&1; then \
		if podman machine inspect >/dev/null 2>&1; then \
			echo "✓ Using Podman (auto-detected via podman machine)"; \
			export DOCKER_HOST="unix://$$(podman machine inspect --format '{{.ConnectionInfo.PodmanSocket.Path}}')"; \
			export TESTCONTAINERS_RYUK_DISABLED=true; \
		elif [ -S "$${XDG_RUNTIME_DIR}/podman/podman.sock" ]; then \
			echo "✓ Using Podman (auto-detected via XDG_RUNTIME_DIR)"; \
			export DOCKER_HOST="unix://$${XDG_RUNTIME_DIR}/podman/podman.sock"; \
			export TESTCONTAINERS_RYUK_DISABLED=true; \
		else \
			echo "ERROR: Podman found but not running."; \
			exit 1; \
		fi; \
	else \
		echo "ERROR: Neither Docker nor Podman is available"; \
		exit 1; \
	fi
endef

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
test:
	@$(configure_container_runtime) && \
	ENVTEST_IMAGE_REPOSITORY=$${ENVTEST_IMAGE_REPOSITORY:-$(IMAGE_REPOSITORY)} \
	ENVTEST_KUBERNETES_VERSION=$${ENVTEST_KUBERNETES_VERSION:-$(IMAGE_TAG)} \
	TESTCONTAINERS_RYUK_DISABLED=$${TESTCONTAINERS_RYUK_DISABLED:-false} \
	go test -v -count=1 -timeout=300s ./pkg/envtest/

.PHONY: build
build:
	go build ./...

.PHONY: lint
lint:
	@$(GOLANGCI) run --config .golangci.yml --timeout $(LINT_TIMEOUT)

.PHONY: lint/fix
lint/fix:
	@$(GOLANGCI) run --config .golangci.yml --timeout $(LINT_TIMEOUT) --fix

.PHONY: fmt
fmt:
	@$(GOLANGCI) fmt --config .golangci.yml
	go fmt ./...

.PHONY: deps
deps:
	go mod tidy
