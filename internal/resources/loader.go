package resources

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"sigs.k8s.io/controller-runtime/pkg/client"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"

	"github.com/lburgazzoli/testcontainers-envtest/internal/resources/filter"
)

func loadFromFile(
	filePath string,
	objectFilter filter.ObjectFilter,
) ([]unstructured.Unstructured, error) {
	data, err := os.ReadFile(filePath) //nolint:gosec // paths come from trusted test configuration
	if err != nil {
		return nil, fmt.Errorf("failed to read file %s: %w", filePath, err)
	}

	manifests, err := Decode(data)
	if err != nil {
		return nil, fmt.Errorf("failed to decode YAML from %s: %w", filePath, err)
	}

	if objectFilter == nil {
		return manifests, nil
	}

	result := make([]unstructured.Unstructured, 0, len(manifests))
	for i := range manifests {
		if objectFilter(&manifests[i]) {
			result = append(result, manifests[i])
		}
	}

	return result, nil
}

func loadFromDirectory(
	dir string,
	objectFilter filter.ObjectFilter,
) ([]unstructured.Unstructured, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("failed to read directory %s: %w", dir, err)
	}

	var result []unstructured.Unstructured
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}

		ext := strings.ToLower(filepath.Ext(entry.Name()))
		if ext != ".yaml" && ext != ".yml" {
			continue
		}

		filePath := filepath.Join(dir, entry.Name())
		manifests, err := loadFromFile(filePath, objectFilter)
		if err != nil {
			return nil, err
		}
		result = append(result, manifests...)
	}

	return result, nil
}

func loadFromPath(
	path string,
	objectFilter filter.ObjectFilter,
) ([]unstructured.Unstructured, error) {
	info, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("manifest path does not exist: %s", path)
		}
		return nil, fmt.Errorf("failed to access manifest path %s: %w", path, err)
	}

	if info.IsDir() {
		return loadFromDirectory(path, objectFilter)
	}

	return loadFromFile(path, objectFilter)
}

// LoadFromPaths loads Kubernetes manifests from multiple paths.
// Relative paths are resolved relative to the project root.
// Supports glob patterns.
func LoadFromPaths(
	paths []string,
	objectFilter filter.ObjectFilter,
) ([]unstructured.Unstructured, error) {
	var result []unstructured.Unstructured

	for _, path := range paths {
		resolvedPath := path
		if !filepath.IsAbs(path) {
			projectRoot, err := FindProjectRoot()
			if err != nil {
				return nil, fmt.Errorf("failed to find project root for relative path %s: %w", path, err)
			}
			resolvedPath = filepath.Join(projectRoot, path)
		}

		if strings.ContainsAny(resolvedPath, "*?[]") {
			matches, err := filepath.Glob(resolvedPath)
			if err != nil {
				return nil, fmt.Errorf("failed to expand glob pattern %s: %w", resolvedPath, err)
			}

			for _, match := range matches {
				manifests, err := loadFromPath(match, objectFilter)
				if err != nil {
					return nil, err
				}
				result = append(result, manifests...)
			}
		} else {
			manifests, err := loadFromPath(resolvedPath, objectFilter)
			if err != nil {
				return nil, err
			}
			result = append(result, manifests...)
		}
	}

	return result, nil
}

// UnstructuredFromObjects converts client.Objects to unstructured objects.
func UnstructuredFromObjects(
	scheme *runtime.Scheme,
	objects []client.Object,
	objectFilter filter.ObjectFilter,
) ([]unstructured.Unstructured, error) {
	result := make([]unstructured.Unstructured, 0, len(objects))

	for _, obj := range objects {
		if err := EnsureGroupVersionKind(scheme, obj); err != nil {
			return nil, fmt.Errorf("failed to ensure GVK for object %T: %w", obj, err)
		}

		if objectFilter != nil && !objectFilter(obj) {
			continue
		}

		u, err := ToUnstructured(obj)
		if err != nil {
			return nil, fmt.Errorf("failed to convert object to unstructured: %w", err)
		}

		result = append(result, *u.DeepCopy())
	}

	return result, nil
}
