//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package app

import (
	"errors"
	"fmt"
	"net"
	"os"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/openclaw/channel"
	"trpc.group/trpc-go/trpc-agent-go/openclaw/internal/admin"
	ocbrowser "trpc.group/trpc-go/trpc-agent-go/openclaw/internal/browser"
	"trpc.group/trpc-go/trpc-agent-go/openclaw/internal/cron"
	"trpc.group/trpc-go/trpc-agent-go/openclaw/internal/octool"
	"trpc.group/trpc-go/trpc-agent-go/openclaw/registry"
)

const adminAutoPortSearchSpan = 32

type adminBinding struct {
	listener  net.Listener
	addr      string
	url       string
	relocated bool
}

func openAdminBinding(
	addr string,
	autoPort bool,
) (*adminBinding, error) {
	preferred := strings.TrimSpace(addr)
	if preferred == "" {
		return nil, fmt.Errorf("admin: empty listen address")
	}

	listener, err := net.Listen("tcp", preferred)
	if err == nil {
		actual := listener.Addr().String()
		return &adminBinding{
			listener: listener,
			addr:     actual,
			url:      listenURL(actual),
		}, nil
	}
	if !autoPort || !isAddressInUse(err) {
		return nil, fmt.Errorf("admin: listen on %s: %w", preferred, err)
	}

	host, portRaw, splitErr := net.SplitHostPort(preferred)
	if splitErr != nil {
		return nil, fmt.Errorf("admin: listen on %s: %w", preferred, err)
	}
	basePort, convErr := strconv.Atoi(portRaw)
	if convErr != nil || basePort <= 0 || basePort >= 65535 {
		return nil, fmt.Errorf("admin: listen on %s: %w", preferred, err)
	}

	maxPort := basePort + adminAutoPortSearchSpan
	if maxPort > 65535 {
		maxPort = 65535
	}
	for port := basePort + 1; port <= maxPort; port++ {
		candidate := net.JoinHostPort(host, strconv.Itoa(port))
		listener, err = net.Listen("tcp", candidate)
		if err == nil {
			actual := listener.Addr().String()
			return &adminBinding{
				listener:  listener,
				addr:      actual,
				url:       listenURL(actual),
				relocated: actual != preferred,
			}, nil
		}
		if !isAddressInUse(err) {
			return nil, fmt.Errorf(
				"admin: listen on %s: %w",
				candidate,
				err,
			)
		}
	}
	return nil, fmt.Errorf(
		"admin: listen on %s failed and no free port was found "+
			"in the next %d ports: %w",
		preferred,
		adminAutoPortSearchSpan,
		err,
	)
}

func isAddressInUse(err error) bool {
	return errors.Is(err, syscall.EADDRINUSE)
}

func buildAdminConfig(
	opts runOptions,
	agentType string,
	instanceID string,
	langfuse admin.LangfuseStatus,
	stateDir string,
	debugDir string,
	startedAt time.Time,
	channels []channel.Channel,
	routes admin.Routes,
	cronSvc *cron.Service,
	execMgr *octool.Manager,
	browserManaged admin.BrowserManagedStatusProvider,
	adminAddr string,
	adminURL string,
) admin.Config {
	return admin.Config{
		AppName:        opts.AppName,
		InstanceID:     instanceID,
		StartedAt:      startedAt,
		Hostname:       runtimeHostname(),
		PID:            os.Getpid(),
		GoVersion:      runtime.Version(),
		AgentType:      strings.TrimSpace(agentType),
		ModelMode:      strings.TrimSpace(opts.ModelMode),
		ModelName:      adminModelName(opts, agentType),
		SessionBackend: strings.TrimSpace(opts.SessionBackend),
		MemoryBackend:  resolveMemoryBackendType(opts.MemoryBackend),
		GatewayAddr:    opts.HTTPAddr,
		GatewayURL:     listenURL(opts.HTTPAddr),
		AdminAddr:      strings.TrimSpace(adminAddr),
		AdminURL:       strings.TrimSpace(adminURL),
		AdminAutoPort:  opts.AdminAutoPort,
		Langfuse:       langfuse,
		StateDir:       stateDir,
		DebugDir:       debugDir,
		Channels:       channelIDs(channels),
		GatewayRoutes:  routes,
		Browser: buildBrowserAdminConfig(
			opts.ToolProviders,
			browserManaged,
		),
		Cron: cronSvc,
		Exec: execMgr,
	}
}

func buildBrowserAdminConfig(
	specs []pluginSpec,
	managed admin.BrowserManagedStatusProvider,
) admin.BrowserConfig {
	providers := make([]admin.BrowserProvider, 0, len(specs))
	for i := range specs {
		spec := specs[i]
		if strings.TrimSpace(spec.Type) != toolProviderBrowser {
			continue
		}

		var cfg ocbrowser.Config
		if err := registry.DecodeStrict(spec.Config, &cfg); err != nil {
			continue
		}

		provider := admin.BrowserProvider{
			Name:             strings.TrimSpace(spec.Name),
			DefaultProfile:   strings.TrimSpace(cfg.DefaultProfile),
			HostServerURL:    strings.TrimSpace(cfg.ServerURL),
			SandboxServerURL: strings.TrimSpace(cfg.SandboxServerURL),
		}
		if cfg.EvaluateEnabled != nil {
			provider.EvaluateEnabled = *cfg.EvaluateEnabled
		}
		if cfg.AllowLoopback != nil {
			provider.AllowLoopback = *cfg.AllowLoopback
		}
		if cfg.AllowPrivateNet != nil {
			provider.AllowPrivateNet = *cfg.AllowPrivateNet
		}
		if cfg.AllowFileURLs != nil {
			provider.AllowFileURLs = *cfg.AllowFileURLs
		}

		if len(cfg.Profiles) > 0 {
			provider.Profiles = make(
				[]admin.BrowserProfile,
				0,
				len(cfg.Profiles),
			)
		}
		for j := range cfg.Profiles {
			profile := cfg.Profiles[j]
			provider.Profiles = append(
				provider.Profiles,
				admin.BrowserProfile{
					Name: strings.TrimSpace(profile.Name),
					Description: strings.TrimSpace(
						profile.Description,
					),
					Transport: strings.TrimSpace(profile.Transport),
					ServerURL: strings.TrimSpace(profile.ServerURL),
					BrowserServerURL: strings.TrimSpace(
						profile.BrowserServerURL,
					),
				},
			)
		}

		if len(cfg.Nodes) > 0 {
			provider.Nodes = make(
				[]admin.BrowserNode,
				0,
				len(cfg.Nodes),
			)
		}
		for j := range cfg.Nodes {
			node := cfg.Nodes[j]
			provider.Nodes = append(provider.Nodes, admin.BrowserNode{
				ID:        strings.TrimSpace(node.ID),
				ServerURL: strings.TrimSpace(node.ServerURL),
			})
		}
		providers = append(providers, provider)
	}
	return admin.BrowserConfig{
		Providers: providers,
		Managed:   managed,
	}
}

func runtimeHostname() string {
	host, err := os.Hostname()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(host)
}

func adminModelName(
	opts runOptions,
	agentType string,
) string {
	if strings.TrimSpace(agentType) != agentTypeLLM {
		return ""
	}
	return strings.TrimSpace(opts.OpenAIModel)
}
