//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package plugin

import (
	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/log"
)

// WithPlugins registers plugins for one runner.Run call.
func WithPlugins(plugins ...Plugin) agent.RunOption {
	copied := append([]Plugin(nil), plugins...)
	return func(opts *agent.RunOptions) {
		manager, err := NewManager(copied...)
		if err != nil {
			log.Errorf("configure run plugins failed: %v", err)
			return
		}
		opts.Plugins = append(opts.Plugins, manager)
	}
}
