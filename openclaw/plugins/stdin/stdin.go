//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package stdin registers a simple "stdin channel" plugin.
//
// It is intended as a reference implementation for writing custom
// channels. The channel reads one line per message from STDIN, sends it
// to the OpenClaw gateway, and prints the reply to STDOUT.
package stdin

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	occhannel "trpc.group/trpc-go/trpc-agent-go/openclaw/channel"
	"trpc.group/trpc-go/trpc-agent-go/openclaw/gwclient"
	"trpc.group/trpc-go/trpc-agent-go/openclaw/registry"
)

const (
	pluginType = "stdin"

	defaultFrom           = "local"
	defaultUserLabel      = "User"
	defaultAssistantLabel = "Assistant"

	exitCmd1 = "/exit"
	exitCmd2 = "/quit"

	defaultScannerBufBytes = 64 * 1024
	defaultScannerMaxBytes = 1 << 20
)

func init() {
	if err := registry.RegisterChannel(pluginType, newChannel); err != nil {
		panic(err)
	}
}

type channelCfg struct {
	From           string `yaml:"from"`
	Thread         string `yaml:"thread"`
	MaxLineBytes   int    `yaml:"max_line_bytes"`
	ShowPrompt     bool   `yaml:"show_prompt,omitempty"`
	ShowRoleLabels bool   `yaml:"show_role_labels,omitempty"`
	UserLabel      string `yaml:"user_label,omitempty"`
	AssistantLabel string `yaml:"assistant_label,omitempty"`
}

func newChannel(
	deps registry.ChannelDeps,
	spec registry.PluginSpec,
) (occhannel.Channel, error) {
	if deps.Gateway == nil {
		return nil, errors.New("stdin channel: nil gateway client")
	}

	var cfg channelCfg
	if err := registry.DecodeStrict(spec.Config, &cfg); err != nil {
		return nil, err
	}

	from := strings.TrimSpace(cfg.From)
	if from == "" {
		from = defaultFrom
	}

	maxLineBytes := cfg.MaxLineBytes
	if maxLineBytes <= 0 {
		maxLineBytes = defaultScannerMaxBytes
	}
	bufBytes := defaultScannerBufBytes
	if maxLineBytes < bufBytes {
		bufBytes = maxLineBytes
	}

	id := pluginType
	if strings.TrimSpace(spec.Name) != "" {
		id = strings.TrimSpace(spec.Name)
	}

	return &channel{
		id:             id,
		gw:             deps.Gateway,
		from:           from,
		thread:         strings.TrimSpace(cfg.Thread),
		bufBytes:       bufBytes,
		maxLineBytes:   maxLineBytes,
		showPrompt:     cfg.ShowPrompt,
		showRoleLabels: cfg.ShowRoleLabels,
		userLabel:      defaultLabel(cfg.UserLabel, defaultUserLabel),
		assistantLabel: defaultLabel(
			cfg.AssistantLabel,
			defaultAssistantLabel,
		),
	}, nil
}

type channel struct {
	id     string
	gw     registry.GatewayClient
	from   string
	thread string

	showPrompt     bool
	showRoleLabels bool
	userLabel      string
	assistantLabel string

	bufBytes     int
	maxLineBytes int
}

func (c *channel) ID() string { return c.id }

func (c *channel) Run(ctx context.Context) error {
	fmt.Fprintln(os.Stdout, "STDIN channel started.")
	fmt.Fprintln(os.Stdout, "Type /quit or /exit to stop.")

	in := bufio.NewScanner(os.Stdin)
	in.Buffer(make([]byte, c.bufBytes), c.maxLineBytes)
	for {
		if ctx.Err() != nil {
			return nil
		}

		c.printPrompt()
		if !in.Scan() {
			c.printPromptTerminator()
			if err := in.Err(); err != nil {
				return err
			}
			return nil
		}

		text := strings.TrimSpace(in.Text())
		if text == "" {
			continue
		}
		if text == exitCmd1 || text == exitCmd2 {
			return nil
		}

		rsp, err := c.gw.SendMessage(ctx, gwclient.MessageRequest{
			Channel: pluginType,
			From:    c.from,
			Thread:  c.thread,
			Text:    text,
			UserID:  c.from,
		})
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			continue
		}
		if rsp.Ignored {
			c.printReply("(ignored)")
			continue
		}
		c.printReply(rsp.Reply)
	}
}

func defaultLabel(raw string, fallback string) string {
	label := strings.TrimSpace(raw)
	if label == "" {
		return fallback
	}
	return label
}

func (c *channel) printPrompt() {
	if !c.showPrompt {
		return
	}
	fmt.Fprintf(os.Stdout, "%s: ", c.userLabel)
}

func (c *channel) printPromptTerminator() {
	if !c.showPrompt {
		return
	}
	fmt.Fprintln(os.Stdout)
}

func (c *channel) printReply(reply string) {
	if c.showRoleLabels {
		fmt.Fprintf(
			os.Stdout,
			"%s: %s\n",
			c.assistantLabel,
			reply,
		)
		return
	}
	fmt.Fprintln(os.Stdout, reply)
}
