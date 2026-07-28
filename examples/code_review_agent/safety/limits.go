//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package safety

import (
	"strings"
	"time"
)

// Limits controls sandbox execution and artifact bounds.
type Limits struct {
	Timeout               time.Duration
	MaxStdoutBytes        int
	MaxStderrBytes        int
	MaxArtifactFiles      int
	MaxArtifactFileBytes  int64
	MaxArtifactTotalBytes int64
	EnvWhitelist          []string
}

// DefaultLimits returns production-safe defaults.
func DefaultLimits() Limits {
	return Limits{
		Timeout:               60 * time.Second,
		MaxStdoutBytes:        1 << 20,
		MaxStderrBytes:        1 << 20,
		MaxArtifactFiles:      32,
		MaxArtifactFileBytes:  2 << 20,
		MaxArtifactTotalBytes: 8 << 20,
		EnvWhitelist: []string{
			"PATH", "HOME", "TMPDIR", "TMP", "TEMP",
			"GOROOT", "GOPATH", "GOCACHE", "GOMODCACHE",
			"CGO_ENABLED", "GO111MODULE", "GOTOOLCHAIN",
			"LANG", "LC_ALL", "TERM",
			"REVIEW_DIFF_PATH", "REVIEW_REPO_PATH", "REVIEW_OUT_DIR",
		},
	}
}

// IsEnvAllowed reports whether an env key may be forwarded.
func (l Limits) IsEnvAllowed(key string) bool {
	upper := strings.ToUpper(key)
	deny := []string{"API_KEY", "PASSWORD", "TOKEN", "SECRET", "PRIVATE_KEY"}
	for _, d := range deny {
		if strings.Contains(upper, d) {
			return false
		}
	}
	for _, k := range l.EnvWhitelist {
		if k == key {
			return true
		}
	}
	return false
}

// MergeLimits starts from DefaultLimits and overlays each non-zero / non-nil
// field from partial so callers can set timeout, output, artifact, or env
// limits independently without wiping the rest.
func MergeLimits(partial Limits) Limits {
	out := DefaultLimits()
	if partial.Timeout > 0 {
		out.Timeout = partial.Timeout
	}
	if partial.MaxStdoutBytes > 0 {
		out.MaxStdoutBytes = partial.MaxStdoutBytes
	}
	if partial.MaxStderrBytes > 0 {
		out.MaxStderrBytes = partial.MaxStderrBytes
	}
	if partial.MaxArtifactFiles > 0 {
		out.MaxArtifactFiles = partial.MaxArtifactFiles
	}
	if partial.MaxArtifactFileBytes > 0 {
		out.MaxArtifactFileBytes = partial.MaxArtifactFileBytes
	}
	if partial.MaxArtifactTotalBytes > 0 {
		out.MaxArtifactTotalBytes = partial.MaxArtifactTotalBytes
	}
	if partial.EnvWhitelist != nil {
		out.EnvWhitelist = append([]string(nil), partial.EnvWhitelist...)
	}
	return out
}
