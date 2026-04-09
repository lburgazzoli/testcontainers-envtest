package container

import (
	"bytes"
	"context"
	"crypto/tls"
	"fmt"
	"time"

	"github.com/moby/moby/api/types/container"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
)

const (
	apiServerPort = "6443/tcp"

	// DefaultImageRepository is the default container image repository.
	DefaultImageRepository = "quay.io/lburgazzoli/testcontainers-envtest"
)

// CertFiles holds PEM-encoded certificate content for mounting into the container.
type CertFiles struct {
	CACert     []byte
	ServerCert []byte
	ServerKey  []byte
	SAKey      []byte
	SAPub      []byte
}

// Start starts the envtest container from a pre-built image.
func Start(
	ctx context.Context,
	image string,
	certs CertFiles,
	opts ...testcontainers.ContainerCustomizer,
) (testcontainers.Container, error) {
	containerFiles := []testcontainers.ContainerFile{
		{Reader: bytes.NewReader(certs.CACert), ContainerFilePath: "/etc/kubernetes/pki/ca.crt", FileMode: 0o644},
		{Reader: bytes.NewReader(certs.ServerCert), ContainerFilePath: "/etc/kubernetes/pki/apiserver.crt", FileMode: 0o644},
		{Reader: bytes.NewReader(certs.ServerKey), ContainerFilePath: "/etc/kubernetes/pki/apiserver.key", FileMode: 0o600},
		{Reader: bytes.NewReader(certs.SAKey), ContainerFilePath: "/etc/kubernetes/pki/sa.key", FileMode: 0o600},
		{Reader: bytes.NewReader(certs.SAPub), ContainerFilePath: "/etc/kubernetes/pki/sa.pub", FileMode: 0o644},
	}

	req := testcontainers.ContainerRequest{
		Image:        image,
		ExposedPorts: []string{apiServerPort},
		Files:        containerFiles,
		// systemd in ubi9-init needs /run as tmpfs and cgroups access
		HostConfigModifier: func(hc *container.HostConfig) {
			hc.Privileged = true
		},
		WaitingFor: wait.ForHTTP("/healthz").
			WithPort(apiServerPort).
			WithTLS(true, &tls.Config{InsecureSkipVerify: true}). //nolint:gosec // health check against local test container
			WithStartupTimeout(120 * time.Second).
			WithStatusCodeMatcher(func(status int) bool {
				return status == 200
			}),
	}

	genericReq := testcontainers.GenericContainerRequest{
		ContainerRequest: req,
		Started:          true,
	}

	for _, opt := range opts {
		if err := opt.Customize(&genericReq); err != nil {
			return nil, fmt.Errorf("failed to apply container option: %w", err)
		}
	}

	c, err := testcontainers.GenericContainer(ctx, genericReq)
	if err != nil {
		return nil, fmt.Errorf("failed to start envtest container: %w", err)
	}

	return c, nil
}

// MappedAPIServerPort returns the host-mapped port for the API server.
func MappedAPIServerPort(ctx context.Context, c testcontainers.Container) (string, error) {
	port, err := c.MappedPort(ctx, apiServerPort)
	if err != nil {
		return "", fmt.Errorf("failed to get mapped port: %w", err)
	}
	return port.Port(), nil
}

// ImageRef returns the full image reference for a given Kubernetes version.
func ImageRef(repository string, version string) string {
	return fmt.Sprintf("%s:%s", repository, version)
}
