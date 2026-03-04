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
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/openclaw/internal/pairing"
	tgapi "trpc.group/trpc-go/trpc-agent-go/openclaw/internal/telegram"
	"trpc.group/trpc-go/trpc-agent-go/openclaw/registry"
)

const subcmdDoctor = "doctor"

const (
	telegramLongPollTimeout = 25 * time.Second
	telegramTimeoutSlack    = 5 * time.Second
)

func runDoctor(args []string) int {
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

	specs := resolveTelegramChannelSpecs(opts.Channels)
	if len(specs) == 0 {
		fmt.Println("Telegram: not configured")
		return 0
	}
	if len(specs) > 1 {
		fmt.Fprintln(
			os.Stderr,
			"doctor: multiple telegram channels configured",
		)
		return 1
	}
	spec := specs[0]

	var cfg telegramChannelConfig
	if err := registry.DecodeStrict(spec.Config, &cfg); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	token := strings.TrimSpace(cfg.Token)
	if token == "" {
		fmt.Fprintln(
			os.Stderr,
			"doctor: missing channels[].config.token for telegram channel",
		)
		return 1
	}

	ctx := context.Background()

	netOpts, err := telegramClientNetOptions(cfg)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	apiOpts, err := tgapi.BuildClientOptionsFromEnv(netOpts)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}

	c, err := tgapi.New(token, apiOpts...)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}

	me, err := c.GetMe(ctx)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	printBot(me)

	ok := true
	if !checkTimeout(netOpts.Timeout) {
		ok = false
	}
	if !checkWebhook(ctx, c) {
		ok = false
	}
	if !checkPolicies(
		cfg.DMPolicy,
		cfg.GroupPolicy,
		splitCSV(opts.AllowUsers),
		cfg.AllowThreads,
	) {
		ok = false
	}
	if !checkPairingStore(
		ctx,
		opts.StateDir,
		cfg.DMPolicy,
		cfg.PairingTTL,
		me,
	) {
		ok = false
	}

	if ok {
		fmt.Println("Doctor: ok")
		return 0
	}
	return 1
}

func printBot(me tgapi.User) {
	username := strings.TrimSpace(me.Username)
	if username != "" {
		fmt.Printf("Bot: @%s (id %d)\n", username, me.ID)
		return
	}
	fmt.Printf("Bot: id %d\n", me.ID)
}

func checkTimeout(timeout time.Duration) bool {
	if timeout <= 0 {
		return true
	}
	min := telegramLongPollTimeout + telegramTimeoutSlack
	if timeout >= min {
		return true
	}
	fmt.Fprintf(
		os.Stderr,
		"WARN: channels[].config.http_timeout=%s may be too low for long polling\n",
		timeout,
	)
	return false
}

func checkWebhook(ctx context.Context, c *tgapi.Client) bool {
	info, err := c.GetWebhookInfo(ctx)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return false
	}
	if strings.TrimSpace(info.URL) == "" {
		fmt.Println("Webhook: not set (long polling ok)")
		return true
	}

	fmt.Fprintf(os.Stderr, "WARN: webhook is set: %s\n", info.URL)
	if info.PendingUpdateCount > 0 {
		fmt.Fprintf(
			os.Stderr,
			"WARN: pending updates: %d\n",
			info.PendingUpdateCount,
		)
	}
	if strings.TrimSpace(info.LastErrorMessage) != "" {
		fmt.Fprintf(
			os.Stderr,
			"WARN: last webhook error: %s\n",
			info.LastErrorMessage,
		)
	}
	fmt.Fprintln(os.Stderr,
		"Long polling (getUpdates) will not work until you delete the webhook.",
	)
	return false
}

func checkPolicies(
	dmPolicy string,
	groupPolicy string,
	allowUsers []string,
	allowThreads []string,
) bool {
	ok := true

	if isPolicy(dmPolicy, "allowlist") && len(allowUsers) == 0 {
		fmt.Fprintln(
			os.Stderr,
			"WARN: telegram dm_policy=allowlist but allow-users is empty",
		)
		ok = false
	}
	if isPolicy(groupPolicy, "allowlist") &&
		len(allowThreads) == 0 {
		fmt.Fprintln(
			os.Stderr,
			"WARN: telegram group_policy=allowlist but allow_threads is empty",
		)
		ok = false
	}
	return ok
}

func checkPairingStore(
	ctx context.Context,
	rawStateDir string,
	dmPolicy string,
	rawPairingTTL string,
	me tgapi.User,
) bool {
	if dmPolicy != "" && !isPolicy(dmPolicy, "pairing") {
		return true
	}

	stateDir, err := resolveStateDir(rawStateDir)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return false
	}

	path, err := pairingStorePath(stateDir, me)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return false
	}

	storeOpts, err := pairingStoreOptions(rawPairingTTL)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return false
	}

	store, err := pairing.NewFileStore(path, storeOpts...)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return false
	}

	pending, err := store.ListPending(ctx)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return false
	}

	fmt.Printf("Pairing store: %s\n", path)
	fmt.Printf("Pairing pending: %d\n", len(pending))
	return true
}

func isPolicy(raw string, want string) bool {
	return strings.EqualFold(strings.TrimSpace(raw), want)
}
