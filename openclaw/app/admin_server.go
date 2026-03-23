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
	"trpc.group/trpc-go/trpc-agent-go/openclaw/internal/cron"
	"trpc.group/trpc-go/trpc-agent-go/openclaw/internal/octool"
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
		MemoryBackend:  strings.TrimSpace(opts.MemoryBackend),
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
		Cron:           cronSvc,
		Exec:           execMgr,
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
