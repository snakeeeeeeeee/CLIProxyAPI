package synthesizer

import (
	"testing"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v7/internal/claudeapipool"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/config"
)

func TestSynthesizeClaudeAPIPoolAuthIncludesPureModeAttribute(t *testing.T) {
	item := claudeapipool.Resolve(&claudeapipool.File{
		Version:  1,
		PureMode: true,
		Items:    []claudeapipool.Item{{APIKey: "key-123"}},
	})[0]
	auth := SynthesizeClaudeAPIPoolAuth(&SynthesisContext{
		Config:      &config.Config{},
		Now:         time.Unix(0, 0).UTC(),
		IDGenerator: NewStableIDGenerator(),
	}, item)
	if auth == nil {
		t.Fatal("SynthesizeClaudeAPIPoolAuth() returned nil")
	}
	if got := auth.Attributes[claudeapipool.AttrPureMode]; got != "true" {
		t.Fatalf("pure mode attribute = %q, want true", got)
	}
}
