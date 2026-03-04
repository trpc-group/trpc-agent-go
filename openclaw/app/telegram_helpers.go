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
	"strings"

	tgch "trpc.group/trpc-go/trpc-agent-go/openclaw/internal/channel/telegram"
	"trpc.group/trpc-go/trpc-agent-go/openclaw/internal/pairing"
	tgapi "trpc.group/trpc-go/trpc-agent-go/openclaw/internal/telegram"
)

const (
	telegramChannelType = "telegram"

	defaultTelegramMaxRetries = 3
)

type telegramChannelConfig struct {
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

func resolveTelegramChannelSpecs(specs []pluginSpec) []pluginSpec {
	out := make([]pluginSpec, 0, len(specs))
	for _, spec := range specs {
		typeName := strings.ToLower(strings.TrimSpace(spec.Type))
		if typeName == telegramChannelType {
			out = append(out, spec)
		}
	}
	return out
}

func pairingStorePath(stateDir string, me tgapi.User) (string, error) {
	bot := tgch.BotInfo{
		ID:       me.ID,
		Username: strings.TrimSpace(me.Username),
	}
	return tgch.PairingStorePath(stateDir, bot)
}

func telegramClientNetOptions(
	cfg telegramChannelConfig,
) (tgapi.ClientNetOptions, error) {
	httpTimeout, err := parseDuration(cfg.HTTPTimeout)
	if err != nil {
		return tgapi.ClientNetOptions{}, err
	}

	maxRetries := defaultTelegramMaxRetries
	if cfg.MaxRetries != nil {
		maxRetries = *cfg.MaxRetries
	}
	return tgapi.ClientNetOptions{
		ProxyURL:   cfg.Proxy,
		Timeout:    httpTimeout,
		MaxRetries: maxRetries,
	}, nil
}

func pairingStoreOptions(rawPairingTTL string) ([]pairing.Option, error) {
	ttlRaw := strings.TrimSpace(rawPairingTTL)
	if ttlRaw == "" {
		return nil, nil
	}

	ttl, err := parseDuration(ttlRaw)
	if err != nil {
		return nil, err
	}
	if ttl <= 0 {
		return nil, errors.New("pairing: non-positive ttl")
	}
	return []pairing.Option{pairing.WithTTL(ttl)}, nil
}
