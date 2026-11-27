//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package envdprocess

import (
	"fmt"
	"strconv"
	"strings"
)

const closeStdinMinimumEnvdVersion = "0.5.2"

type clientOptions struct {
	envdVersion        string
	supportsCloseStdin bool
	stdoutCaptureLimit int
	stderrCaptureLimit int
}

// ClientOption configures process behavior shared by Start, Connect, and Run.
type ClientOption func(*clientOptions)

// WithEnvdVersion configures the envd version used for capability checks. An
// empty version leaves capabilities optimistic for E2B-compatible endpoints.
// A configured version older than 0.5.2 disables CloseStdin before an RPC is
// sent.
func WithEnvdVersion(version string) ClientOption {
	return func(options *clientOptions) {
		options.envdVersion = strings.TrimSpace(version)
	}
}

// WithStdoutCaptureLimit limits retained stdout to the first limit bytes.
// Zero leaves stdout capture unlimited. The response stream is always drained
// after the limit is reached, and Result.StdoutTruncated reports discarded
// bytes.
func WithStdoutCaptureLimit(limit int) ClientOption {
	return func(options *clientOptions) {
		options.stdoutCaptureLimit = limit
	}
}

// WithStderrCaptureLimit limits retained stderr to the first limit bytes.
// Zero leaves stderr capture unlimited. The response stream is always drained
// after the limit is reached, and Result.StderrTruncated reports discarded
// bytes.
func WithStderrCaptureLimit(limit int) ClientOption {
	return func(options *clientOptions) {
		options.stderrCaptureLimit = limit
	}
}

func negativeCaptureLimitError(stream string, limit int) error {
	return fmt.Errorf(
		"envd process: %s capture limit must not be negative: %d",
		stream,
		limit,
	)
}

// LaunchOption configures one process launched by Start or Run.
type LaunchOption func(*launchOptions)

type launchOptions struct {
	tag string
}

// WithTag associates tag with a process launched by Start or Run. Tags are
// not unique in envd; callers that later select or clean up by tag must ensure
// uniqueness within the sandbox. Run generates a private unique tag when this
// option is omitted so it can attempt cleanup before a PID is observed.
func WithTag(tag string) LaunchOption {
	return func(options *launchOptions) {
		options.tag = tag
	}
}

func newClientOptions(options []ClientOption) (clientOptions, error) {
	applied := clientOptions{supportsCloseStdin: true}
	for _, option := range options {
		if option != nil {
			option(&applied)
		}
	}
	if applied.stdoutCaptureLimit < 0 {
		return clientOptions{}, negativeCaptureLimitError(
			"stdout", applied.stdoutCaptureLimit,
		)
	}
	if applied.stderrCaptureLimit < 0 {
		return clientOptions{}, negativeCaptureLimitError(
			"stderr", applied.stderrCaptureLimit,
		)
	}
	if applied.envdVersion != "" {
		supported, err := versionAtLeast(
			applied.envdVersion, closeStdinMinimumEnvdVersion,
		)
		if err != nil {
			return clientOptions{}, fmt.Errorf(
				"envd process: invalid envd version %q: %w",
				applied.envdVersion,
				err,
			)
		}
		applied.supportsCloseStdin = supported
	}
	return applied, nil
}

func newLaunchOptions(options []LaunchOption) launchOptions {
	var applied launchOptions
	for _, option := range options {
		if option != nil {
			option(&applied)
		}
	}
	return applied
}

func versionAtLeast(version string, minimum string) (bool, error) {
	parsedVersion, versionPrerelease, err := parseVersion(version)
	if err != nil {
		return false, err
	}
	parsedMinimum, minimumPrerelease, err := parseVersion(minimum)
	if err != nil {
		return false, err
	}
	for i := range parsedVersion {
		if parsedVersion[i] != parsedMinimum[i] {
			return parsedVersion[i] > parsedMinimum[i], nil
		}
	}
	if versionPrerelease != minimumPrerelease {
		return !versionPrerelease, nil
	}
	return true, nil
}

func parseVersion(version string) ([3]int, bool, error) {
	var parsed [3]int
	version = strings.TrimPrefix(strings.TrimSpace(version), "v")
	if index := strings.IndexByte(version, '+'); index >= 0 {
		version = version[:index]
	}
	prerelease := false
	if index := strings.IndexByte(version, '-'); index >= 0 {
		prerelease = true
		version = version[:index]
	}
	parts := strings.Split(version, ".")
	if len(parts) != len(parsed) {
		return parsed, false, fmt.Errorf("want major.minor.patch")
	}
	for i, part := range parts {
		value, err := strconv.Atoi(part)
		if err != nil || value < 0 {
			return parsed, false, fmt.Errorf("want numeric major.minor.patch")
		}
		parsed[i] = value
	}
	return parsed, prerelease, nil
}
