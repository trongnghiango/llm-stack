package main

import (
	"flag"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

func TestMainFunctionDirect(t *testing.T) {
	// Create a valid config file in temp dir
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.json")
	cfgContent := `{"upstream_url":"http://127.0.0.1:9999","port":0,"use_llm_router":false}`
	if err := os.WriteFile(cfgPath, []byte(cfgContent), 0644); err != nil {
		t.Fatalf("write temp config failed: %v", err)
	}

	// Backup original state
	origArgs := os.Args
	origCommandLine := flag.CommandLine
	defer func() {
		os.Args = origArgs
		flag.CommandLine = origCommandLine
	}()

	// Setup fake command line args and flag set
	os.Args = []string{"claude-proxy", "-config", cfgPath, "-port", "0"}
	flag.CommandLine = flag.NewFlagSet("test", flag.ContinueOnError)

	// Run main in background
	mainChan := make(chan struct{})
	go func() {
		defer close(mainChan)
		main()
	}()

	// Wait for server to boot
	time.Sleep(200 * time.Millisecond)

	// Send SIGTERM to ourselves to trigger graceful shutdown
	err := syscall.Kill(syscall.Getpid(), syscall.SIGTERM)
	if err != nil {
		t.Fatalf("failed to send SIGTERM: %v", err)
	}

	// Wait for main to exit
	select {
	case <-mainChan:
		// success
	case <-time.After(5 * time.Second):
		t.Fatalf("main did not exit within timeout")
	}
}
