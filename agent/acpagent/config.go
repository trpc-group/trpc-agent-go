//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package acpagent

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"
)

type processConfig struct {
	// Name is the agent name reported to the framework and used for state namespacing.
	Name string
	// Description is the optional agent description reported to the framework.
	Description string
	// Command is the executable used to start the ACP agent.
	Command string
	// Args are passed to Command without shell expansion.
	Args []string
	// Env contains additional process environment entries in KEY=VALUE form.
	Env []string
	// WorkDir is the absolute working directory used to start the ACP agent.
	WorkDir string
	// IdleTimeout controls shared process idle recycling. Zero disables recycling.
	IdleTimeout time.Duration
}

func (c processConfig) validate() error {
	if strings.TrimSpace(c.Name) == "" {
		return errors.New("acpagent: name is required")
	}
	if strings.TrimSpace(c.Command) == "" {
		return errors.New("acpagent: command is required")
	}
	if strings.ContainsRune(c.Command, '\x00') {
		return errors.New("acpagent: command contains a NUL byte")
	}
	if c.WorkDir == "" {
		return errors.New("acpagent: work directory is required")
	}
	if strings.ContainsRune(c.WorkDir, '\x00') {
		return errors.New("acpagent: work directory contains a NUL byte")
	}
	if !filepath.IsAbs(c.WorkDir) {
		return fmt.Errorf("acpagent: work directory must be absolute: %q", c.WorkDir)
	}
	if c.IdleTimeout < 0 {
		return errors.New("acpagent: idle timeout must not be negative")
	}
	for i, entry := range c.Env {
		key, _, ok := strings.Cut(entry, "=")
		if !ok {
			return fmt.Errorf("acpagent: env entry %d must use KEY=VALUE", i)
		}
		if key == "" {
			return fmt.Errorf("acpagent: env entry %d has an empty key", i)
		}
		if strings.ContainsRune(key, '\x00') || strings.ContainsRune(entry[len(key)+1:], '\x00') {
			return fmt.Errorf("acpagent: env entry %d contains a NUL byte", i)
		}
	}
	return nil
}

func (c processConfig) clone() processConfig {
	c.Args = append([]string(nil), c.Args...)
	c.Env = append([]string(nil), c.Env...)
	return c
}
