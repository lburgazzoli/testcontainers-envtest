# testcontainers-envtest Design Document

## Context

Controller-runtime's `envtest.Environment` requires downloading kube-apiserver and etcd binaries
(via `setup-envtest`) to run integration tests. This creates friction: CI environments need
pre-installed binaries, developers must manage binary versions, and cross-platform support
is limited.

This module replaces local binary management with testcontainers-go, running kube-apiserver
and etcd in a **single container** via Docker/Podman. The container is built at test time
from a UBI9-minimal base with envtest binaries downloaded from the official
`kubernetes-sigs/controller-tools` releases.

### Relationship to k3s-envtest

This module follows the architecture and API patterns established by
[k3s-envtest](https://github.com/lburgazzoli/k3s-envtest), which uses a K3s container
for the same purpose. The key difference is that this module runs **bare kube-apiserver + etcd**
(the same binaries that `setup-envtest` downloads) instead of K3s, resulting in:

- Lighter weight (no kubelet, scheduler, or controller-manager)
- Closer to standard envtest behavior (API server + etcd only)
- Uses the exact same binaries as standard envtest
- No pod scheduling capability (same limitation as standard envtest)

## Architecture: Single Container with UBI9

A single container runs both etcd and kube-apiserver. The container image is built at test time
using testcontainers' `FromDockerfile` capability. This eliminates the need for:

- A Docker network between containers
- Managing two separate container lifecycles
- Inter-container networking configuration

### Container Image

Built from `registry.access.redhat.com/ubi9-minimal` with envtest binaries downloaded
from official releases:

```dockerfile
FROM registry.access.redhat.com/ubi9-minimal

ARG ENVTEST_VERSION=1.32.0
ARG TARGETARCH=amd64

RUN microdnf install -y tar gzip && microdnf clean all

RUN curl -sL https://github.com/kubernetes-sigs/controller-tools/releases/download/envtest-v${ENVTEST_VERSION}/envtest-v${ENVTEST_VERSION}-linux-${TARGETARCH}.tar.gz \
    | tar xz -C /usr/local/bin --strip-components=2 controller-tools/envtest/

COPY entrypoint.sh /entrypoint.sh
RUN chmod +x /entrypoint.sh

EXPOSE 6443

ENTRYPOINT ["/entrypoint.sh"]
```

### Entrypoint Script

The entrypoint starts etcd in the background, waits for it to be healthy, then starts
kube-apiserver in the foreground:

```bash
#!/bin/bash
set -e

# Start etcd in background
etcd \
  --listen-client-urls=http://127.0.0.1:2379 \
  --advertise-client-urls=http://127.0.0.1:2379 \
  --listen-peer-urls=http://127.0.0.1:2380 \
  --data-dir=/tmp/etcd-data &

ETCD_PID=$!

# Wait for etcd to be ready
for i in $(seq 1 30); do
  if etcdctl endpoint health --endpoints=http://127.0.0.1:2379 2>/dev/null; then
    break
  fi
  sleep 0.5
done

# Start kube-apiserver (foreground)
exec kube-apiserver \
  --etcd-servers=http://127.0.0.1:2379 \
  --tls-cert-file=/etc/kubernetes/pki/apiserver.crt \
  --tls-private-key-file=/etc/kubernetes/pki/apiserver.key \
  --client-ca-file=/etc/kubernetes/pki/ca.crt \
  --service-account-signing-key-file=/etc/kubernetes/pki/sa.key \
  --service-account-key-file=/etc/kubernetes/pki/sa.pub \
  --service-account-issuer=https://kubernetes.default.svc.cluster.local \
  --authorization-mode=RBAC \
  --service-cluster-ip-range=10.0.0.0/24 \
  --disable-admission-plugins=ServiceAccount \
  "$@"
```

The `"$@"` at the end allows passing extra kube-apiserver flags via container command args.

### Binary Source

Envtest binaries are downloaded from the official `controller-tools` releases:
```
https://github.com/kubernetes-sigs/controller-tools/releases/download/envtest-v{VERSION}/envtest-v{VERSION}-linux-{ARCH}.tar.gz
```

Each tarball contains:
```
controller-tools/envtest/
  kube-apiserver
  etcd
  kubectl
```

These are the **exact same binaries** that `setup-envtest` downloads for local use.

## Package Structure

Following k3s-envtest's layout with `internal/` packages for separation of concerns:

```
github.com/lburgazzoli/testcontainers-envtest/
  pkg/envtest/
    envtest.go            # Environment type, Start/Stop, rest.Config construction
    envtest_opts.go       # Options struct, Option interface, With* functional options, env var loading
    envtest_test.go       # Integration tests
  internal/
    container/
      container.go        # Dockerfile generation, entrypoint script, container build+start
    cert/
      cert.go             # TLS certificate generation (reuse k3s-envtest pattern with tlscert)
    resources/
      crd.go              # CRD installation via SSA, WaitForCRDEstablished
      loader.go           # Manifest loading from paths/files/globs
      resources.go        # Unstructured conversion, YAML decode, GVK helpers
      filter/
        filter.go         # Object filtering (ByType, etc.)
    gvk/
      gvk.go              # Well-known GVK constants
  go.mod
  go.sum
```

## Public API

### Core Types

Following k3s-envtest patterns: `Option` as an interface (not `func`), `Options` struct with
grouped config sections, env var loading via viper, and `New()` returning `(*Environment, error)`.

```go
package envtest

type TeardownTask func(context.Context) error

// Environment manages a containerized kube-apiserver + etcd for testing.
type Environment struct {
    options Options

    container     testcontainers.Container

    cfg           *rest.Config
    cli           client.Client
    certData      *cert.Data
    manifests     Manifests
    teardownTasks []TeardownTask
}

// Manifests contains typed Kubernetes resources loaded from manifest files.
type Manifests struct {
    CustomResourceDefinitions []apiextensionsv1.CustomResourceDefinition
}
```

### Options (following k3s-envtest patterns)

```go
// Logger is compatible with testing.T's Logf method.
type Logger interface {
    Logf(format string, args ...any)
}

type LoggerFunc func(format string, args ...any)

type Option interface {
    ApplyToOptions(opts *Options)
}

type APIServerConfig struct {
    Args []string `mapstructure:"args"`
}

type EnvtestConfig struct {
    // Version is the Kubernetes version for envtest binaries, e.g. "1.32.0".
    // Defaults to DefaultKubernetesVersion (currently "1.35.0", updated with each module release).
    Version string `mapstructure:"version"`
}

type CRDConfig struct {
    ReadyTimeout time.Duration `mapstructure:"ready_timeout"`
    PollInterval time.Duration `mapstructure:"poll_interval"`
}

type CertificateConfig struct {
    Path     string        `mapstructure:"path"`
    Validity time.Duration `mapstructure:"validity"`
}

type ManifestConfig struct {
    Paths   []string        `mapstructure:"paths"`
    Objects []client.Object `mapstructure:"-"`
}

type LoggingConfig struct {
    Enabled *bool `mapstructure:"enabled"`
}

type Options struct {
    Scheme      *runtime.Scheme   `mapstructure:"-"`
    Envtest     EnvtestConfig     `mapstructure:"envtest"`
    APIServer   APIServerConfig   `mapstructure:"apiserver"`
    CRD         CRDConfig         `mapstructure:"crd"`
    Certificate CertificateConfig `mapstructure:"certificate"`
    Manifest    ManifestConfig    `mapstructure:"manifest"`
    Logging     LoggingConfig     `mapstructure:"logging"`
    Logger      Logger            `mapstructure:"-"`
}
```

### Functional Options

```go
// Version / image options
func WithKubernetesVersion(version string) Option  // e.g. "1.32.0" — determines binary download

// API Server options
func WithAPIServerArgs(args ...string) Option

// Manifest options
func WithManifests(paths ...string) Option
func WithObjects(objects ...client.Object) Option

// Certificate options
func WithCertPath(path string) Option
func WithCertValidity(duration time.Duration) Option

// CRD options
func WithCRDReadyTimeout(duration time.Duration) Option
func WithCRDPollInterval(duration time.Duration) Option

// Logger options
func WithLogger(logger Logger) Option
func WithTestcontainersLogging(enable bool) Option
func SuppressTestcontainersLogging() Option

// Scheme
func WithScheme(s *runtime.Scheme) Option

// Environment variable loading
func LoadConfigFromEnv() (*Options, error)  // prefix: ENVTEST_
```

### Constructor and Lifecycle

```go
func New(opts ...Option) (*Environment, error)

func (e *Environment) Start(ctx context.Context) error
func (e *Environment) Stop(ctx context.Context) error
func (e *Environment) AddTeardown(task TeardownTask)

// Accessors
func (e *Environment) Config() *rest.Config
func (e *Environment) Client() client.Client
func (e *Environment) Scheme() *runtime.Scheme
func (e *Environment) CertificatePaths() CertificatePaths
func (e *Environment) CABundle() []byte
func (e *Environment) ContainerID() string
func (e *Environment) CustomResourceDefinitions() []apiextensionsv1.CustomResourceDefinition
```

**Key design choices matching k3s-envtest:**
- `New()` returns `(*Environment, error)` — validates config eagerly
- `Start()` returns `error` (not `*rest.Config`) — config accessed via `Config()` accessor
- `Stop()` is safe to call even if `Start()` fails partway through
- Cleanup registered via `t.Cleanup()` or `defer` immediately after `New()`

## Container Orchestration

### Startup Sequence (`Start`)

1. **Configure testcontainers logger** based on user preferences

2. **Generate TLS certificates** (`internal/cert`):
   - Uses `github.com/mdelapenya/tlscert` (same as k3s-envtest)
   - Self-signed CA + server certificate with SANs: `localhost`, `127.0.0.1`
   - Client certificate (CN=`system:admin`, O=`system:masters`) for admin access
   - Service account RSA key pair
   - Written to temp directory, registered for cleanup as teardown task

3. **Build and start container** (`internal/container`):
   - Generate Dockerfile and entrypoint.sh from templates (parameterized by version/arch)
   - Build image via testcontainers `FromDockerfile`
   - Mount TLS files via `testcontainers.ContainerFile` (in-memory bytes from step 2):
     - `/etc/kubernetes/pki/apiserver.crt`
     - `/etc/kubernetes/pki/apiserver.key`
     - `/etc/kubernetes/pki/ca.crt`
     - `/etc/kubernetes/pki/sa.key`
     - `/etc/kubernetes/pki/sa.pub`
   - Exposed port: `6443`
   - Pass extra kube-apiserver args via container Cmd
   - Wait strategy: HTTPS health check on `/healthz` (port 6443, allow insecure TLS)

4. **Build `*rest.Config`**:
   - Host: `https://localhost:<mapped-6443-port>`
   - TLS: CA cert + client cert/key from generated material

5. **Create Kubernetes client** (`client.New(cfg, ...)`)

6. **Load and install CRDs** (if configured):
   - Load manifests from `ManifestConfig.Paths` and `ManifestConfig.Objects`
   - Filter for CRD types only
   - Install each via Server-Side Apply (same as k3s-envtest)
   - Poll until Established condition is true

### Shutdown Sequence (`Stop`)

Following k3s-envtest's reverse-order teardown pattern:

1. Execute teardown tasks in reverse order (cert cleanup, etc.)
2. Terminate the container

`Stop()` handles nil/uninitialized fields gracefully for partial-start cleanup.

## TLS Certificate Generation (`internal/cert`)

Reuses k3s-envtest's pattern with `github.com/mdelapenya/tlscert`:

```go
type Data struct {
    Path       string
    CACert     []byte
    ServerCert []byte
    ServerKey  []byte
}

func New(path string, validity time.Duration, sans []string) (*Data, error)
```

Additionally needs service account key pair generation (not in k3s-envtest since K3s
handles this internally):

```go
func GenerateServiceAccountKeyPair(path string) (pubPEM, privPEM []byte, err error)
```

Uses Go's `crypto/rsa` to generate an RSA key pair and returns PEM-encoded bytes for
mounting into the container.

### Certificate SANs

```go
var CertificateSANs = []string{
    "localhost",
    "127.0.0.1",
}
```

Simpler than k3s-envtest's SANs since there's no container-to-host webhook communication —
the client connects directly to the exposed port on localhost.

## Container Image Build (`internal/container`)

The `internal/container` package encapsulates Dockerfile generation and container lifecycle:

```go
// BuildAndStart builds the envtest container image and starts it.
func BuildAndStart(
    ctx context.Context,
    version string,
    certFiles map[string][]byte,  // path -> content for /etc/kubernetes/pki/
    extraArgs []string,           // additional kube-apiserver flags
    opts ...testcontainers.ContainerCustomizer,
) (testcontainers.Container, error)
```

The Dockerfile and entrypoint.sh are embedded in the Go binary via `//go:embed` or
generated as strings. A temporary build context directory is created for testcontainers.

### Image Caching

Testcontainers caches built images by default (keyed on Dockerfile content hash).
Since the Dockerfile is parameterized by Kubernetes version, changing the version
triggers a new build, while repeated runs with the same version reuse the cached image.

## CRD Installation (`internal/resources`)

Reuses k3s-envtest's approach — **not** using `envtest.InstallCRDs`:

- Load YAML manifests from paths (files, directories, globs)
- Filter for CRD types via `filter.ByType`
- Convert to typed `apiextensionsv1.CustomResourceDefinition`
- Install via Server-Side Apply (`client.Apply` with `ForceOwnership`)
- Poll for `Established` condition using `wait.PollUntilContextTimeout`

### Reusable internal packages from k3s-envtest

The following `internal/` packages can be adapted directly:

| Package | Source | Purpose |
|---------|--------|---------|
| `internal/cert` | k3s-envtest `internal/cert` | TLS cert generation with tlscert |
| `internal/resources` | k3s-envtest `internal/resources` | Manifest loading, CRD install, SSA |
| `internal/resources/filter` | k3s-envtest `internal/resources/filter` | Object type filtering |
| `internal/gvk` | k3s-envtest `internal/gvk` | Well-known GVK constants |

## Usage Example

```go
package mycontroller_test

import (
    "context"
    "testing"

    envtest "github.com/lburgazzoli/testcontainers-envtest/pkg/envtest"
)

func TestMyController(t *testing.T) {
    ctx := context.Background()

    env, err := envtest.New(
        envtest.WithKubernetesVersion("1.32.0"),
        envtest.WithManifests("../../config/crd/bases"),
        envtest.WithLogger(t),
    )
    if err != nil {
        t.Fatalf("failed to create environment: %v", err)
    }
    t.Cleanup(func() {
        _ = env.Stop(ctx)
    })

    if err := env.Start(ctx); err != nil {
        t.Fatalf("failed to start environment: %v", err)
    }

    // Use accessors (same pattern as k3s-envtest)
    k8sClient := env.Client()

    // ... run controller tests ...
}
```

### Migration from standard envtest

```go
// Before (standard envtest):
// env := &envtest.Environment{
//     CRDDirectoryPaths: []string{"../../config/crd/bases"},
//     BinaryAssetsDirectory: "...",
// }
// cfg, err := env.Start()

// After (containerized):
env, err := envtest.New(
    envtest.WithManifests("../../config/crd/bases"),
)
// ...
err = env.Start(ctx)
cfg := env.Config()
```

### Migration from k3s-envtest

```go
// Before (k3s-envtest):
// env, err := k3senv.New(
//     k3senv.WithK3sImage("rancher/k3s:v1.32.9-k3s1"),
//     k3senv.WithManifests("../../config/crd/bases"),
// )

// After (testcontainers-envtest):
env, err := envtest.New(
    envtest.WithKubernetesVersion("1.32.0"),
    envtest.WithManifests("../../config/crd/bases"),
)
// API is identical: env.Start(ctx), env.Config(), env.Client(), env.Stop(ctx)
```

## Key Implementation Details

### Single Container Simplification
Both etcd and kube-apiserver run in the same container. etcd listens on `127.0.0.1:2379`
(loopback only). No Docker network needed. The entrypoint script manages the process
lifecycle: etcd starts first, kube-apiserver starts after etcd is healthy.

### Port Mapping
kube-apiserver listens on 6443 inside the container. Testcontainers maps it to a random
host port. `rest.Config.Host` uses `localhost:<mapped-port>`. Obtain via
`container.MappedPort(ctx, "6443/tcp")`.

### TLS and Client Authentication
The kube-apiserver is configured with `--client-ca-file` pointing to the generated CA.
A client certificate with CN=`system:admin` and O=`system:masters` is generated from
the same CA, giving the client full admin access via RBAC.

### Admission Controllers
`ServiceAccount` admission plugin is disabled (`--disable-admission-plugins=ServiceAccount`)
because there is no controller-manager to populate service account tokens. This matches
standard envtest behavior.

### Service Account Keys
We generate an RSA key pair and pass it to kube-apiserver via
`--service-account-signing-key-file` and `--service-account-key-file`. This is needed for
any workload that creates ServiceAccount tokens.

### Multi-Architecture Support
The Dockerfile accepts `TARGETARCH` as a build arg. Testcontainers can detect the host
architecture and pass it through, enabling arm64 support on Apple Silicon.

### Available Kubernetes Versions
Any version available in the `controller-tools` releases can be used. As of writing,
versions range from v1.23.5 to v1.35.0.

Default version is defined as a constant:
```go
const DefaultKubernetesVersion = "1.35.0"
```
This constant is updated with each module release to track the latest stable Kubernetes version.

## Dependencies

```
github.com/testcontainers/testcontainers-go      # container management
github.com/mdelapenya/tlscert                     # TLS certificate generation
github.com/spf13/viper                            # env var config loading
sigs.k8s.io/controller-runtime                    # client, SSA, scheme
k8s.io/client-go                                  # rest.Config
k8s.io/apiextensions-apiserver                    # CRD types
k8s.io/apimachinery                               # unstructured, wait, GVK
k8s.io/utils                                      # ptr helpers
gopkg.in/yaml.v3                                  # YAML manifest parsing
```

## Verification

1. **Basic connectivity**: Start environment, create a Namespace, verify it exists
2. **CRD installation**: Start with CRD paths, verify CRD is registered and a CR can be created
3. **Custom versions**: Start with specific Kubernetes version, verify API server version
4. **Cleanup**: Verify container is removed after `Stop()`
5. **Partial failure**: Verify cleanup when container fails to start
6. **Logger integration**: Verify `WithLogger(t)` captures container lifecycle output

## File Implementation Guide

| File | Lines (est.) | Responsibility |
|------|-------------|----------------|
| `pkg/envtest/envtest.go` | ~250 | `Environment` struct, `New()`, `Start()`, `Stop()`, config build |
| `pkg/envtest/envtest_opts.go` | ~250 | `Options` struct, config sections, `Option` interface, `With*` funcs, env var loading, validation |
| `pkg/envtest/envtest_test.go` | ~100 | Integration tests |
| `internal/container/container.go` | ~150 | Dockerfile/entrypoint generation, build+start, wait strategy |
| `internal/cert/cert.go` | ~120 | TLS cert generation (adapted from k3s-envtest) + SA key pair |
| `internal/resources/crd.go` | ~90 | CRD install via SSA, WaitForCRDEstablished |
| `internal/resources/loader.go` | ~150 | Manifest loading from paths/files/globs |
| `internal/resources/resources.go` | ~100 | Unstructured conversion, YAML decode, GVK helpers |
| `internal/resources/filter/filter.go` | ~40 | Object filtering |
| `internal/gvk/gvk.go` | ~15 | Well-known GVK constants |