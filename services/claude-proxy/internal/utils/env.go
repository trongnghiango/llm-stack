package utils

import (
    "os"
    "strconv"
    "strings"
    "time"
)

// Bool reads an environment variable as a boolean.
// Returns def if variable is unset, empty, or cannot be parsed.
// Accepts "1", "true", "0", "false" (case‑insensitive).
func Bool(key string, def bool) bool {
    v := os.Getenv(key)
    if v == "" {
        return def
    }
    lower := strings.ToLower(v)
    switch lower {
    case "1", "true":
        return true
    case "0", "false":
        return false
    default:
        return def
    }
}

// Int reads an environment variable as a positive integer.
// Returns def if variable is unset, empty, or cannot be parsed.
func Int(key string, def int) int {
    v := os.Getenv(key)
    if v == "" {
        return def
    }
    if i, err := strconv.Atoi(v); err == nil && i > 0 {
        return i
    }
    return def
}

// Duration reads an environment variable as seconds and returns a time.Duration.
// Returns def if variable is unset, empty, or cannot be parsed.
func Duration(key string, def time.Duration) time.Duration {
    v := os.Getenv(key)
    if v == "" {
        return def
    }
    if i, err := strconv.Atoi(v); err == nil && i > 0 {
        return time.Duration(i) * time.Second
    }
    return def
}
