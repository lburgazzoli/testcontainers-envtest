package envtest

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/spf13/viper"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/utils/ptr"

	"github.com/lburgazzoli/testcontainers-envtest/internal/container"
)

const (
	DefaultKubernetesVersion = "1.35.0"
	DefaultCertDirPrefix     = "/tmp/envtest-certs-"
	DefaultCertValidity      = 24 * time.Hour
	DefaultCRDPollInterval   = 100 * time.Millisecond
	DefaultCRDReadyTimeout   = 30 * time.Second
)

// Logger is compatible with testing.T's Logf method.
type Logger interface {
	Logf(format string, args ...any)
}

// LoggerFunc adapts a printf-style function to the Logger interface.
type LoggerFunc func(format string, args ...any)

func (f LoggerFunc) Logf(format string, args ...any) {
	f(format, args...)
}

// Option configures an Environment.
type Option interface {
	ApplyToOptions(opts *Options)
}

type optionFunc func(*Options)

func (f optionFunc) ApplyToOptions(o *Options) {
	f(o)
}

// EnvtestConfig groups envtest container configuration.
type EnvtestConfig struct {
	// Version is the Kubernetes version, e.g. "1.32.0".
	// Used as the image tag. Defaults to DefaultKubernetesVersion.
	Version string `mapstructure:"version"`

	// ImageRepository is the container image repository.
	// Defaults to DefaultImageRepository ("quay.io/lburgazzoli/testcontainers-envtest").
	ImageRepository string `mapstructure:"image_repository"`
}

// APIServerConfig groups kube-apiserver configuration.
type APIServerConfig struct {
	Args []string `mapstructure:"args"`
}

// CRDConfig groups CRD installation configuration.
type CRDConfig struct {
	ReadyTimeout time.Duration `mapstructure:"ready_timeout"`
	PollInterval time.Duration `mapstructure:"poll_interval"`
}

// CertificateConfig groups certificate configuration.
type CertificateConfig struct {
	Path     string        `mapstructure:"path"`
	Validity time.Duration `mapstructure:"validity"`
}

// ManifestConfig groups manifest loading configuration.
type ManifestConfig struct {
	Paths   []string        `mapstructure:"paths"`
	Objects []client.Object `mapstructure:"-"`
}

// LoggingConfig groups logging configuration.
type LoggingConfig struct {
	Enabled *bool `mapstructure:"enabled"`
}

// Options holds all configuration for an Environment.
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

func (o *Options) ApplyOptions(opts []Option) *Options {
	for _, opt := range opts {
		opt.ApplyToOptions(o)
	}
	return o
}

func (o *Options) ApplyToOptions(target *Options) {
	if o.Scheme != nil {
		target.Scheme = o.Scheme
	}

	if o.Envtest.Version != "" {
		target.Envtest.Version = o.Envtest.Version
	}
	if o.Envtest.ImageRepository != "" {
		target.Envtest.ImageRepository = o.Envtest.ImageRepository
	}

	if len(o.APIServer.Args) > 0 {
		target.APIServer.Args = append(target.APIServer.Args, o.APIServer.Args...)
	}

	if o.CRD.ReadyTimeout != 0 {
		target.CRD.ReadyTimeout = o.CRD.ReadyTimeout
	}
	if o.CRD.PollInterval != 0 {
		target.CRD.PollInterval = o.CRD.PollInterval
	}

	if o.Certificate.Path != "" {
		target.Certificate.Path = o.Certificate.Path
	}
	if o.Certificate.Validity != 0 {
		target.Certificate.Validity = o.Certificate.Validity
	}

	if len(o.Manifest.Paths) > 0 {
		target.Manifest.Paths = append(target.Manifest.Paths, o.Manifest.Paths...)
	}
	if len(o.Manifest.Objects) > 0 {
		target.Manifest.Objects = append(target.Manifest.Objects, o.Manifest.Objects...)
	}

	if o.Logging.Enabled != nil {
		target.Logging.Enabled = o.Logging.Enabled
	}

	if o.Logger != nil {
		target.Logger = o.Logger
	}
}

