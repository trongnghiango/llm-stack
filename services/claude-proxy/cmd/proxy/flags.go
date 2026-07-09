package main

import (
	"flag"
	"fmt"
	"os"

	"claude-proxy/internal/logger"
	"claude-proxy/internal/router"
)

// parseFlags registers CLI flags, parses them, and applies overrides to the
// global config after it has been loaded from config.json. Returns the config
// file path so main() knows which file to load.
func parseFlags() string {
	cfgPath := flag.String("config", "config.json", "Path to configuration file")
	port := flag.Int("port", 0, "Override proxy port (default: from config)")
	upstream := flag.String("upstream", "", "Override upstream URL (default: from config)")
	apiKey := flag.String("api-key", "", "Override upstream API key (default: from config)")
	llmRouter := flag.String("llm-router", "", "Override use_llm_router (true/false, default: from config)")
	showHelp := flag.Bool("help", false, "Show usage")
	flag.Parse()

	if *showHelp {
		fmt.Fprintf(os.Stderr, "Usage: %s [flags]\n\nFlags:\n", os.Args[0])
		flag.PrintDefaults()
		os.Exit(0)
	}

	// Apply overrides after config.json is loaded — these are called from main()
	// after LoadConfig, so we store them in a hook.
	flagOverrides = flagOverridesFn{
		port:      *port,
		upstream:  *upstream,
		apiKey:    *apiKey,
		llmRouter: *llmRouter,
	}
	return *cfgPath
}

type flagOverridesFn struct {
	port      int
	upstream  string
	apiKey    string
	llmRouter string
}

var flagOverrides flagOverridesFn

// applyFlagOverrides writes any non-zero flag values into the global config.
func applyFlagOverrides() {
	s := router.GetState()
	cfg := s.Config // copy

	changed := false
	if flagOverrides.port > 0 {
		cfg.Port = flagOverrides.port
		logger.Infof("[Flags] Override port -> %d", cfg.Port)
		changed = true
	}
	if flagOverrides.upstream != "" {
		cfg.UpstreamURL = flagOverrides.upstream
		logger.Infof("[Flags] Override upstream -> %s", cfg.UpstreamURL)
		changed = true
	}
	if flagOverrides.apiKey != "" {
		cfg.UpstreamAPIKey = flagOverrides.apiKey
		logger.Infof("[Flags] Override API key provided")
		changed = true
	}
	if flagOverrides.llmRouter != "" {
		switch flagOverrides.llmRouter {
		case "true", "1":
			cfg.UseLLMRouter = true
		case "false", "0":
			cfg.UseLLMRouter = false
		default:
			logger.Errorf("[Flags] Invalid value for --llm-router: %s (expected true/false)", flagOverrides.llmRouter)
		}
		logger.Infof("[Flags] Override use_llm_router -> %v", cfg.UseLLMRouter)
		changed = true
	}

	if changed {
		router.SetState(cfg)
	}
}
