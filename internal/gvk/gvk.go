package gvk

import "k8s.io/apimachinery/pkg/runtime/schema"

var (
	CustomResourceDefinition = schema.GroupVersionKind{
		Group:   "apiextensions.k8s.io",
		Version: "v1",
		Kind:    "CustomResourceDefinition",
	}
)
