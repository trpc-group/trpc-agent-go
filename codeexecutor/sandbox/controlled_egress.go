//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package sandbox

import (
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/codeexecutor"
	"trpc.group/trpc-go/trpc-agent-go/codeexecutor/sandbox/controlledegress"
)

const (
	envControlledEgressUnix = "TRPC_AGENT_CONTROLLED_EGRESS"

	controlledEgressProxyProbeTimeout = 500 * time.Millisecond
)

var proxyEnvKeys = []string{
	"HTTP_PROXY", "HTTPS_PROXY", "http_proxy", "https_proxy",
	"ALL_PROXY", "all_proxy", "NO_PROXY", "no_proxy",
}

// WithControlledEgressRelayPath sets the in-sandbox egress-relay helper binary.
// Required for NetworkControlled on Linux.
func WithControlledEgressRelayPath(path string) Option {
	return func(r *Runtime) {
		r.egressRelayPath = path
	}
}

// WithControlledEgressProxy enables NetworkControlled and sets the host proxy
// Unix socket used for controlled egress.
func (p PermissionProfile) WithControlledEgressProxy(
	endpoint ControlledEgressProxy,
) PermissionProfile {
	p.network.Mode = NetworkControlled
	p.controlledEgress = endpoint
	return p
}

func (c ControlledEgressProxy) effectiveRelayPort() int {
	if c.RelayPort > 0 {
		return c.RelayPort
	}
	return controlledegress.DefaultRelayPort
}

func (c ControlledEgressProxy) validate() error {
	if c.UnixPath == "" {
		return deniedf(
			ErrPolicyViolation,
			"network",
			"",
			"controlled egress requires UnixPath",
		)
	}
	if !filepath.IsAbs(c.UnixPath) {
		return deniedf(
			ErrPolicyViolation,
			"network",
			c.UnixPath,
			"controlled egress UnixPath must be absolute",
		)
	}
	if c.RelayPort < 0 || c.RelayPort > 65535 {
		return deniedf(
			ErrPolicyViolation,
			"network",
			"",
			"controlled egress RelayPort must be between 0 and 65535",
		)
	}
	return nil
}

func applyControlledEgressEnv(
	env []string,
	plan resolvedNetworkPolicy,
) []string {
	proxyURL := fmt.Sprintf("http://127.0.0.1:%d", plan.relayPort)
	filtered := make([]string, 0, len(env)+8)
	for _, kv := range env {
		k, _, ok := strings.Cut(kv, "=")
		if !ok {
			continue
		}
		if isProxyEnvKey(k) || isControlledEgressOwnedEnvKey(k) {
			continue
		}
		filtered = append(filtered, kv)
	}
	filtered = append(filtered,
		"HTTP_PROXY="+proxyURL,
		"HTTPS_PROXY="+proxyURL,
		"http_proxy="+proxyURL,
		"https_proxy="+proxyURL,
		envControlledEgressUnix+"=unix://"+plan.unixPath,
	)
	return filtered
}

func isProxyEnvKey(k string) bool {
	for _, pk := range proxyEnvKeys {
		if k == pk {
			return true
		}
	}
	return false
}

func isControlledEgressOwnedEnvKey(key string) bool {
	switch key {
	case envControlledEgressUnix:
		return true
	default:
		return false
	}
}

// probeControlledEgressProxy ensures the caller-owned host proxy UDS is
// accepting connections before entering the sandbox.
func probeControlledEgressProxy(unixPath string) error {
	if unixPath == "" {
		return deniedf(ErrPolicyViolation, "network", "", "controlled egress missing unix path")
	}
	conn, err := net.DialTimeout("unix", unixPath, controlledEgressProxyProbeTimeout)
	if err != nil {
		return deniedf(
			ErrSetupFailed,
			"network",
			unixPath,
			"controlled egress proxy unreachable: %v",
			err,
		)
	}
	_ = conn.Close()
	return nil
}

