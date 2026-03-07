//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package telegram registers the Telegram channel plugin.
package telegram

import (
	"context"
	"errors"
	"strings"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/log"

	occhannel "trpc.group/trpc-go/trpc-agent-go/openclaw/channel"
	tgch "trpc.group/trpc-go/trpc-agent-go/openclaw/internal/channel/telegram"
	tgapi "trpc.group/trpc-go/trpc-agent-go/openclaw/internal/telegram"
	"trpc.group/trpc-go/trpc-agent-go/openclaw/registry"
)

const (
	pluginType = "telegram"

	defaultStreamingMode = "progress"
	defaultMaxRetries    = 3

	errMissingToken = "telegram channel: missing config.token"
)

func init() {
	if err := registry.RegisterChannel(pluginType, newChannel); err != nil {
		panic(err)
	}
}

type channelCfg struct {
	Token string `yaml:"token"`

	StartFromLatest *bool `yaml:"start_from_latest"`

	Proxy       string `yaml:"proxy"`
	HTTPTimeout string `yaml:"http_timeout"`
	MaxRetries  *int   `yaml:"max_retries"`

	Streaming    string   `yaml:"streaming"`
	DMPolicy     string   `yaml:"dm_policy"`
	GroupPolicy  string   `yaml:"group_policy"`
	AllowThreads []string `yaml:"allow_threads"`
	PairingTTL   string   `yaml:"pairing_ttl"`
}

func newChannel(
	deps registry.ChannelDeps,
	spec registry.PluginSpec,
) (occhannel.Channel, error) {
	if deps.Gateway == nil {
		return nil, errors.New("telegram channel: nil gateway client")
	}

	var cfg channelCfg
	if err := registry.DecodeStrict(spec.Config, &cfg); err != nil {
		return nil, err
	}

	token := strings.TrimSpace(cfg.Token)
	if token == "" {
		return nil, errors.New(errMissingToken)
	}

	ctx := deps.Ctx
	if ctx == nil {
		ctx = context.Background()
	}

	apiOpts, err := makeTelegramAPIOptions(cfg)
	if err != nil {
		return nil, err
	}

	bot, err := tgch.ProbeBotInfo(ctx, token, apiOpts...)
	if err != nil {
		return nil, err
	}
	logTelegramBot(bot)

	chOpts := []tgch.Option{
		tgch.WithAPIOptions(apiOpts...),
		tgch.WithStateDir(deps.StateDir),
		tgch.WithStreamingMode(resolveStreamingMode(cfg.Streaming)),
	}
	if cfg.StartFromLatest != nil {
		chOpts = append(
			chOpts,
			tgch.WithStartFromLatest(*cfg.StartFromLatest),
		)
	}
	if strings.TrimSpace(cfg.DMPolicy) != "" {
		chOpts = append(chOpts, tgch.WithDMPolicy(cfg.DMPolicy))
	}
	if strings.TrimSpace(cfg.GroupPolicy) != "" {
		chOpts = append(
			chOpts,
			tgch.WithGroupPolicy(cfg.GroupPolicy),
		)
	}
	if len(deps.AllowUsers) > 0 {
		chOpts = append(chOpts, tgch.WithAllowUsers(deps.AllowUsers...))
	}
	if len(cfg.AllowThreads) > 0 {
		chOpts = append(
			chOpts,
			tgch.WithAllowThreads(cfg.AllowThreads...),
		)
	}
	if strings.TrimSpace(cfg.PairingTTL) != "" {
		ttl, err := time.ParseDuration(strings.TrimSpace(cfg.PairingTTL))
		if err != nil {
			return nil, err
		}
		chOpts = append(chOpts, tgch.WithPairingTTL(ttl))
	}

	return tgch.New(token, bot, deps.Gateway, chOpts...)
}

func resolveStreamingMode(raw string) string {
	mode := strings.TrimSpace(raw)
	if mode == "" {
		return defaultStreamingMode
	}
	return mode
}

func logTelegramBot(bot tgch.BotInfo) {
	if strings.TrimSpace(bot.Username) != "" {
		log.Infof("Telegram enabled as @%s", bot.Username)
		return
	}
	if bot.ID != 0 {
		log.Infof("Telegram enabled as id %d", bot.ID)
		return
	}
	log.Infof("Telegram enabled")
}

func makeTelegramAPIOptions(cfg channelCfg) ([]tgapi.Option, error) {
	httpTimeout := time.Duration(0)
	if strings.TrimSpace(cfg.HTTPTimeout) != "" {
		dur, err := time.ParseDuration(strings.TrimSpace(cfg.HTTPTimeout))
		if err != nil {
			return nil, err
		}
		httpTimeout = dur
	}

	maxRetries := defaultMaxRetries
	if cfg.MaxRetries != nil {
		maxRetries = *cfg.MaxRetries
	}

	return tgapi.BuildClientOptionsFromEnv(tgapi.ClientNetOptions{
		ProxyURL:   cfg.Proxy,
		Timeout:    httpTimeout,
		MaxRetries: maxRetries,
	})
}
