package module

import (
	"context"
	"reflect"
	"strings"
	"testing"
)

func testFactories() []Factory {
	return []Factory{
		{ID: "app.a.core", DefaultEnabled: true, Required: true, New: noop},
		{ID: "app.a.files", DefaultEnabled: true, Requires: []string{"app.a.core"}, New: noop},
		{ID: "app.a.webdav", DefaultEnabled: false, Requires: []string{"app.a.files"}, New: noop},
		{ID: "app.a.analysis.image", DefaultEnabled: true, New: noop},
		{ID: "app.a.analysis.video", DefaultEnabled: false, New: noop},
	}
}

func noop() Instance { return &noopInstance{} }

type noopInstance struct{}

func (n *noopInstance) Configure(context.Context, *Host) error { return nil }
func (n *noopInstance) Stop(context.Context) error            { return nil }

func ids(resolution *Resolution) []string { return IDs(resolution.Factories) }

func TestResolveDefaultSet(t *testing.T) {
	resolution, err := Resolve(testFactories(), nil, nil)
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	want := []string{"app.a.analysis.image", "app.a.core", "app.a.files"}
	if got := ids(resolution); !reflect.DeepEqual(got, want) {
		t.Fatalf("resolved = %v, want %v", got, want)
	}
}

func TestResolveAllowlistKeepsOnlyRequiredAndDependencies(t *testing.T) {
	resolution, err := Resolve(testFactories(), []string{"app.a.webdav"}, nil)
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	// The allowlist is authoritative: only the named module plus the always
	// required one survive. webdav's dependency is absent, so the cascade drops
	// webdav and says why instead of silently mounting a broken module.
	want := []string{"app.a.core"}
	if got := ids(resolution); !reflect.DeepEqual(got, want) {
		t.Fatalf("resolved = %v, want %v", got, want)
	}
	if len(resolution.Warnings) != 1 || !strings.Contains(resolution.Warnings[0], "app.a.webdav") {
		t.Fatalf("warnings = %v, want a cascade warning for webdav", resolution.Warnings)
	}
}

func TestResolveAllowlistWithDependenciesKeepsBoth(t *testing.T) {
	resolution, err := Resolve(testFactories(), []string{"app.a.files", "app.a.webdav"}, nil)
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	want := []string{"app.a.core", "app.a.files", "app.a.webdav"}
	if got := ids(resolution); !reflect.DeepEqual(got, want) {
		t.Fatalf("resolved = %v, want %v", got, want)
	}
}

func TestResolveDisabledCascadesToDependents(t *testing.T) {
	// webdav is default-enabled here so disabling its dependency must remove
	// it and report the cascade.
	factories := testFactories()
	for i := range factories {
		if factories[i].ID == "app.a.webdav" {
			factories[i].DefaultEnabled = true
		}
	}
	resolution, err := Resolve(factories, nil, []string{"app.a.files"})
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if resolution.Enabled["app.a.webdav"] {
		t.Fatal("webdav stays enabled without its files dependency")
	}
	if len(resolution.Warnings) != 1 || !strings.Contains(resolution.Warnings[0], "app.a.webdav") {
		t.Fatalf("warnings = %v, want a cascade warning for webdav", resolution.Warnings)
	}
}

func TestResolveRejectsDisablingRequiredModule(t *testing.T) {
	if _, err := Resolve(testFactories(), nil, []string{"app.a.core"}); err == nil {
		t.Fatal("expected an error when disabling a required module")
	}
}

func TestResolveRejectsUnknownModule(t *testing.T) {
	if _, err := Resolve(testFactories(), []string{"app.a.unknown"}, nil); err == nil {
		t.Fatal("expected an error for an unknown module ID")
	}
}

func TestResolveRejectsDuplicateID(t *testing.T) {
	factories := append(testFactories(), Factory{ID: "app.a.core", New: noop})
	if _, err := Resolve(factories, nil, nil); err == nil {
		t.Fatal("expected an error for a duplicate module ID")
	}
}

func TestResolveRejectsCycle(t *testing.T) {
	factories := []Factory{
		{ID: "app.a.one", DefaultEnabled: true, Requires: []string{"app.a.two"}, New: noop},
		{ID: "app.a.two", DefaultEnabled: true, Requires: []string{"app.a.one"}, New: noop},
	}
	if _, err := Resolve(factories, nil, nil); err == nil || !strings.Contains(err.Error(), "cycle") {
		t.Fatalf("error = %v, want a dependency cycle", err)
	}
}

func TestCapabilitiesAreSortedAndUnique(t *testing.T) {
	factories := []Factory{
		{ID: "app.a.one", Capabilities: []string{"files.read", "files.write"}},
		{ID: "app.a.two", Capabilities: []string{"files.read", "auth"}},
	}
	got := Capabilities(factories)
	want := []string{"auth", "files.read", "files.write"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Capabilities() = %v, want %v", got, want)
	}
}

func TestMigrateSchemaOnlyTouchesEnabledModules(t *testing.T) {
	// A nil gorm handle panics on use, so this asserts the skip path: a module
	// with no models and no hooks never touches the database.
	factories := []Factory{{ID: "app.a.empty"}}
	if err := MigrateSchema(context.Background(), nil, factories); err != nil {
		t.Fatalf("MigrateSchema() error = %v", err)
	}
}

func TestPortRegistryPrefersFeatureProvider(t *testing.T) {
	registry := NewPortRegistry()
	Provide[int](registry, "files", "app.a.integration", KindIntegration, 1)
	Provide[int](registry, "files", "app.a.feature", KindFeature, 2)
	got, ok := Get[int](registry, "files")
	if !ok || got != 2 {
		t.Fatalf("Get() = %d, %v, want the feature provider", got, ok)
	}
}

func TestPortRegistryRejectsAmbiguousIntegrations(t *testing.T) {
	registry := NewPortRegistry()
	Provide[int](registry, "files", "app.a.one", KindIntegration, 1)
	Provide[int](registry, "files", "app.a.two", KindIntegration, 2)
	if _, err := registry.Resolve("files"); err == nil {
		t.Fatal("expected an ambiguity error for two integration providers")
	}
}

func TestPortRegistrySelectionOverridesDefault(t *testing.T) {
	registry := NewPortRegistry()
	Provide[int](registry, "files", "app.a.feature", KindFeature, 1)
	Provide[int](registry, "files", "app.a.integration", KindIntegration, 2)
	registry.Select(map[string]string{"files": "app.a.integration"})
	got, ok := Get[int](registry, "files")
	if !ok || got != 2 {
		t.Fatalf("Get() = %d, %v, want the explicitly selected provider", got, ok)
	}
}

func TestPortRegistryMissingPort(t *testing.T) {
	if _, ok := Get[int](NewPortRegistry(), "files"); ok {
		t.Fatal("Get() = ok for a port with no provider")
	}
}
