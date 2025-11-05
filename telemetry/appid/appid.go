//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package appid records and provides default app/agent names.
// It captures the first Runner's app and agent names as defaults and
// offers safe getters for fallback in observability or reporting paths.
package appid

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// Info holds a pair of application and agent names.
type Info struct {
	App   string
	Agent string
}

var (
	once  sync.Once
	first Info
	mu    sync.RWMutex
	seen  = make(map[string]struct{})
)

// pairSepStr is the string form of pairSepByte used when joining.
const pairSepStr string = "\x1f"

// RegisterRunner records a Runner's app and agent names.
// The first call defines the defaults used by DefaultApp/DefaultAgent.
func RegisterRunner(appName, agentName string) {
	// Set the first runner info once.
	once.Do(func() { first = Info{App: appName, Agent: agentName} })

	// Track the pair for optional diagnostics.
	k := appName + pairSepStr + agentName
	mu.Lock()
	seen[k] = struct{}{}
	mu.Unlock()
}

// DefaultApp returns the first Runner's app or the process name.
func DefaultApp() string {
	if v := first.App; v != "" {
		return v
	}
	return procName()
}

// DefaultAgent returns the first Runner's agent or the process name.
func DefaultAgent() string {
	if v := first.Agent; v != "" {
		return v
	}
	return procName()
}

// Runners returns a snapshot of all seen app/agent pairs.
func Runners() []Info {
	mu.RLock()
	defer mu.RUnlock()
	out := make([]Info, 0, len(seen))
	for k := range seen {
		// k is app pairSepStr agent
		app, agent, _ := strings.Cut(k, pairSepStr)
		out = append(out, Info{App: app, Agent: agent})
	}
	return out
}

// procName returns the current process basename.
func procName() string {
	exe, err := os.Executable()
	if err == nil && exe != "" {
		return filepath.Base(exe)
	}
	if len(os.Args) > 0 && os.Args[0] != "" {
		return filepath.Base(os.Args[0])
	}
	return "unknown"
}
