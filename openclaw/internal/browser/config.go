//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package browser

import (
	"errors"
	"fmt"
	"strings"
	"time"

	mcptool "trpc.group/trpc-go/trpc-agent-go/tool/mcp"
)

const (
	ToolName = "browser"

	defaultProfileName = "openclaw"

	transportStdio      = "stdio"
	transportSSE        = "sse"
	transportStreamable = "streamable"
	transportStreamHTTP = "streamable_http"
)

type reconnectConfig struct {
	Enabled     bool `yaml:"enabled,omitempty"`
	MaxAttempts int  `yaml:"max_attempts,omitempty"`
}

// NodeConfig describes one remote browser server node.
type NodeConfig struct {
	ID        string `yaml:"id,omitempty"`
	Name      string `yaml:"name,omitempty"`
	ServerURL string `yaml:"server_url,omitempty"`
	AuthToken string `yaml:"auth_token,omitempty"`
}

// ProfileConfig describes one browser execution profile.
type ProfileConfig struct {
	Name             string            `yaml:"name,omitempty"`
	Description      string            `yaml:"description,omitempty"`
	AuthToken        string            `yaml:"auth_token,omitempty"`
	BrowserServerURL string            `yaml:"browser_server_url,omitempty"`
	Transport        string            `yaml:"transport,omitempty"`
	ServerURL        string            `yaml:"server_url,omitempty"`
	Headers          map[string]string `yaml:"headers,omitempty"`
	Command          string            `yaml:"command,omitempty"`
	Args             []string          `yaml:"args,omitempty"`
	Timeout          time.Duration     `yaml:"timeout,omitempty"`
	Reconnect        *reconnectConfig  `yaml:"reconnect,omitempty"`
}

// Config describes the native browser tool configuration.
type Config struct {
	DefaultProfile   string          `yaml:"default_profile,omitempty"`
	EvaluateEnabled  *bool           `yaml:"evaluate_enabled,omitempty"`
	ServerURL        string          `yaml:"server_url,omitempty"`
	AuthToken        string          `yaml:"auth_token,omitempty"`
	SandboxServerURL string          `yaml:"sandbox_server_url,omitempty"`
	SandboxAuthToken string          `yaml:"sandbox_auth_token,omitempty"`
	AllowedDomains   []string        `yaml:"allowed_domains,omitempty"`
	BlockedDomains   []string        `yaml:"blocked_domains,omitempty"`
	AllowLoopback    *bool           `yaml:"allow_loopback,omitempty"`
	AllowPrivateNet  *bool           `yaml:"allow_private_networks,omitempty"`
	AllowFileURLs    *bool           `yaml:"allow_file_urls,omitempty"`
	Nodes            []NodeConfig    `yaml:"nodes,omitempty"`
	Profiles         []ProfileConfig `yaml:"profiles,omitempty"`
}

type resolvedConfig struct {
	DefaultProfile  string
	EvaluateEnabled bool
	Navigation      navigationPolicy
	HostServer      *serverTargetConfig
	SandboxServer   *serverTargetConfig
	NodeTargets     map[string]serverTargetConfig
	Profiles        []resolvedProfile
}

type resolvedProfile struct {
	Name             string
	Description      string
	BrowserServerURL string
	AuthToken        string
	Connection       mcptool.ConnectionConfig
	Reconnect        *reconnectConfig
}

type serverTargetConfig struct {
	ID        string
	ServerURL string
	AuthToken string
}

func resolveConfig(cfg Config) (resolvedConfig, error) {
	if len(cfg.Profiles) == 0 {
		return resolvedConfig{}, errors.New(
			"browser provider requires at least one profile",
		)
	}

	out := resolvedConfig{
		DefaultProfile: strings.TrimSpace(cfg.DefaultProfile),
	}
	if cfg.EvaluateEnabled != nil {
		out.EvaluateEnabled = *cfg.EvaluateEnabled
	}
	out.Navigation = resolveNavigationPolicy(cfg)
	out.HostServer = resolveServerTarget(
		targetHost,
		cfg.ServerURL,
		cfg.AuthToken,
	)
	out.SandboxServer = resolveServerTarget(
		targetSandbox,
		cfg.SandboxServerURL,
		cfg.SandboxAuthToken,
	)
	out.NodeTargets = resolveNodeTargets(cfg.Nodes)
	allowEmptyProfileConnection := out.HostServer != nil ||
		out.SandboxServer != nil ||
		len(out.NodeTargets) > 0

	seen := make(map[string]struct{}, len(cfg.Profiles))
	out.Profiles = make([]resolvedProfile, 0, len(cfg.Profiles))
	for i := range cfg.Profiles {
		resolved, err := resolveProfile(
			cfg.Profiles[i],
			i,
			allowEmptyProfileConnection,
		)
		if err != nil {
			return resolvedConfig{}, err
		}
		if _, ok := seen[resolved.Name]; ok {
			return resolvedConfig{}, fmt.Errorf(
				"browser profile %q is duplicated",
				resolved.Name,
			)
		}
		seen[resolved.Name] = struct{}{}
		out.Profiles = append(out.Profiles, resolved)
	}

	if out.DefaultProfile == "" {
		out.DefaultProfile = out.Profiles[0].Name
	}
	if _, ok := seen[out.DefaultProfile]; !ok {
		return resolvedConfig{}, fmt.Errorf(
			"browser default_profile %q is not defined",
			out.DefaultProfile,
		)
	}
	return out, nil
}

