package resources

import (
	"context"
	"fmt"
	"time"

	"sigs.k8s.io/controller-runtime/pkg/client"

	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	k8serr "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/wait"
)

// IsCRDEstablished checks if a CRD has the Established condition set to true.
func IsCRDEstablished(crd *apiextensionsv1.CustomResourceDefinition) bool {
	for _, condition := range crd.Status.Conditions {
		if condition.Type == apiextensionsv1.Established && condition.Status == apiextensionsv1.ConditionTrue {
			return true
		}
	}
	return false
}

// WaitForCRDEstablished polls until a CRD becomes established or the timeout is reached.
func WaitForCRDEstablished(
	ctx context.Context,
	cli client.Client,
	crdName string,
	pollInterval time.Duration,
	timeout time.Duration,
) error {
	err := wait.PollUntilContextTimeout(ctx, pollInterval, timeout, true, func(ctx context.Context) (bool, error) {
		crd := apiextensionsv1.CustomResourceDefinition{}

		err := cli.Get(ctx, types.NamespacedName{Name: crdName}, &crd)
		switch {
		case k8serr.IsNotFound(err):
			return false, nil
		case err != nil:
			return false, fmt.Errorf("failed to get CRD: %w", err)
		default:
			return IsCRDEstablished(&crd), nil
		}
	})

	if err != nil {
		return fmt.Errorf("CRD %s not established: %w", crdName, err)
	}

	return nil
}

// InstallCRD installs a single CRD via Server-Side Apply and waits for it to be established.
func InstallCRD(
	ctx context.Context,
	cli client.Client,
	scheme *runtime.Scheme,
	crd *apiextensionsv1.CustomResourceDefinition,
	pollInterval time.Duration,
	readyTimeout time.Duration,
) error {
	if err := EnsureGroupVersionKind(scheme, crd); err != nil {
		return fmt.Errorf("failed to set GVK for CRD %s: %w", crd.GetName(), err)
	}

	unstructuredCRD, err := ToUnstructured(crd)
	if err != nil {
		return fmt.Errorf("failed to convert CRD %s to unstructured: %w", crd.GetName(), err)
	}

	applyConfig := client.ApplyConfigurationFromUnstructured(unstructuredCRD)
	err = cli.Apply(ctx, applyConfig, client.ForceOwnership, client.FieldOwner("testcontainers-envtest"))
	if err != nil {
		return fmt.Errorf("failed to apply CRD %s: %w", crd.GetName(), err)
	}

	err = WaitForCRDEstablished(ctx, cli, crd.GetName(), pollInterval, readyTimeout)
	if err != nil {
		return fmt.Errorf("failed to wait for CRD to be established: %w", err)
	}

	return nil
}
