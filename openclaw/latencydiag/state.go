package latencydiag

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const (
	stateDirName       = "runtime"
	stateFileName      = "latency_diagnostics.json"
	stateFileMode      = 0o600
	stateDirMode       = 0o700
	stateCacheTTL      = 500 * time.Millisecond
	statusEnabledText  = "on"
	statusDisabledText = "off"
)

type fileState struct {
	Enabled bool `json:"enabled"`
}

type cacheEntry struct {
	enabled   bool
	expiresAt time.Time
}

var stateCache = struct {
	sync.Mutex
	entries map[string]cacheEntry
}{
	entries: make(map[string]cacheEntry),
}

// Enabled reports whether latency diagnostics should be enabled.
func Enabled(stateDir string) (bool, error) {
	path := statePath(stateDir)
	if path == "" {
		return true, nil
	}
	if enabled, ok := cachedEnabled(path, time.Now()); ok {
		return enabled, nil
	}
	enabled, err := readEnabled(path)
	if err == nil {
		cacheEnabled(path, enabled, time.Now())
	}
	return enabled, err
}

func readEnabled(path string) (bool, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return true, nil
		}
		return true, err
	}
	var state fileState
	if err := json.Unmarshal(data, &state); err != nil {
		return true, err
	}
	return state.Enabled, nil
}

// SetEnabled persists the latency diagnostics switch.
func SetEnabled(stateDir string, enabled bool) error {
	path := statePath(stateDir)
	if path == "" {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(path), stateDirMode); err != nil {
		return err
	}
	data, err := json.MarshalIndent(fileState{Enabled: enabled}, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	if err := os.WriteFile(path, data, stateFileMode); err != nil {
		return err
	}
	cacheEnabled(path, enabled, time.Now())
	return nil
}

// StatusText returns a stable display string for a boolean state.
func StatusText(enabled bool) string {
	if enabled {
		return statusEnabledText
	}
	return statusDisabledText
}

func statePath(stateDir string) string {
	root := strings.TrimSpace(stateDir)
	if root == "" {
		return ""
	}
	return filepath.Join(root, stateDirName, stateFileName)
}

func cachedEnabled(path string, now time.Time) (bool, bool) {
	stateCache.Lock()
	defer stateCache.Unlock()
	entry, ok := stateCache.entries[path]
	if !ok || now.After(entry.expiresAt) {
		return false, false
	}
	return entry.enabled, true
}

func cacheEnabled(path string, enabled bool, now time.Time) {
	stateCache.Lock()
	defer stateCache.Unlock()
	stateCache.entries[path] = cacheEntry{
		enabled:   enabled,
		expiresAt: now.Add(stateCacheTTL),
	}
}
