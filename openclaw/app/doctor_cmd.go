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
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	tgch "trpc.group/trpc-go/trpc-agent-go/openclaw/internal/channel/telegram"
	"trpc.group/trpc-go/trpc-agent-go/openclaw/internal/pairing"
	tgapi "trpc.group/trpc-go/trpc-agent-go/openclaw/internal/telegram"
)

const subcmdDoctor = "doctor"

const (
	telegramLongPollTimeout = 25 * time.Second
	telegramTimeoutSlack    = 5 * time.Second
)

func runDoctor(args []string) int {
	fs := flag.NewFlagSet(subcmdDoctor, flag.ContinueOnError)
	fs.SetOutput(os.Stderr)

	token := fs.String("telegram-token", "", "Telegram bot token")
	stateDir := fs.String(
		"state-dir",
		"",
		"State dir (default: $HOME/.trpc-agent-go/openclaw)",
	)
	proxy := fs.String(
		"telegram-proxy",
		"",
		"HTTP proxy URL for Telegram API calls (optional)",
	)
	httpTimeout := fs.Duration(
		"telegram-http-timeout",
		0,
		"HTTP client timeout for Telegram API calls (optional)",
	)
	maxRetries := fs.Int(
		"telegram-max-retries",
		defaultTelegramMaxRetries,
		"Max retries for Telegram API calls",
	)

	dmPolicy := fs.String(
		"telegram-dm-policy",
		"",
		"Telegram DM policy: disabled|open|allowlist|pairing",
	)
	groupPolicy := fs.String(
		"telegram-group-policy",
		"",
		"Telegram group policy: disabled|open|allowlist",
	)
	allowUsers := fs.String(
		"allow-users",
		"",
		"Comma-separated allowlist; empty allows all",
	)
	allowThreads := fs.String(
		"telegram-allow-threads",
		"",
		"Comma-separated allowlist of chat/topic threads",
	)

	if err := fs.Parse(args); err != nil {
		return 2
	}
	if strings.TrimSpace(*token) == "" {
		fmt.Println("Telegram: disabled")
		return 0
	}

	ctx := context.Background()
	opts, err := makeTelegramAPIOptions(*proxy, *httpTimeout, *maxRetries)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}

	c, err := tgapi.New(*token, opts...)
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
	if !checkTimeout(*httpTimeout) {
		ok = false
	}
	if !checkWebhook(ctx, c) {
		ok = false
	}
	if !checkPolicies(
		*dmPolicy,
		*groupPolicy,
		*allowUsers,
		*allowThreads,
	) {
		ok = false
	}
	if !checkPairingStore(
		ctx,
		*stateDir,
		*dmPolicy,
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
		"WARN: telegram-http-timeout=%s may be too low for long polling\n",
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
	rawAllowUsers string,
	rawAllowThreads string,
) bool {
	ok := true

	if isPolicy(dmPolicy, "allowlist") && len(splitCSV(rawAllowUsers)) == 0 {
		fmt.Fprintln(os.Stderr,
			"WARN: telegram-dm-policy=allowlist but allow-users is empty",
		)
		ok = false
	}
	if isPolicy(groupPolicy, "allowlist") &&
		len(splitCSV(rawAllowThreads)) == 0 {
		fmt.Fprintln(os.Stderr,
			"WARN: telegram-group-policy=allowlist but "+
				"telegram-allow-threads is empty",
		)
		ok = false
	}
	return ok
}

func checkPairingStore(
	ctx context.Context,
	rawStateDir string,
	dmPolicy string,
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

	bot := tgch.BotInfo{
		ID:       me.ID,
		Username: strings.TrimSpace(me.Username),
	}
	path, err := tgch.PairingStorePath(stateDir, bot)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return false
	}

	store, err := pairing.NewFileStore(path)
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
