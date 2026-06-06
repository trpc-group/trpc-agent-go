//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package skill

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
)

// SkillScopeMode controls how agent skills are shared.
type SkillScopeMode string

const (
	// SkillScopeNone leaves repository/publisher selection unscoped.
	SkillScopeNone SkillScopeMode = ""
	// SkillScopeApp shares skills across all users within the same app.
	SkillScopeApp SkillScopeMode = "app"
	// SkillScopeUser isolates skills by app and user.
	SkillScopeUser SkillScopeMode = "user"
)

// SkillScope identifies the app/user boundary for a skill repository view.
type SkillScope struct {
	AppName string
	UserID  string
}

// RepositoryProvider resolves the repository visible for a skill scope.
type RepositoryProvider interface {
	Repository(ctx context.Context, scope SkillScope) (Repository, error)
}

// RepositoryProviderFunc adapts a function into a RepositoryProvider.
type RepositoryProviderFunc func(ctx context.Context, scope SkillScope) (Repository, error)

// Repository implements RepositoryProvider.
func (f RepositoryProviderFunc) Repository(ctx context.Context, scope SkillScope) (Repository, error) {
	if f == nil {
		return nil, nil
	}
	return f(ctx, scope)
}

// NormalizeSkillScopeMode normalizes known string forms while preserving
// SkillScopeNone as the unscoped mode. Unknown non-empty modes fall back to
// app-level sharing to keep configuration typos conservative.
func NormalizeSkillScopeMode(mode SkillScopeMode) SkillScopeMode {
	switch SkillScopeMode(strings.ToLower(strings.TrimSpace(string(mode)))) {
	case SkillScopeUser:
		return SkillScopeUser
	case SkillScopeNone:
		return SkillScopeNone
	case SkillScopeApp:
		return SkillScopeApp
	default:
		return SkillScopeApp
	}
}

// NewSkillScope builds a scope from app/user values according to mode.
func NewSkillScope(mode SkillScopeMode, appName, userID string) (SkillScope, error) {
	mode = NormalizeSkillScopeMode(mode)
	scope := SkillScope{
		AppName: strings.TrimSpace(appName),
		UserID:  strings.TrimSpace(userID),
	}
	switch mode {
	case SkillScopeNone:
		return SkillScope{}, nil
	case SkillScopeApp:
		scope.UserID = ""
		if scope.AppName == "" {
			return SkillScope{}, errors.New("skill scope: appName is required")
		}
	case SkillScopeUser:
		if scope.AppName == "" {
			return SkillScope{}, errors.New("skill scope: appName is required")
		}
		if scope.UserID == "" {
			return SkillScope{}, errors.New("skill scope: userID is required")
		}
	}
	return scope, nil
}

// IsZero reports whether the scope was not explicitly set.
func (s SkillScope) IsZero() bool {
	return strings.TrimSpace(s.AppName) == "" && strings.TrimSpace(s.UserID) == ""
}

// ScopePathParts returns filesystem-safe path components for the scope.
func ScopePathParts(mode SkillScopeMode, scope SkillScope) ([]string, error) {
	mode = NormalizeSkillScopeMode(mode)
	normalized, err := NewSkillScope(mode, scope.AppName, scope.UserID)
	if err != nil {
		return nil, err
	}
	appPart := safeScopePart(normalized.AppName)
	switch mode {
	case SkillScopeApp:
		return []string{"apps", appPart}, nil
	case SkillScopeUser:
		return []string{"users", appPart, safeScopePart(normalized.UserID)}, nil
	default:
		return nil, fmt.Errorf("skill scope: unsupported mode %q", mode)
	}
}

func safeScopePart(value string) string {
	value = strings.TrimSpace(value)
	if isSafeScopePart(value) {
		return value
	}
	sum := sha256.Sum256([]byte(value))
	return "id-" + hex.EncodeToString(sum[:])[:16]
}

func isSafeScopePart(value string) bool {
	if value == "" || len(value) > 128 {
		return false
	}
	if value == "." || value == ".." || strings.Contains(value, "..") {
		return false
	}
	for _, r := range value {
		switch {
		case r >= 'a' && r <= 'z':
		case r >= 'A' && r <= 'Z':
		case r >= '0' && r <= '9':
		case r == '.', r == '_', r == '-':
		default:
			return false
		}
	}
	return true
}
