//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package replaytest

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/memory"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

// Harness executes cases and compares every backend with the first backend.
type Harness struct {
	Backends   []Backend
	Normalizer Normalizer
	Allowed    []AllowedDiff
}

// CaptureOptions controls how one backend snapshot is normalized.
type CaptureOptions struct {
	Normalizer             Normalizer
	OrderEventsByTimestamp bool
	UnorderedMemories      bool
}

// Run executes one case with isolated backend instances supplied by the caller.
func (h Harness) Run(ctx context.Context, replayCase Case) (CaseReport, error) {
	if err := validateCase(replayCase); err != nil {
		return CaseReport{}, err
	}
	if len(h.Backends) < 2 {
		return CaseReport{}, fmt.Errorf("replay harness requires at least two backends")
	}
	if err := validateAllowedDiffs(h.Allowed); err != nil {
		return CaseReport{}, err
	}
	if err := validateBackends(h.Backends); err != nil {
		return CaseReport{}, err
	}
	if err := validateRequiredCapabilities(replayCase, h.Backends); err != nil {
		return CaseReport{}, err
	}
	normalizer := h.Normalizer
	if normalizer.VolatilePayloadKeys == nil {
		normalizer = DefaultNormalizer()
	}
	report := CaseReport{
		Name: replayCase.Name, SessionID: h.Backends[0].SessionKey.SessionID,
		RequiredCapabilities: append([]string(nil), replayCase.RequiredCapabilities...),
		SkippedBackends:      make(map[string][]string),
		Capabilities:         make(map[string]map[string]Capability, len(h.Backends)),
		Diffs:                make([]Diff, 0),
	}
	type capturedSnapshot struct {
		backendName string
		snapshot    Snapshot
	}
	snapshots := make([]capturedSnapshot, 0, len(h.Backends))
	for i, backend := range h.Backends {
		report.Backends = append(report.Backends, backend.Name)
		report.Capabilities[backend.Name] = cloneCapabilities(backend.Capabilities)
		unsupported := unsupportedRequiredCapabilities(replayCase, backend)
		if len(unsupported) > 0 {
			if i == 0 {
				return report, fmt.Errorf(
					"baseline backend %q does not support required capabilities %v",
					backend.Name, unsupported,
				)
			}
			report.SkippedBackends[backend.Name] = unsupported
			continue
		}
		if err := replayCase.Run(ctx, backend); err != nil {
			return report, fmt.Errorf("run %q on %s: %w", replayCase.Name, backend.Name, err)
		}
		snapshot, err := Capture(ctx, backend, CaptureOptions{
			Normalizer:             normalizer,
			OrderEventsByTimestamp: replayCase.OrderEventsByTimestamp,
			UnorderedMemories:      replayCase.UnorderedMemories,
		})
		if err != nil {
			return report, fmt.Errorf("capture %q from %s: %w", replayCase.Name, backend.Name, err)
		}
		snapshots = append(snapshots, capturedSnapshot{backendName: backend.Name, snapshot: snapshot})
	}
	if len(snapshots) < 2 {
		report.Inconclusive = true
		return report, nil
	}
	for i := 1; i < len(snapshots); i++ {
		diffs, err := Compare(
			replayCase.Name,
			snapshots[0].backendName,
			snapshots[i].backendName,
			snapshots[0].snapshot,
			snapshots[i].snapshot,
			h.Allowed,
		)
		if err != nil {
			return report, err
		}
		report.Diffs = append(report.Diffs, diffs...)
	}
	return report, nil
}

func unsupportedRequiredCapabilities(replayCase Case, backend Backend) []string {
	var unsupported []string
	for _, name := range replayCase.RequiredCapabilities {
		if !backend.Capabilities[name].Supported {
			unsupported = append(unsupported, name)
		}
	}
	return unsupported
}

func validateCase(replayCase Case) error {
	if strings.TrimSpace(replayCase.Name) == "" || replayCase.Run == nil {
		return fmt.Errorf("replay case requires name and run function")
	}
	seen := make(map[string]struct{}, len(replayCase.RequiredCapabilities))
	for _, capability := range replayCase.RequiredCapabilities {
		if strings.TrimSpace(capability) == "" {
			return fmt.Errorf("replay case %q has an empty required capability", replayCase.Name)
		}
		if _, exists := seen[capability]; exists {
			return fmt.Errorf("replay case %q has duplicate required capability %q", replayCase.Name, capability)
		}
		seen[capability] = struct{}{}
	}
	return nil
}

