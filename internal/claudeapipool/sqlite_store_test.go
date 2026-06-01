package claudeapipool

import (
	"path/filepath"
	"testing"

	"github.com/router-for-me/CLIProxyAPI/v7/internal/config"
)

func TestResolveDBPathUsesFixedFileBesideConfig(t *testing.T) {
	configPath := filepath.Join("workspace", "config.yaml")
	got := ResolveDBPath(configPath, &config.Config{})
	want := filepath.Join("workspace", DefaultDBFileName)
	if got != want {
		t.Fatalf("ResolveDBPath() = %q, want %q", got, want)
	}
}

func TestLoadStoreMigratesYAMLWhenSQLiteEmpty(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.yaml")
	yamlPath := filepath.Join(dir, DefaultFileName)
	doc := &File{
		Version: 1,
		Defaults: Defaults{
			BaseURL: "https://api.example.test",
			Headers: map[string]string{
				"anthropic-version": "2023-06-01",
			},
		},
		Models: []config.ClaudeModel{{Name: "claude-default"}},
		Items:  []Item{{APIKey: "key-1"}},
	}
	if err := Save(yamlPath, doc); err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	got, err := LoadStore(configPath, &config.Config{})
	if err != nil {
		t.Fatalf("LoadStore() error = %v", err)
	}
	if len(got.Items) != 1 || got.Items[0].APIKey != "key-1" {
		t.Fatalf("LoadStore() items = %#v, want migrated key-1", got.Items)
	}
	if got.Defaults.BaseURL != "https://api.example.test" {
		t.Fatalf("Defaults.BaseURL = %q", got.Defaults.BaseURL)
	}
	if got.Models[0].Name != "claude-default" {
		t.Fatalf("Models = %#v", got.Models)
	}
}

func TestLoadStorePrefersExistingSQLiteOverYAML(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.yaml")
	yamlPath := filepath.Join(dir, DefaultFileName)
	if err := Save(yamlPath, &File{Version: 1, Items: []Item{{APIKey: "yaml-key"}}}); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	if _, err := LoadStore(configPath, &config.Config{}); err != nil {
		t.Fatalf("LoadStore() initial error = %v", err)
	}
	if err := SaveStore(configPath, &config.Config{}, &File{Version: 1, Items: []Item{{APIKey: "sqlite-key"}}}); err != nil {
		t.Fatalf("SaveStore() error = %v", err)
	}
	if err := Save(yamlPath, &File{Version: 1, Items: []Item{{APIKey: "changed-yaml-key"}}}); err != nil {
		t.Fatalf("Save() update error = %v", err)
	}

	got, err := LoadStore(configPath, &config.Config{})
	if err != nil {
		t.Fatalf("LoadStore() final error = %v", err)
	}
	if len(got.Items) != 1 || got.Items[0].APIKey != "sqlite-key" {
		t.Fatalf("LoadStore() items = %#v, want sqlite-key", got.Items)
	}
}

func TestSaveStoreRoundTripsConfigAndItemOrder(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.yaml")
	enabled := true
	doc := &File{
		Version:  1,
		PureMode: true,
		VirtualCache: VirtualCacheConfig{
			Enabled: &enabled,
		},
		Routing: RoutingConfig{
			PerAccountRPM: 5,
		},
		Defaults: Defaults{
			BaseURL:  "https://api.example.test",
			Priority: 3,
		},
		Models: []config.ClaudeModel{{Name: "claude-default"}},
		Items: []Item{
			{APIKey: "key-1"},
			{APIKey: "key-2", Disabled: true},
			{APIKey: "key-3"},
		},
	}
	if err := SaveStore(configPath, &config.Config{}, doc); err != nil {
		t.Fatalf("SaveStore() error = %v", err)
	}

	doc.Items = append(doc.Items[:1], doc.Items[2:]...)
	if err := SaveStore(configPath, &config.Config{}, doc); err != nil {
		t.Fatalf("SaveStore() after delete error = %v", err)
	}
	got, err := LoadStore(configPath, &config.Config{})
	if err != nil {
		t.Fatalf("LoadStore() error = %v", err)
	}
	if !got.PureMode || got.Routing.PerAccountRPM != 5 || got.Defaults.Priority != 3 || got.Models[0].Name != "claude-default" {
		t.Fatalf("round-tripped config = %#v", got)
	}
	if len(got.Items) != 2 || got.Items[0].APIKey != "key-1" || got.Items[1].APIKey != "key-3" {
		t.Fatalf("items = %#v, want key-1/key-3 after rewrite", got.Items)
	}
	resolved := Resolve(got)
	if resolved[0].Position != 1 || resolved[1].Position != 2 {
		t.Fatalf("positions = %#v", resolved)
	}
}
