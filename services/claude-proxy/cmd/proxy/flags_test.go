package main

import (
	"flag"
	"os"
	"testing"

	"claude-proxy/internal/config"
	"claude-proxy/internal/router"
)

func TestParseFlags(t *testing.T) {
	// Backup original args and CommandLine
	origArgs := os.Args
	origCommandLine := flag.CommandLine
	defer func() {
		os.Args = origArgs
		flag.CommandLine = origCommandLine
	}()

	os.Args = []string{"claude-proxy", "-config", "config.json"}
	flag.CommandLine = flag.NewFlagSet("test", flag.ContinueOnError)

	path := parseFlags()
	if path != "config.json" {
		t.Errorf("expected config.json, got %s", path)
	}
}

func TestApplyFlagOverrides(t *testing.T) {
	// Initialize baseline state
	cfg := config.Config{
		UpstreamURL: "http://initial",
		Port:        1111,
	}
	router.SetState(cfg)

	// Set overrides
	flagOverrides.port = 2222
	flagOverrides.upstream = "http://override"
	flagOverrides.apiKey = "override-key"
	flagOverrides.llmRouter = "true"

	applyFlagOverrides()

	st := router.GetState()
	if st.Config.Port != 2222 {
		t.Errorf("expected port 2222, got %d", st.Config.Port)
	}
	if st.Config.UpstreamURL != "http://override" {
		t.Errorf("expected http://override, got %s", st.Config.UpstreamURL)
	}
	if st.Config.UpstreamAPIKey != "override-key" {
		t.Errorf("expected override-key, got %s", st.Config.UpstreamAPIKey)
	}
	if !st.Config.UseLLMRouter {
		t.Errorf("expected UseLLMRouter true")
	}

	// Test flagOverrides.llmRouter = false
	flagOverrides.llmRouter = "false"
	applyFlagOverrides()
	if router.GetState().Config.UseLLMRouter {
		t.Errorf("expected UseLLMRouter false")
	}

	// Test invalid llmRouter value
	flagOverrides.llmRouter = "invalid"
	applyFlagOverrides()

	// Clean up
	flagOverrides = flagOverridesFn{}
}
