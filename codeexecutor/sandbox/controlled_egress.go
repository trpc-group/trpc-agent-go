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
	"crypto/rand"
	"encoding/hex"
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

const controlledEgressProxyProbeTimeout = 500 * time.Millisecond

const controlledEgressSetupTokenBytes = 16

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
		if isProxyEnvKey(k) {
			continue
		}
		filtered = append(filtered, kv)
	}
	filtered = append(filtered,
		"HTTP_PROXY="+proxyURL,
		"HTTPS_PROXY="+proxyURL,
		"http_proxy="+proxyURL,
		"https_proxy="+proxyURL,
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

func wrapControlledEgressSpec(
	plan resolvedNetworkPolicy,
	relayPath string,
	bwrapPath string,
	seccompFD int,
	mountProc bool,
	spec codeexecutor.RunProgramSpec,
) (codeexecutor.RunProgramSpec, error) {
	if err := probeControlledEgressProxy(plan.unixPath); err != nil {
		return spec, err
	}
	if bwrapPath == "" {
		return spec, deniedf(
			ErrSetupFailed,
			"network",
			"",
			"controlled egress missing bubblewrap path",
		)
	}
	if seccompFD < 3 {
		return spec, deniedf(
			ErrSetupFailed,
			"network",
			"",
			"controlled egress missing workload seccomp fd",
		)
	}
	setupToken, err := newControlledEgressSetupToken()
	if err != nil {
		return spec, deniedf(
			ErrSetupFailed,
			"network",
			"",
			"generate controlled egress setup token: %v",
			err,
		)
	}
	wrapped := spec
	wrapped.Cmd = relayPath
	wrapped.Args = append([]string{
		"-unix", plan.unixPath,
		"-port", fmt.Sprintf("%d", plan.relayPort),
		"-bwrap", bwrapPath,
		"-seccomp-fd", fmt.Sprintf("%d", seccompFD),
		"-setup-token", setupToken,
		fmt.Sprintf("-mount-proc=%t", mountProc),
		"--",
		spec.Cmd,
	}, spec.Args...)
	return wrapped, nil
}

func newControlledEgressSetupToken() (string, error) {
	var token [controlledEgressSetupTokenBytes]byte
	if _, err := rand.Read(token[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(token[:]), nil
}

func controlledEgressSetupToken(args []string) string {
	for i := 0; i+1 < len(args); i++ {
		if args[i] == "-setup-token" {
			return args[i+1]
		}
	}
	return ""
}

func mapControlledEgressSetupExit(
	profile PermissionProfile,
	exitCode int,
	setupMarkerSeen bool,
) error {
	if profile.network.Mode != NetworkControlled {
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
	mu          sync.Mutex
	marker      string
	setupMarker bool
	tail        string
}

func newControlledEgressMarkerTracker(
	setupToken string,
) *controlledEgressMarkerTracker {
	marker := ""
	if setupToken != "" {
		marker = controlledegress.SetupErrorPrefix + setupToken + ":"
	}
	return &controlledEgressMarkerTracker{marker: marker}
}

func (t *controlledEgressMarkerTracker) Write(chunk []byte) (int, error) {
	if t == nil {
		return len(chunk), nil
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	combined := t.tail + string(chunk)
	if t.marker != "" && strings.Contains(combined, t.marker) {
		t.setupMarker = true
	}
	keep := len(t.marker) - 1
	if keep < 0 {
		keep = 0
	}
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

type processStderrSetupTracker struct {
	source io.ReadCloser
	reader *os.File
	writer *os.File
	done   chan struct{}

	markers controlledEgressMarkerTracker
}

func newProcessStderrSetupTracker(
	source io.ReadCloser,
	setupToken string,
) (*processStderrSetupTracker, error) {
	reader, writer, err := os.Pipe()
	if err != nil {
		return nil, err
	}
	return &processStderrSetupTracker{
		source:  source,
		reader:  reader,
		writer:  writer,
		done:    make(chan struct{}),
		markers: *newControlledEgressMarkerTracker(setupToken),
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
