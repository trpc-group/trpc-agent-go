//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// Package safety provides a thin pre-execution Tool Safety Guard that
// implements tool.PermissionPolicy for issue #2002.
//
// It reuses internal/shellsafe for command structure checks and fails closed
// when a command cannot be parsed or when policy overlays omit deny lists.
package safety

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"gopkg.in/yaml.v3"

	"trpc.group/trpc-go/trpc-agent-go/internal/shellsafe"
)

// Policy is the declarative configuration for Guard.
//
// LoadPolicyFile starts from DefaultPolicy and overlays values from the file.
// Omitted deny lists therefore keep safe defaults (fail-closed), instead of
// silently disabling denials — a common failure mode called out in reviews of
// competing #2002 implementations.
type Policy struct {
	// AllowedCommands is passed to shellsafe allow matching (strict).
	AllowedCommands []string `json:"allowed_commands" yaml:"allowed_commands"`
	// DeniedCommands is passed to shellsafe deny matching (basename-friendly).
	DeniedCommands []string `json:"denied_commands" yaml:"denied_commands"`
	// DeniedPaths blocks path-like argv tokens that match or contain these markers.
	DeniedPaths []string `json:"denied_paths" yaml:"denied_paths"`
	// AllowedHosts is the network allowlist.
	// Bare entries match the host exactly. Leading-dot entries (".example.com")
	// opt into suffix matching for every subdomain of that domain.
	AllowedHosts []string `json:"allowed_hosts" yaml:"allowed_hosts"`
	// AllowedEnvVars, when non-empty, restricts env overrides in tool args.
	AllowedEnvVars []string `json:"allowed_env_vars" yaml:"allowed_env_vars"`
	// AskCommands triggers PermissionActionAsk when the executable basename matches.
	AskCommands []string `json:"ask_commands" yaml:"ask_commands"`
	// MaxTimeoutSeconds is advisory metadata surfaced in reports (enforcement
	// remains with workspaceexec/hostexec/codeexecutor).
	MaxTimeoutSeconds int `json:"max_timeout_seconds" yaml:"max_timeout_seconds"`
	// MaxOutputBytes is advisory metadata surfaced in reports.
	MaxOutputBytes int `json:"max_output_bytes" yaml:"max_output_bytes"`
	// HostExecRequiresAsk forces ask for hostexec tools even when the command
	// would otherwise allow.
	HostExecRequiresAsk bool `json:"host_exec_requires_ask" yaml:"host_exec_requires_ask"`
}

// DefaultPolicy returns a fail-closed baseline covering the issue's must-catch
// classes: dangerous deletion, credential paths, and common network clients.
func DefaultPolicy() Policy {
	return Policy{
		DeniedCommands: []string{
			"rm", "rmdir", "del", "rd",
			"dd", "mkfs", "fdisk",
			"chmod", "chown",
		},
		DeniedPaths: []string{
			"/.ssh", "/.gnupg", "/.aws",
			".env", "id_rsa", "id_ed25519",
			"/etc/shadow", "/etc/passwd",
		},
		AllowedHosts: []string{
			"api.github.com",
			"proxy.golang.org",
			"sum.golang.org",
			"storage.googleapis.com",
		},
		// Keep only locale/terminal hints. Do not allow PATH / HOME / GOROOT /
		// GOPATH here: tool-arg overrides of those can redirect which binary
		// runs after Guard allows a scanned command name.
		AllowedEnvVars: []string{
			"LANG", "LC_ALL", "TERM",
		},
		AskCommands: []string{
			"npm", "pnpm", "yarn", "pip", "pip3",
			"go", "apt", "apt-get", "yum", "dnf", "brew",
			"cargo", "gem",
		},
		MaxTimeoutSeconds:   60,
		MaxOutputBytes:      1 << 20,
		HostExecRequiresAsk: true,
	}
}