var _ Option = &Options{}

// Scheme option.

func WithScheme(s *runtime.Scheme) Option {
	return optionFunc(func(o *Options) { o.Scheme = s })
}

// Version options.

func WithKubernetesVersion(version string) Option {
	return optionFunc(func(o *Options) { o.Envtest.Version = version })
}

func WithImageRepository(repository string) Option {
	return optionFunc(func(o *Options) { o.Envtest.ImageRepository = repository })
}

// API Server options.

func WithAPIServerArgs(args ...string) Option {
	return optionFunc(func(o *Options) { o.APIServer.Args = append(o.APIServer.Args, args...) })
}

// Manifest options.

func WithManifests(paths ...string) Option {
	return optionFunc(func(o *Options) { o.Manifest.Paths = append(o.Manifest.Paths, paths...) })
}

func WithObjects(objects ...client.Object) Option {
	return optionFunc(func(o *Options) { o.Manifest.Objects = append(o.Manifest.Objects, objects...) })
}

// Certificate options.

func WithCertPath(path string) Option {
	return optionFunc(func(o *Options) { o.Certificate.Path = path })
}

func WithCertValidity(duration time.Duration) Option {
	return optionFunc(func(o *Options) { o.Certificate.Validity = duration })
}

// CRD options.

func WithCRDReadyTimeout(duration time.Duration) Option {
	return optionFunc(func(o *Options) { o.CRD.ReadyTimeout = duration })
}

func WithCRDPollInterval(duration time.Duration) Option {
	return optionFunc(func(o *Options) { o.CRD.PollInterval = duration })
}

// Logger options.

func WithLogger(logger Logger) Option {
	return optionFunc(func(o *Options) { o.Logger = logger })
}

func WithTestcontainersLogging(enable bool) Option {
	return optionFunc(func(o *Options) { o.Logging.Enabled = &enable })
}

func SuppressTestcontainersLogging() Option {
	return WithTestcontainersLogging(false)
}

// LoadConfigFromEnv loads configuration from environment variables with ENVTEST_ prefix.
func LoadConfigFromEnv() (*Options, error) {
	v := viper.New()

	v.SetEnvPrefix("ENVTEST")
	v.AutomaticEnv()
	v.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))

	v.SetDefault("envtest.version", DefaultKubernetesVersion)
	v.SetDefault("envtest.image_repository", container.DefaultImageRepository)
	v.SetDefault("apiserver.args", []string{})
	v.SetDefault("crd.ready_timeout", DefaultCRDReadyTimeout)
	v.SetDefault("crd.poll_interval", DefaultCRDPollInterval)
	v.SetDefault("certificate.path", "")
	v.SetDefault("certificate.validity", DefaultCertValidity)
	v.SetDefault("manifest.paths", []string{})
	v.SetDefault("logging.enabled", true)

	var opts Options

	if err := v.Unmarshal(&opts); err != nil {
		return nil, fmt.Errorf("failed to unmarshal config from environment: %w", err)
	}

	if opts.Logging.Enabled == nil {
		opts.Logging.Enabled = ptr.To(true)
	}

	return &opts, nil
}

func (opts *Options) validate() error {
	if opts.Envtest.Version == "" {
		return errors.New("kubernetes version cannot be empty")
	}

	if opts.CRD.ReadyTimeout <= 0 {
		return fmt.Errorf("CRD ready timeout must be positive, got %v", opts.CRD.ReadyTimeout)
	}

	if opts.CRD.PollInterval <= 0 {
		return fmt.Errorf("CRD poll interval must be positive, got %v", opts.CRD.PollInterval)
	}
	if opts.CRD.PollInterval < 10*time.Millisecond {
		return fmt.Errorf("CRD poll interval too small: %v (minimum: 10ms)", opts.CRD.PollInterval)
	}

	if opts.Certificate.Validity <= 0 {
		return fmt.Errorf("certificate validity must be positive, got %v", opts.Certificate.Validity)
	}

	return nil
}
