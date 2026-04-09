package envtest

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/lburgazzoli/testcontainers-envtest/internal/cert"
	"github.com/lburgazzoli/testcontainers-envtest/internal/container"
	"github.com/lburgazzoli/testcontainers-envtest/internal/gvk"
	"github.com/lburgazzoli/testcontainers-envtest/internal/resources"
	"github.com/lburgazzoli/testcontainers-envtest/internal/resources/filter"
	"github.com/testcontainers/testcontainers-go"

	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/rest"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

var (
	// CertificateSANs are the Subject Alternative Names for the API server TLS certificate.
	CertificateSANs = []string{
		"localhost",
		"127.0.0.1",
	}
)

// TeardownTask is a cleanup function executed during Stop.
type TeardownTask func(context.Context) error

// CertificatePaths contains the file paths for TLS certificates.
type CertificatePaths struct {
	Dir     string
	CAFile  string
	TLSCert string
	TLSKey  string
}

// Manifests contains typed Kubernetes resources loaded from manifest files.
type Manifests struct {
	CustomResourceDefinitions []apiextensionsv1.CustomResourceDefinition
}

// Environment manages a containerized kube-apiserver + etcd for testing.
type Environment struct {
	options Options

	container testcontainers.Container

	cfg           *rest.Config
	cli           client.Client
	certData      *cert.Data
	manifests     Manifests
	teardownTasks []TeardownTask
}

// New creates a new Environment with the given options.
func New(opts ...Option) (*Environment, error) {
	options, err := LoadConfigFromEnv()
	if err != nil {
		return nil, fmt.Errorf("failed to load environment variables: %w", err)
	}

	options.ApplyOptions(opts)

	if err := options.validate(); err != nil {
		return nil, fmt.Errorf("invalid configuration: %w", err)
	}

	if options.Scheme == nil {
		options.Scheme = runtime.NewScheme()
	}

	return &Environment{
		options:       *options,
		teardownTasks: []TeardownTask{},
	}, nil
}

// Start initializes and starts the envtest environment.
//
// Always register cleanup immediately after New():
//
//	env, err := envtest.New(...)
//	if err != nil {
//	    return err
//	}
//	t.Cleanup(func() { _ = env.Stop(ctx) })
//	err = env.Start(ctx)
func (e *Environment) Start(ctx context.Context) error {
	e.debugf("Starting envtest environment with Kubernetes version: %s", e.options.Envtest.Version)

	if err := e.setupCertificates(); err != nil {
		return err
	}
	e.debugf("Generated certificates in: %s", e.certData.Path)

	if err := e.startContainer(ctx); err != nil {
		return err
	}
	e.debugf("Container started: %s", e.container.GetContainerID())

	if err := e.buildRestConfig(ctx); err != nil {
		return err
	}
	e.debugf("REST config ready: %s", e.cfg.Host)

	if err := e.createKubernetesClient(); err != nil {
		return err
	}

	if err := e.prepareManifests(); err != nil {
		return err
	}

	if err := e.installCRDs(ctx); err != nil {
		return err
	}

	e.debugf("envtest environment started successfully")
	return nil
}

// Stop tears down the environment. Safe to call even if Start() failed partway through.
func (e *Environment) Stop(ctx context.Context) error {
	e.debugf("Stopping envtest environment")
	var errs []error

	for i := len(e.teardownTasks) - 1; i >= 0; i-- {
		if err := e.teardownTasks[i](ctx); err != nil {
			errs = append(errs, fmt.Errorf("teardown task %d failed: %w", i, err))
		}
	}

	if e.container != nil {
		if err := testcontainers.TerminateContainer(e.container); err != nil {
			errs = append(errs, fmt.Errorf("failed to terminate container: %w", err))
		}
	}

	if len(errs) > 0 {
		return errors.Join(errs...)
	}

	return nil
}

// AddTeardown registers a cleanup task executed during Stop (in reverse order).
func (e *Environment) AddTeardown(task TeardownTask) {
	e.teardownTasks = append(e.teardownTasks, task)
}

func (e *Environment) Config() *rest.Config {
	return e.cfg
}

func (e *Environment) Client() client.Client {
	return e.cli
}

func (e *Environment) Scheme() *runtime.Scheme {
	return e.options.Scheme
}

func (e *Environment) ContainerID() string {
	if e.container == nil {
		return ""
	}
	return e.container.GetContainerID()
}

func (e *Environment) CABundle() []byte {
	if e.certData == nil {
		return nil
	}
	return e.certData.CABundle()
}

func (e *Environment) CertificatePaths() CertificatePaths {
	if e.certData == nil {
		return CertificatePaths{}
	}
	return CertificatePaths{
		Dir:     e.certData.Path,
		CAFile:  filepath.Join(e.certData.Path, cert.CACertFileName),
		TLSCert: filepath.Join(e.certData.Path, cert.CertFileName),
		TLSKey:  filepath.Join(e.certData.Path, cert.KeyFileName),
	}
}

