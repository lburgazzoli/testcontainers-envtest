package filter

import (
	"sigs.k8s.io/controller-runtime/pkg/client"

	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/sets"
)

// ObjectFilter is a predicate for filtering Kubernetes objects.
type ObjectFilter func(client.Object) bool

// ByType creates a filter that accepts only objects matching the given GVKs.
func ByType(gvks ...schema.GroupVersionKind) ObjectFilter {
	gvkSet := sets.New(gvks...)
	return func(obj client.Object) bool {
		return gvkSet.Has(obj.GetObjectKind().GroupVersionKind())
	}
}