func resolveNavigationPolicy(cfg Config) navigationPolicy {
	policy := navigationPolicy{
		AllowedDomains: normalizeDomains(cfg.AllowedDomains),
		BlockedDomains: normalizeDomains(cfg.BlockedDomains),
	}
	if cfg.AllowLoopback != nil {
		policy.AllowLoopback = *cfg.AllowLoopback
	}
	if cfg.AllowPrivateNet != nil {
		policy.AllowPrivateNet = *cfg.AllowPrivateNet
	}
	if cfg.AllowFileURLs != nil {
		policy.AllowFileURLs = *cfg.AllowFileURLs
	}
	return policy
}

func resolveProfile(
	cfg ProfileConfig,
	index int,
	allowEmptyConnection bool,
) (resolvedProfile, error) {
	name := strings.TrimSpace(cfg.Name)
	if name == "" {
		if index == 0 {
			name = defaultProfileName
		} else {
			return resolvedProfile{}, fmt.Errorf(
				"browser profile at index %d is missing name",
				index,
			)
		}
	}

	conn := mcptool.ConnectionConfig{
		Transport: strings.TrimSpace(cfg.Transport),
		ServerURL: strings.TrimSpace(cfg.ServerURL),
		Headers:   cfg.Headers,
		Command:   strings.TrimSpace(cfg.Command),
		Args:      cfg.Args,
		Timeout:   cfg.Timeout,
	}
	browserServerURL := strings.TrimSpace(cfg.BrowserServerURL)
	if browserServerURL != "" {
		if conn.Transport != "" {
			return resolvedProfile{}, fmt.Errorf(
				"browser profile %q cannot mix browser_server_url "+
					"with transport",
				name,
			)
		}
		return resolvedProfile{
			Name:             name,
			Description:      strings.TrimSpace(cfg.Description),
			BrowserServerURL: browserServerURL,
			AuthToken:        strings.TrimSpace(cfg.AuthToken),
		}, nil
	}
	if conn.Transport == "" && allowEmptyConnection {
		return resolvedProfile{
			Name:        name,
			Description: strings.TrimSpace(cfg.Description),
			AuthToken:   strings.TrimSpace(cfg.AuthToken),
		}, nil
	}
	if err := validateConnection(conn); err != nil {
		return resolvedProfile{}, fmt.Errorf(
			"browser profile %q: %w",
			name,
			err,
		)
	}

	return resolvedProfile{
		Name:        name,
		Description: strings.TrimSpace(cfg.Description),
		AuthToken:   strings.TrimSpace(cfg.AuthToken),
		Connection:  conn,
		Reconnect:   cfg.Reconnect,
	}, nil
}

func resolveServerTarget(
	id string,
	serverURL string,
	authToken string,
) *serverTargetConfig {
	serverURL = strings.TrimSpace(serverURL)
	if serverURL == "" {
		return nil
	}
	return &serverTargetConfig{
		ID:        id,
		ServerURL: serverURL,
		AuthToken: strings.TrimSpace(authToken),
	}
}

func resolveNodeTargets(
	nodes []NodeConfig,
) map[string]serverTargetConfig {
	if len(nodes) == 0 {
		return nil
	}

	out := make(map[string]serverTargetConfig, len(nodes))
	for i := range nodes {
		node := nodes[i]
		serverURL := strings.TrimSpace(node.ServerURL)
		if serverURL == "" {
			continue
		}
		id := strings.TrimSpace(node.ID)
		if id == "" {
			id = strings.TrimSpace(node.Name)
		}
		if id == "" {
			continue
		}
		out[id] = serverTargetConfig{
			ID:        id,
			ServerURL: serverURL,
			AuthToken: strings.TrimSpace(node.AuthToken),
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func validateConnection(cfg mcptool.ConnectionConfig) error {
	transport := strings.ToLower(strings.TrimSpace(cfg.Transport))
	switch transport {
	case transportStdio:
		if strings.TrimSpace(cfg.Command) == "" {
			return errors.New("stdio transport requires command")
		}
		return nil
	case transportSSE, transportStreamable, transportStreamHTTP:
		if strings.TrimSpace(cfg.ServerURL) == "" {
			return errors.New("network transport requires server_url")
		}
		return nil
	default:
		return fmt.Errorf(
			"unsupported transport %q",
			cfg.Transport,
		)
	}
}
