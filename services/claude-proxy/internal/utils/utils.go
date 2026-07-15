package utils

import (
	"crypto/tls"
	"net/http"
	"os"
	"strconv"
	"time"
)

// getEnvInt reads an integer env var, returns fallback if missing or invalid.
func getEnvInt(key string, fallback int) int {
	if v := os.Getenv(key); v != "" {
		if i, err := strconv.Atoi(v); err == nil && i > 0 {
			return i
		}
	}
	return fallback
}

// getEnvDuration reads an env var representing seconds or a duration string (e.g. "120s"), returns fallback duration if missing or invalid.
func getEnvDuration(key string, fallback time.Duration) time.Duration {
	if v := os.Getenv(key); v != "" {
		if d, err := time.ParseDuration(v); err == nil && d > 0 {
			return d
		}
		if i, err := strconv.Atoi(v); err == nil && i > 0 {
			return time.Duration(i) * time.Second
		}
	}
	return fallback
}

// HTTPClient is the global HTTP client reused across the service.
var HTTPClient = &http.Client{
	// Timeout for all outbound requests (configurable via HTTP_CLIENT_TIMEOUT env, default 30s)
	Timeout: getEnvDuration("HTTP_CLIENT_TIMEOUT", 30*time.Second),
	Transport: &http.Transport{
		MaxIdleConns:          100,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
		DisableCompression:    true,
		TLSClientConfig: &tls.Config{
			InsecureSkipVerify: false,
		},
	},
}
