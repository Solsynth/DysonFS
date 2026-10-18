package module

import (
	"fmt"
	"sort"
	"strings"
)

// Resolution is the outcome of applying the config to the compiled registry.
type Resolution struct {
	Factories []Factory
	Enabled   map[string]bool
	// Warnings records modules removed because a dependency was disabled.
	Warnings []string
}

// Resolve applies [modules] enabled/disabled to the compiled factories.
//
// Rules:
//   - DefaultEnabled seeds the set.
//   - A non-empty enabled list becomes the allowlist.
//   - Required modules are always present.
//   - disabled removes modules; disabling a required module is an error.
//   - Dependents of a disabled module are removed with a warning.
//   - Module dependencies are topologically ordered; cycles fail.
func Resolve(factories []Factory, enabled, disabled []string) (*Resolution, error) {
	byID := make(map[string]Factory, len(factories))
	for _, factory := range factories {
		if strings.TrimSpace(factory.ID) == "" {
			return nil, fmt.Errorf("module with empty ID")
		}
		if _, exists := byID[factory.ID]; exists {
			return nil, fmt.Errorf("duplicate module ID %q", factory.ID)
		}
		if factory.Kind == "" {
			factory.Kind = KindFeature
		}
		byID[factory.ID] = factory
	}

	normalize := func(ids []string) ([]string, error) {
		out := make([]string, 0, len(ids))
		seen := make(map[string]bool, len(ids))
		for _, raw := range ids {
			id := strings.TrimSpace(raw)
			if id == "" {
				continue
			}
			if _, ok := byID[id]; !ok {
				return nil, fmt.Errorf("unknown module %q", id)
			}
			if seen[id] {
				continue
			}
			seen[id] = true
			out = append(out, id)
		}
		return out, nil
	}

	enabledIDs, err := normalize(enabled)
	if err != nil {
		return nil, err
	}
	disabledIDs, err := normalize(disabled)
	if err != nil {
		return nil, err
	}

	selected := make(map[string]bool, len(byID))
	if len(enabledIDs) > 0 {
		for _, id := range enabledIDs {
			selected[id] = true
		}
	} else {
		for id, factory := range byID {
			if factory.DefaultEnabled {
				selected[id] = true
			}
		}
	}

	// Required modules are part of every deployment.
	for id, factory := range byID {
		if factory.Required {
			selected[id] = true
		}
	}

	for _, id := range disabledIDs {
		if byID[id].Required {
			return nil, fmt.Errorf("module %q is required and cannot be disabled", id)
		}
		delete(selected, id)
	}

	// Cascade: a module whose dependency is absent cannot run.
	var warnings []string
	for {
		removed := false
		for id := range selected {
			for _, dep := range byID[id].Requires {
				if !selected[dep] {
					delete(selected, id)
					warnings = append(warnings,
						fmt.Sprintf("module %q disabled because dependency %q is disabled", id, dep))
					removed = true
					break
				}
			}
		}
		if !removed {
			break
		}
	}

	ordered, err := topologicalOrder(selected, byID)
	if err != nil {
		return nil, err
	}

	result := make([]Factory, 0, len(ordered))
	for _, id := range ordered {
		result = append(result, byID[id])
	}
	sort.Strings(warnings)
	return &Resolution{Factories: result, Enabled: selected, Warnings: warnings}, nil
}

// topologicalOrder sorts the selected modules so dependencies come first.
// Ties are broken by module ID for deterministic route registration.
func topologicalOrder(selected map[string]bool, byID map[string]Factory) ([]string, error) {
	remaining := make(map[string]int, len(selected))
	dependents := make(map[string][]string, len(selected))
	for id := range selected {
		remaining[id] = 0
	}
	for id := range selected {
		for _, dep := range byID[id].Requires {
			if !selected[dep] {
				return nil, fmt.Errorf("module %q requires disabled module %q", id, dep)
			}
			dependents[dep] = append(dependents[dep], id)
			remaining[id]++
		}
	}

	var ready []string
	for id, count := range remaining {
		if count == 0 {
			ready = append(ready, id)
		}
	}
	sort.Strings(ready)

	order := make([]string, 0, len(selected))
	for len(ready) > 0 {
		id := ready[0]
		ready = ready[1:]
		order = append(order, id)
		for _, child := range dependents[id] {
			remaining[child]--
			if remaining[child] == 0 {
				ready = append(ready, child)
			}
		}
		sort.Strings(ready)
	}

	if len(order) != len(selected) {
		var cycle []string
		for id, count := range remaining {
			if count > 0 {
				cycle = append(cycle, id)
			}
		}
		sort.Strings(cycle)
		return nil, fmt.Errorf("module dependency cycle involving %s", strings.Join(cycle, ", "))
	}
	return order, nil
}

// Capabilities collects unique capability names from the resolved modules.
func Capabilities(factories []Factory) []string {
	seen := map[string]bool{}
	var out []string
	for _, factory := range factories {
		for _, name := range factory.Capabilities {
			name = strings.TrimSpace(name)
			if name == "" || seen[name] {
				continue
			}
			seen[name] = true
			out = append(out, name)
		}
	}
	sort.Strings(out)
	return out
}

// IDs returns the resolved module IDs in dependency order.
func IDs(factories []Factory) []string {
	out := make([]string, 0, len(factories))
	for _, factory := range factories {
		out = append(out, factory.ID)
	}
	return out
}