func (r *Runtime) wrapControlledEgressSpec(
	plan resolvedNetworkPolicy,
	relayPath string,
	spec codeexecutor.RunProgramSpec,
) (codeexecutor.RunProgramSpec, error) {
	if err := probeControlledEgressProxy(plan.unixPath); err != nil {
		return spec, err
	}
	if plan.unixPath == "" {
		return spec, deniedf(ErrPolicyViolation, "network", "", "controlled egress missing unix path")
	}
	wrapped := spec
	wrapped.Cmd = relayPath
	wrapped.Args = append([]string{
		"-unix", plan.unixPath,
		"-port", fmt.Sprintf("%d", plan.relayPort),
		"--",
		spec.Cmd,
	}, spec.Args...)
	return wrapped, nil
}

// mapControlledEgressSetupExit turns egress-relay setup failures into
// ErrSetupFailed. User commands may return the same exit codes, so the trusted
// relay's post-command marker takes precedence over setup-like guest stderr.
func mapControlledEgressSetupExit(
	profile PermissionProfile,
	exitCode int,
	setupMarkerSeen bool,
	userExitMarkerSeen bool,
) error {
	if profile.network.Mode != NetworkControlled {
		return nil
	}
	if userExitMarkerSeen {
		return nil
	}
	if !setupMarkerSeen {
		return nil
	}
	switch exitCode {
	case controlledegress.ExitSetupFailed:
		return deniedf(
			ErrSetupFailed,
			"network",
			"",
			"controlled egress relay setup failed (exit %d)",
			exitCode,
		)
	case controlledegress.ExitUsageError:
		return deniedf(
			ErrSetupFailed,
			"network",
			"",
			"controlled egress relay usage error (exit %d)",
			exitCode,
		)
	default:
		return nil
	}
}

type controlledEgressMarkerTracker struct {
	mu             sync.Mutex
	setupMarker    bool
	userExitMarker bool
	tail           string
}

func (t *controlledEgressMarkerTracker) Write(chunk []byte) (int, error) {
	if t == nil {
		return len(chunk), nil
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	combined := t.tail + string(chunk)
	if strings.Contains(combined, controlledegress.SetupErrorPrefix) {
		t.setupMarker = true
	}
	if strings.Contains(combined, controlledegress.UserExitPrefix) {
		t.userExitMarker = true
	}
	keep := max(
		len(controlledegress.SetupErrorPrefix),
		len(controlledegress.UserExitPrefix),
	) - 1
	if len(combined) > keep {
		combined = combined[len(combined)-keep:]
	}
	t.tail = combined
	return len(chunk), nil
}

func (t *controlledEgressMarkerTracker) setupMarkerSeen() bool {
	if t == nil {
		return false
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.setupMarker
}

func (t *controlledEgressMarkerTracker) userExitMarkerSeen() bool {
	if t == nil {
		return false
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.userExitMarker
}

type processStderrSetupTracker struct {
	source io.ReadCloser
	reader *os.File
	writer *os.File
	done   chan struct{}

	markers controlledEgressMarkerTracker
}

func newProcessStderrSetupTracker(
	source io.ReadCloser,
) (*processStderrSetupTracker, error) {
	reader, writer, err := os.Pipe()
	if err != nil {
		return nil, err
	}
	return &processStderrSetupTracker{
		source: source,
		reader: reader,
		writer: writer,
		done:   make(chan struct{}),
	}, nil
}

func (t *processStderrSetupTracker) start() {
	go func() {
		defer close(t.done)
		defer t.source.Close()
		defer t.writer.Close()
		buf := make([]byte, 32<<10)
		for {
			n, err := t.source.Read(buf)
			if n > 0 {
				_, _ = t.markers.Write(buf[:n])
				if _, writeErr := t.writer.Write(buf[:n]); writeErr != nil {
					return
				}
			}
			if err != nil {
				return
			}
		}
	}()
}

func (t *processStderrSetupTracker) setupMarkerSeen() bool {
	if t == nil {
		return false
	}
	<-t.done
	return t.markers.setupMarkerSeen()
}

func (t *processStderrSetupTracker) userExitMarkerSeen() bool {
	if t == nil {
		return false
	}
	<-t.done
	return t.markers.userExitMarkerSeen()
}