func validateRequiredCapabilities(replayCase Case, backends []Backend) error {
	for _, backend := range backends {
		for _, name := range replayCase.RequiredCapabilities {
			if _, declared := backend.Capabilities[name]; !declared {
				return fmt.Errorf(
					"backend %q must declare required capability %q for replay case %q",
					backend.Name, name, replayCase.Name,
				)
			}
		}
	}
	return nil
}

// Capture loads and normalizes the replay-visible state of one backend.
func Capture(ctx context.Context, backend Backend, options CaptureOptions) (Snapshot, error) {
	if err := validateBackend(backend); err != nil {
		return Snapshot{}, err
	}
	normalizer := options.Normalizer
	if normalizer.VolatilePayloadKeys == nil {
		normalizer = DefaultNormalizer()
	}
	sess, memories, err := loadBackend(ctx, backend)
	if err != nil {
		return Snapshot{}, err
	}
	return normalizer.normalize(
		sess,
		memories,
		backend.Capabilities,
		options.OrderEventsByTimestamp,
		options.UnorderedMemories,
	)
}

// Supports reports whether a backend advertises one capability.
// An omitted capability defaults to supported for backward compatibility.
func Supports(backend Backend, capability string) bool {
	return capabilitySupported(backend.Capabilities, capability)
}

func loadBackend(ctx context.Context, backend Backend) (*session.Session, []*memory.Entry, error) {
	if backend.Load != nil {
		return backend.Load(ctx, backend)
	}
	sess, err := backend.Session.GetSession(ctx, backend.SessionKey)
	if err != nil {
		return nil, nil, err
	}
	var memories []*memory.Entry
	if capabilitySupported(backend.Capabilities, CapabilityMemory) {
		memories, err = backend.Memory.ReadMemories(ctx, memory.UserKey{
			AppName: backend.SessionKey.AppName,
			UserID:  backend.SessionKey.UserID,
		}, 0)
		if err != nil {
			return nil, nil, err
		}
	}
	return sess, memories, nil
}

func validateBackends(backends []Backend) error {
	names := make(map[string]struct{}, len(backends))
	for _, backend := range backends {
		if err := validateBackend(backend); err != nil {
			return err
		}
		if _, exists := names[backend.Name]; exists {
			return fmt.Errorf("duplicate backend name %q", backend.Name)
		}
		names[backend.Name] = struct{}{}
	}
	return nil
}

func validateBackend(backend Backend) error {
	if strings.TrimSpace(backend.Name) == "" || backend.Session == nil {
		return fmt.Errorf("invalid backend configuration")
	}
	if backend.Load == nil && capabilitySupported(backend.Capabilities, CapabilityMemory) && backend.Memory == nil {
		return fmt.Errorf("backend %q requires a memory service", backend.Name)
	}
	for name, capability := range backend.Capabilities {
		if strings.TrimSpace(name) == "" {
			return fmt.Errorf("backend %q has an empty capability name", backend.Name)
		}
		if !capability.Supported && strings.TrimSpace(capability.Reason) == "" {
			return fmt.Errorf("backend %q unsupported capability %q requires a reason", backend.Name, name)
		}
	}
	return nil
}

func cloneCapabilities(capabilities map[string]Capability) map[string]Capability {
	result := make(map[string]Capability, len(capabilities))
	for name, capability := range capabilities {
		result[name] = capability
	}
	return result
}

// HasUnexpectedDiff reports whether a case contains a non-allowlisted mismatch.
func HasUnexpectedDiff(report CaseReport) bool {
	if report.Inconclusive {
		return true
	}
	for _, diff := range report.Diffs {
		if !diff.Allowed {
			return true
		}
	}
	for backend, names := range report.SkippedBackends {
		capabilities, exists := report.Capabilities[backend]
		if !exists {
			return true
		}
		for _, name := range names {
			capability, exists := capabilities[name]
			if !exists || capability.Supported || !capability.AllowedDiff {
				return true
			}
		}
	}
	return false
}

// WriteReport atomically writes an indented JSON report.
func WriteReport(path string, report Report) error {
	if path == "" {
		return fmt.Errorf("report path is empty")
	}
	if report.Version == 0 {
		report.Version = 1
	}
	raw, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal replay report: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return fmt.Errorf("create report directory: %w", err)
	}
	temporary := path + ".tmp"
	if err := os.WriteFile(temporary, append(raw, '\n'), 0o600); err != nil {
		return fmt.Errorf("write replay report: %w", err)
	}
	if err := os.Rename(temporary, path); err != nil {
		return fmt.Errorf("publish replay report: %w", err)
	}
	return nil
}
