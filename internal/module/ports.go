package module

import (
	"fmt"
	"sort"
	"sync"
)

// Provider is one implementation registered for a port.
type Provider struct {
	ModuleID string
	Kind     Kind
	Impl     any
}

// PortRegistry holds every provider published during ProvidePorts.
type PortRegistry struct {
	mu        sync.RWMutex
	providers map[PortID][]Provider
	selection map[PortID]string
}

// NewPortRegistry creates an empty registry.
func NewPortRegistry() *PortRegistry {
	return &PortRegistry{
		providers: map[PortID][]Provider{},
		selection: map[PortID]string{},
	}
}

// Provide publishes an implementation. Multiple modules may provide the same
// port; Resolve applies [ports] selection and the feature-over-integration
// default.
func (r *PortRegistry) Provide(id PortID, provider Provider) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.providers[id] = append(r.providers[id], provider)
}

// Select applies explicit [ports] choices (port -> module ID).
func (r *PortRegistry) Select(selection map[string]string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for rawPort, moduleID := range selection {
		r.selection[PortID(rawPort)] = moduleID
	}
}

// Has reports whether at least one provider exists for the port.
func (r *PortRegistry) Has(id PortID) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.providers[id]) > 0
}

// Resolve returns the active provider for a port.
func (r *PortRegistry) Resolve(id PortID) (Provider, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	providers := r.providers[id]
	if len(providers) == 0 {
		return Provider{}, fmt.Errorf("no provider registered for port %q", id)
	}
	if moduleID, ok := r.selection[id]; ok && moduleID != "" {
		for _, provider := range providers {
			if provider.ModuleID == moduleID {
				return provider, nil
			}
		}
		return Provider{}, fmt.Errorf("port %q is configured to use module %q, which does not provide it", id, moduleID)
	}

	var features, integrations []Provider
	for _, provider := range providers {
		if provider.Kind == KindFeature {
			features = append(features, provider)
		} else {
			integrations = append(integrations, provider)
		}
	}
	switch {
	case len(features) == 1:
		return features[0], nil
	case len(features) > 1:
		return Provider{}, fmt.Errorf("port %q has multiple feature providers (%s); set [ports] %s", id, providerNames(features), id)
	case len(integrations) == 1:
		return integrations[0], nil
	case len(integrations) > 1:
		return Provider{}, fmt.Errorf("port %q has multiple integration providers (%s); set [ports] %s", id, providerNames(integrations), id)
	default:
		return Provider{}, fmt.Errorf("no usable provider for port %q", id)
	}
}

func providerNames(providers []Provider) string {
	names := make([]string, 0, len(providers))
	for _, provider := range providers {
		names = append(names, provider.ModuleID)
	}
	sort.Strings(names)
	out := ""
	for i, name := range names {
		if i > 0 {
			out += ", "
		}
		out += name
	}
	return out
}

// Provide registers a typed port implementation.
func Provide[T any](registry *PortRegistry, id PortID, moduleID string, kind Kind, impl T) {
	registry.Provide(id, Provider{ModuleID: moduleID, Kind: kind, Impl: impl})
}

// Get resolves and type-asserts a port implementation.
func Get[T any](registry *PortRegistry, id PortID) (T, bool) {
	var zero T
	provider, err := registry.Resolve(id)
	if err != nil {
		return zero, false
	}
	value, ok := provider.Impl.(T)
	if !ok {
		return zero, false
	}
	return value, true
}
