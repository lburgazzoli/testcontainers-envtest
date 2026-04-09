package resources

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/apiutil"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

func ToUnstructured(obj any) (*unstructured.Unstructured, error) {
	data, err := runtime.DefaultUnstructuredConverter.ToUnstructured(obj)
	if err != nil {
		return nil, fmt.Errorf("unable to convert object %T to unstructured: %w", obj, err)
	}

	return &unstructured.Unstructured{Object: data}, nil
}

func GetGroupVersionKindForObject(
	s *runtime.Scheme,
	obj runtime.Object,
) (schema.GroupVersionKind, error) {
	if obj == nil {
		return schema.GroupVersionKind{}, errors.New("nil object")
	}

	if obj.GetObjectKind().GroupVersionKind().Version != "" && obj.GetObjectKind().GroupVersionKind().Kind != "" {
		return obj.GetObjectKind().GroupVersionKind(), nil
	}

	gvk, err := apiutil.GVKForObject(obj, s)
	if err != nil {
		return schema.GroupVersionKind{}, fmt.Errorf("failed to get GVK: %w", err)
	}

	return gvk, nil
}

func EnsureGroupVersionKind(
	s *runtime.Scheme,
	obj client.Object,
) error {
	gvk, err := GetGroupVersionKindForObject(s, obj)
	if err != nil {
		return err
	}

	obj.GetObjectKind().SetGroupVersionKind(gvk)

	return nil
}

// Convert converts an unstructured object to a typed object and ensures GVK is set.
func Convert[T client.Object](
	scheme *runtime.Scheme,
	src *unstructured.Unstructured,
	dst T,
) error {
	if err := scheme.Convert(src, dst, nil); err != nil {
		return fmt.Errorf("failed to convert object: %w", err)
	}
	if err := EnsureGroupVersionKind(scheme, dst); err != nil {
		return fmt.Errorf("failed to ensure GVK for object: %w", err)
	}
	return nil
}

func Decode(content []byte) ([]unstructured.Unstructured, error) {
	results := make([]unstructured.Unstructured, 0)

	r := bytes.NewReader(content)
	yd := yaml.NewDecoder(r)

	for {
		var out map[string]any

		err := yd.Decode(&out)
		if err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			return nil, fmt.Errorf("unable to decode resource: %w", err)
		}

		if len(out) == 0 {
			continue
		}

		kind, ok := out["kind"]
		if !ok || kind == nil || kind == "" {
			continue
		}

		obj, err := ToUnstructured(&out)
		if err != nil {
			return nil, fmt.Errorf("unable to convert to unstructured: %w", err)
		}

		results = append(results, *obj)
	}

	return results, nil
}

// FindProjectRoot walks up from cwd looking for go.mod.
func FindProjectRoot() (string, error) {
	currentDir, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("failed to get current directory: %w", err)
	}

	for {
		if _, err := os.Stat(filepath.Join(currentDir, "go.mod")); err == nil {
			return filepath.FromSlash(currentDir), nil
		}

		parentDir := filepath.Dir(currentDir)
		if parentDir == currentDir {
			break
		}

		currentDir = parentDir
	}

	return "", errors.New("project root not found")
}