// CustomResourceDefinitions returns a deep copy of loaded CRDs.
func (e *Environment) CustomResourceDefinitions() []apiextensionsv1.CustomResourceDefinition {
	result := make([]apiextensionsv1.CustomResourceDefinition, len(e.manifests.CustomResourceDefinitions))
	for i := range e.manifests.CustomResourceDefinitions {
		result[i] = *e.manifests.CustomResourceDefinitions[i].DeepCopy()
	}
	return result
}

func (e *Environment) setupCertificates() error {
	certPath := e.options.Certificate.Path
	if certPath == "" {
		certPath = fmt.Sprintf("%s%d", DefaultCertDirPrefix, os.Getpid())

		e.AddTeardown(func(_ context.Context) error {
			return os.RemoveAll(certPath)
		})
	}

	certData, err := cert.New(certPath, e.options.Certificate.Validity, CertificateSANs)
	if err != nil {
		return fmt.Errorf("failed to generate certificates: %w", err)
	}

	e.certData = certData
	return nil
}

func (e *Environment) startContainer(ctx context.Context) error {
	certs := container.CertFiles{
		CACert:     e.certData.CACert,
		ServerCert: e.certData.ServerCert,
		ServerKey:  e.certData.ServerKey,
		SAKey:      e.certData.SAKey,
		SAPub:      e.certData.SAPub,
	}

	image := container.ImageRef(e.options.Envtest.ImageRepository, e.options.Envtest.Version)
	e.debugf("Using container image: %s", image)

	c, err := container.Start(
		ctx,
		image,
		certs,
	)
	if err != nil {
		return fmt.Errorf("failed to start envtest container: %w", err)
	}

	e.container = c
	return nil
}

func (e *Environment) buildRestConfig(ctx context.Context) error {
	port, err := container.MappedAPIServerPort(ctx, e.container)
	if err != nil {
		return fmt.Errorf("failed to get API server port: %w", err)
	}

	e.cfg = &rest.Config{
		Host: fmt.Sprintf("https://localhost:%s", port),
		TLSClientConfig: rest.TLSClientConfig{
			CAData:   e.certData.CACert,
			CertData: e.certData.ClientCert,
			KeyData:  e.certData.ClientKey,
		},
	}

	return nil
}

func (e *Environment) createKubernetesClient() error {
	cli, err := client.New(e.cfg, client.Options{Scheme: e.options.Scheme})
	if err != nil {
		return fmt.Errorf("failed to create Kubernetes client: %w", err)
	}

	e.cli = cli
	return nil
}

func (e *Environment) prepareManifests() error {
	e.manifests = Manifests{}

	manifestFilter := filter.ByType(gvk.CustomResourceDefinition)

	var unstructuredObjs []runtime.Object

	if len(e.options.Manifest.Paths) > 0 {
		manifests, err := resources.LoadFromPaths(e.options.Manifest.Paths, manifestFilter)
		if err != nil {
			return fmt.Errorf("failed to load manifests from paths: %w", err)
		}
		for i := range manifests {
			unstructuredObjs = append(unstructuredObjs, &manifests[i])
		}
	}

	if len(e.options.Manifest.Objects) > 0 {
		manifests, err := resources.UnstructuredFromObjects(
			e.options.Scheme,
			e.options.Manifest.Objects,
			manifestFilter,
		)
		if err != nil {
			return fmt.Errorf("failed to convert objects: %w", err)
		}
		for i := range manifests {
			unstructuredObjs = append(unstructuredObjs, &manifests[i])
		}
	}

	for _, obj := range unstructuredObjs {
		uns, ok := obj.(*unstructured.Unstructured)
		if !ok {
			continue
		}

		if uns.GroupVersionKind() == gvk.CustomResourceDefinition {
			var crd apiextensionsv1.CustomResourceDefinition
			if err := resources.Convert(e.options.Scheme, uns, &crd); err != nil {
				return fmt.Errorf("failed to convert CRD %s: %w", uns.GetName(), err)
			}
			e.manifests.CustomResourceDefinitions = append(e.manifests.CustomResourceDefinitions, crd)
		}
	}

	e.debugf("Loaded %d CRDs", len(e.manifests.CustomResourceDefinitions))
	return nil
}

func (e *Environment) installCRDs(ctx context.Context) error {
	for i := range e.manifests.CustomResourceDefinitions {
		crd := &e.manifests.CustomResourceDefinitions[i]
		e.debugf("Installing CRD %s", crd.GetName())

		err := resources.InstallCRD(
			ctx,
			e.cli,
			e.options.Scheme,
			crd,
			e.options.CRD.PollInterval,
			e.options.CRD.ReadyTimeout,
		)
		if err != nil {
			return fmt.Errorf("failed to install CRD %s: %w", crd.GetName(), err)
		}

		e.debugf("CRD %s is now established", crd.GetName())
	}

	return nil
}

func (e *Environment) debugf(format string, args ...any) {
	if e.options.Logger != nil {
		e.options.Logger.Logf("[envtest] "+format, args...)
	}
}