// LoadPolicyFile reads JSON or YAML policy. Extension selects the decoder.
// The result is DefaultPolicy overlaid with file contents.
// On read failure it still returns DefaultPolicy alongside the error so a
// careless caller that ignores err does not get an empty fail-open policy.
func LoadPolicyFile(path string) (Policy, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return DefaultPolicy(), fmt.Errorf("safety: read policy %q: %w", path, err)
	}
	return ParsePolicy(data, path)
}

// ParsePolicy decodes policy bytes. name is used only to pick JSON vs YAML
// (".json" suffix → JSON; otherwise YAML). Unknown keys are rejected so a
// typo cannot silently drop a deny list.
func ParsePolicy(data []byte, name string) (Policy, error) {
	base := DefaultPolicy()
	var overlay policyOverlay
	var err error
	lower := strings.ToLower(name)
	if strings.HasSuffix(lower, ".json") {
		dec := json.NewDecoder(bytes.NewReader(data))
		dec.DisallowUnknownFields()
		err = dec.Decode(&overlay)
	} else {
		dec := yaml.NewDecoder(bytes.NewReader(data))
		dec.KnownFields(true)
		err = dec.Decode(&overlay)
		if errors.Is(err, io.EOF) {
			err = nil
		}
	}
	if err != nil {
		return DefaultPolicy(), fmt.Errorf("safety: parse policy: %w", err)
	}
	return overlay.apply(base), nil
}

// policyOverlay uses pointers so callers can distinguish "omitted" from
// "explicitly empty". Omitted fields keep DefaultPolicy values.
type policyOverlay struct {
	AllowedCommands     *[]string `json:"allowed_commands" yaml:"allowed_commands"`
	DeniedCommands      *[]string `json:"denied_commands" yaml:"denied_commands"`
	DeniedPaths         *[]string `json:"denied_paths" yaml:"denied_paths"`
	AllowedHosts        *[]string `json:"allowed_hosts" yaml:"allowed_hosts"`
	AllowedEnvVars      *[]string `json:"allowed_env_vars" yaml:"allowed_env_vars"`
	AskCommands         *[]string `json:"ask_commands" yaml:"ask_commands"`
	MaxTimeoutSeconds   *int      `json:"max_timeout_seconds" yaml:"max_timeout_seconds"`
	MaxOutputBytes      *int      `json:"max_output_bytes" yaml:"max_output_bytes"`
	HostExecRequiresAsk *bool     `json:"host_exec_requires_ask" yaml:"host_exec_requires_ask"`
}

func (o policyOverlay) apply(base Policy) Policy {
	if o.AllowedCommands != nil {
		base.AllowedCommands = cleanStrings(*o.AllowedCommands)
	}
	if o.DeniedCommands != nil {
		base.DeniedCommands = cleanStrings(*o.DeniedCommands)
	}
	if o.DeniedPaths != nil {
		base.DeniedPaths = cleanStrings(*o.DeniedPaths)
	}
	if o.AllowedHosts != nil {
		base.AllowedHosts = cleanStrings(*o.AllowedHosts)
	}
	if o.AllowedEnvVars != nil {
		base.AllowedEnvVars = cleanStrings(*o.AllowedEnvVars)
	}
	if o.AskCommands != nil {
		base.AskCommands = cleanStrings(*o.AskCommands)
	}
	if o.MaxTimeoutSeconds != nil {
		base.MaxTimeoutSeconds = *o.MaxTimeoutSeconds
	}
	if o.MaxOutputBytes != nil {
		base.MaxOutputBytes = *o.MaxOutputBytes
	}
	if o.HostExecRequiresAsk != nil {
		base.HostExecRequiresAsk = *o.HostExecRequiresAsk
	}
	return base
}

func cleanStrings(in []string) []string {
	out := make([]string, 0, len(in))
	for _, s := range in {
		s = strings.TrimSpace(s)
		if s == "" {
			continue
		}
		out = append(out, s)
	}
	return out
}

// shellPolicy maps DeniedCommands / AllowedCommands into shellsafe.Policy.
// Unexported on purpose: callers should not depend on internal/shellsafe types.
func (p Policy) shellPolicy() shellsafe.Policy {
	return shellsafe.PolicyFromLists(p.AllowedCommands, p.DeniedCommands)
}
