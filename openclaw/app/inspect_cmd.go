//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package app

import (
	"errors"
	"fmt"
	"os"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/openclaw/registry"
)

const subcmdInspect = "inspect"

const (
	inspectCmdPlugins    = "plugins"
	inspectCmdConfigKeys = "config-keys"
)

const (
	registryKindChannel        = "channel"
	registryKindSessionBackend = "session backend"
	registryKindMemoryBackend  = "memory backend"
	registryKindToolProvider   = "tool provider"
	registryKindToolSet        = "toolset provider"
	registryKindModel          = "model"
)

func runInspect(args []string) int {
	if len(args) == 0 {
		return runInspectPlugins(nil)
	}

	cmd := strings.TrimSpace(args[0])
	switch strings.ToLower(cmd) {
	case inspectCmdPlugins:
		return runInspectPlugins(args[1:])
	case inspectCmdConfigKeys:
		return runInspectConfigKeys(args[1:])
	case "", "help", "-h", "--help":
		printInspectUsage()
		return 2
	default:
		fmt.Fprintf(os.Stderr, "unknown inspect command: %s\n", cmd)
		printInspectUsage()
		return 2
	}
}

func runInspectPlugins(args []string) int {
	if len(args) > 0 {
		fmt.Fprintln(os.Stderr, unexpectedArgsError(args))
		printInspectUsage()
		return 2
	}

	printInspectList("Built-in channels", []string{
		registry.ChannelTypeTelegram,
	})
	printInspectList("Channel plugins", registry.Types(registryKindChannel))
	printInspectList("Model types", registry.Types(registryKindModel))
	printInspectList(
		"Session backends",
		registry.Types(registryKindSessionBackend),
	)
	printInspectList(
		"Memory backends",
		registry.Types(registryKindMemoryBackend),
	)
	printInspectList(
		"Tool providers",
		registry.Types(registryKindToolProvider),
	)
	printInspectList("ToolSets", registry.Types(registryKindToolSet))
	return 0
}

func runInspectConfigKeys(args []string) int {
	opts, err := parseRunOptions(args)
	if err != nil {
		var exitErr *exitError
		if errors.As(err, &exitErr) {
			if exitErr.Code != 2 {
				fmt.Fprintln(os.Stderr, exitErr.Err)
			}
			return exitErr.Code
		}
		fmt.Fprintln(os.Stderr, err)
		return 1
	}

	for _, key := range resolveSkillConfigKeys(opts) {
		fmt.Fprintln(os.Stdout, key)
	}
	return 0
}

func printInspectList(title string, items []string) {
	fmt.Fprintf(os.Stdout, "%s:\n", title)
	if len(items) == 0 {
		fmt.Fprintln(os.Stdout, "- (none)")
		return
	}
	for _, item := range items {
		fmt.Fprintf(os.Stdout, "- %s\n", item)
	}
}

func printInspectUsage() {
	fmt.Fprintln(os.Stderr, "Usage:")
	fmt.Fprintln(os.Stderr, "  openclaw inspect plugins")
	fmt.Fprintln(os.Stderr,
		"  openclaw inspect config-keys [openclaw flags]",
	)
}
