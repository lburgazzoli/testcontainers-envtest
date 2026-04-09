package envtest_test

import (
	"os"
	"testing"

	"sigs.k8s.io/controller-runtime/pkg/client"

	corev1 "k8s.io/api/core/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	k8sscheme "k8s.io/client-go/kubernetes/scheme"

	envtest "github.com/lburgazzoli/testcontainers-envtest/pkg/envtest"

	. "github.com/onsi/gomega"
)

func TestBasicConnectivity(t *testing.T) {
	g := NewWithT(t)
	ctx := t.Context()

	imageRepo := os.Getenv("ENVTEST_IMAGE_REPOSITORY")
	if imageRepo == "" {
		t.Skip("ENVTEST_IMAGE_REPOSITORY not set")
	}

	k8sVersion := os.Getenv("ENVTEST_KUBERNETES_VERSION")
	if k8sVersion == "" {
		t.Skip("ENVTEST_KUBERNETES_VERSION not set")
	}

	scheme := runtime.NewScheme()
	g.Expect(k8sscheme.AddToScheme(scheme)).To(Succeed())
	g.Expect(apiextensionsv1.AddToScheme(scheme)).To(Succeed())

	env, err := envtest.New(
		envtest.WithKubernetesVersion(k8sVersion),
		envtest.WithImageRepository(imageRepo),
		envtest.WithScheme(scheme),
		envtest.WithLogger(t),
	)
	g.Expect(err).ToNot(HaveOccurred())

	t.Cleanup(func() {
		g.Expect(env.Stop(ctx)).To(Succeed())
	})

	g.Expect(env.Start(ctx)).To(Succeed())

	cfg := env.Config()
	g.Expect(cfg).ToNot(BeNil())
	g.Expect(cfg.Host).To(ContainSubstring("localhost"))

	k8sClient := env.Client()
	g.Expect(k8sClient).ToNot(BeNil())

	// Create a namespace to verify the API server is functional
	ns := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-ns",
		},
	}
	g.Expect(k8sClient.Create(ctx, ns)).To(Succeed())

	// Verify it exists
	got := &corev1.Namespace{}
	g.Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(ns), got)).To(Succeed())
	g.Expect(got.Name).To(Equal("test-ns"))
}
