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
	"context"
	"errors"
	"fmt"
	"runtime"
	"strings"
)

// PreflightStatus describes whether managed sandbox backend dependencies are
// ready for execution.
type PreflightStatus string

const (
	// PreflightReady means managed backend preflight succeeded.
	PreflightReady PreflightStatus = "ready"
	// PreflightFailed means managed backend preflight ran and failed.
	PreflightFailed PreflightStatus = "failed"
	// PreflightNotRequired means the active profile does not need a managed
	// OS sandbox backend.
	PreflightNotRequired PreflightStatus = "not-required"
	// PreflightUnsupported means the profile or platform cannot use a managed
	// OS sandbox backend.
	PreflightUnsupported PreflightStatus = "unsupported"
)

// FileSystemSandboxType is the high-level filesystem sandbox mode reported by
// Explain. It is a status label, not a complete grant list.
type FileSystemSandboxType string

const (
	// FileSystemSandboxWorkspaceWrite means the managed profile grants write
	// access to workspace special paths.
	FileSystemSandboxWorkspaceWrite FileSystemSandboxType = "workspace-write"
	// FileSystemSandboxReadOnly means the managed profile does not grant
	// workspace special-path writes.
	FileSystemSandboxReadOnly FileSystemSandboxType = "read-only"
	// FileSystemSandboxDisabled means OS sandbox enforcement is off.
	FileSystemSandboxDisabled FileSystemSandboxType = "disabled"
	// FileSystemSandboxExternal means an external sandbox is expected to
	// enforce filesystem policy.
	FileSystemSandboxExternal FileSystemSandboxType = "external"
)

// ExplainReport is a high-level sandbox status summary for operators.
// It is intentionally small: it reports backend selection, filesystem sandbox
// type, network mode, and preflight readiness. It is not a full policy dump.
type ExplainReport struct {
	RequestedBackend  BackendType
	ResolvedBackend   BackendType
	FileSystemSandbox FileSystemSandboxType
	NetworkMode       NetworkMode
	PreflightStatus   PreflightStatus
	// PreflightError is a short operator-facing summary when PreflightStatus
	// is failed or unsupported after a managed backend probe. It includes the
	// error kind, backend name, and a sanitized cause. Probe stderr, host
	// paths from probe output, and environment values are omitted. It must
	// not be treated as a stable machine-readable protocol.
	PreflightError string
}

// Explain reports high-level sandbox status for the runtime.
//
// It reuses the same normalized PermissionProfile and managed-backend
// preflight paths that execution uses (linuxPreflight / macosPreflight).
// Explain never runs a caller command, never acquires a workspace run lock,
// and never creates a workspace. On managed profiles it may run the same
// short backend probe used by execution (for example /bin/true under
// bubblewrap) and cache the result on the Runtime.
//
// When managed preflight fails, Explain still returns a populated report so
// callers can inspect the configured status, and also returns the preflight
// error.
func (r *Runtime) Explain(ctx context.Context) (ExplainReport, error) {
	if r == nil {
		return ExplainReport{}, errors.New("nil sandbox runtime")
	}
	if err := ctx.Err(); err != nil {
		return ExplainReport{}, err
	}

	profile := normalizeProfile(r.profile)
	report := ExplainReport{
		RequestedBackend:  r.backend,
		ResolvedBackend:   resolveExplainBackend(r.backend),
		FileSystemSandbox: fileSystemSandboxType(profile),
		NetworkMode:       profile.network.Mode,
	}

	switch profile.enforcement() {
	case enforcementDisabled:
		report.PreflightStatus = PreflightNotRequired
		return report, nil
	case enforcementExternal:
		report.PreflightStatus = PreflightUnsupported
		return report, nil
	default:
		err := r.explainManagedPreflight(ctx)
		if err != nil {
			report.PreflightStatus = preflightStatusFromError(err)
			report.PreflightError = summarizePreflightError(err)
			return report, err
		}
		report.PreflightStatus = PreflightReady
		return report, nil
	}
}

// String returns a concise human-readable status summary. The format is for
// diagnostics only and is not a stable machine protocol.
func (report ExplainReport) String() string {
	backend := string(report.RequestedBackend)
	if backend == "" {
		backend = string(BackendAuto)
	}
	resolved := string(report.ResolvedBackend)
	if resolved == "" {
		resolved = backend
	}
	backendLine := backend
	if resolved != backend {
		backendLine = backend + " -> " + resolved
	}

	preflight := string(report.PreflightStatus)
	if preflight == "" {
		preflight = string(PreflightNotRequired)
	}
	if report.PreflightError != "" &&
		(report.PreflightStatus == PreflightFailed ||
			report.PreflightStatus == PreflightUnsupported) {
		preflight = string(report.PreflightStatus) + ": " + report.PreflightError
	}

	fs := report.FileSystemSandbox
	if fs == "" {
		fs = FileSystemSandboxDisabled
	}
	network := string(report.NetworkMode)
	if network == "" {
		network = string(NetworkRestricted)
	}

	return fmt.Sprintf(
		"Sandbox\n  backend:    %s\n  filesystem: %s\n  network:    %s\n  preflight:  %s",
		backendLine,
		fs,
		network,
		preflight,
	)
}

func resolveExplainBackend(requested BackendType) BackendType {
	if requested != "" && requested != BackendAuto {
		return requested
	}
	switch runtime.GOOS {
	case "linux":
		return BackendLinuxBubblewrap
	case "darwin":
		return BackendMacOSSandboxExec
	default:
		return BackendAuto
	}
}

func fileSystemSandboxType(profile PermissionProfile) FileSystemSandboxType {
	switch profile.enforcement() {
	case enforcementDisabled:
		return FileSystemSandboxDisabled
	case enforcementExternal:
		return FileSystemSandboxExternal
	default:
		if hasWorkspaceWrite(profile) {
			return FileSystemSandboxWorkspaceWrite
		}
		return FileSystemSandboxReadOnly
	}
}

func hasWorkspaceWrite(profile PermissionProfile) bool {
	for _, rule := range profile.fileSystem.Rules {
		if rule.Kind != ruleSpecial || rule.Access != accessWrite {
			continue
		}
		switch rule.Special {
		case specialWorkspace, specialWork, specialHome, specialTmp,
			specialRuns, specialOut, specialSkills:
			return true
		}
	}
	return false
}

func preflightStatusFromError(err error) PreflightStatus {
	if err == nil {
		return PreflightReady
	}
	if isKind(err, ErrUnsupportedBackend) {
		return PreflightUnsupported
	}
	return PreflightFailed
}

func summarizePreflightError(err error) string {
	if err == nil {
		return ""
	}
	var se *sandboxError
	if errors.As(err, &se) && se != nil {
		parts := []string{string(se.Kind)}
		if se.Backend != "" {
			parts = append(parts, "backend="+se.Backend)
		}
		if cause := sanitizedPreflightCause(se.Err); cause != "" {
			parts = append(parts, cause)
		}
		return truncateExplainText(strings.Join(parts, " "))
	}
	return truncateExplainText(firstLine(err.Error()))
}

// sanitizedPreflightCause keeps a short operator-facing cause. Probe errors
// wrap the exec failure and attach stderr; Unwrap drops that attached output
// so host paths and mount details do not leak into Explain.
func sanitizedPreflightCause(err error) string {
	if err == nil {
		return ""
	}
	if unwrapped := errors.Unwrap(err); unwrapped != nil {
		err = unwrapped
	}
	return firstLine(err.Error())
}

func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = strings.TrimSpace(s[:i])
	}
	return s
}

func truncateExplainText(s string) string {
	const maxLen = 200
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}
