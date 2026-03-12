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
	"flag"
	"fmt"
	"os"
	"sort"
	"strings"
	"text/tabwriter"
	"time"

	tgch "trpc.group/trpc-go/trpc-agent-go/openclaw/internal/channel/telegram"
	"trpc.group/trpc-go/trpc-agent-go/openclaw/internal/pairing"
	tgapi "trpc.group/trpc-go/trpc-agent-go/openclaw/internal/telegram"
	"trpc.group/trpc-go/trpc-agent-go/openclaw/registry"
)

const (
	subcmdPairing = "pairing"

	flagConfig   = "config"
	flagChannel  = "channel"
	flagStateDir = "state-dir"

	pairingCmdList    = "list"
	pairingCmdApprove = "approve"
)

var probeBotInfo = func(
	ctx context.Context,
	token string,
	opts ...tgapi.Option,
) (tgch.BotInfo, error) {
	return tgch.ProbeBotInfo(ctx, token, opts...)
}

func runPairing(args []string) int {
	fs := flag.NewFlagSet(subcmdPairing, flag.ContinueOnError)
	fs.SetOutput(os.Stderr)

	configPath := fs.String(
		flagConfig,
		"",
		"Path to YAML config file; can also be set via $"+openClawConfigEnvName,
	)
	channelName := fs.String(
		flagChannel,
		"",
		"Telegram channel name (optional)",
	)
	stateDir := fs.String(
		flagStateDir,
		"",
		"State dir (default: $HOME/.trpc-agent-go-github/openclaw)",
	)

	normalizedArgs, err := normalizePairingArgs(args)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		printPairingUsage()
		return 2
	}

	if err := fs.Parse(normalizedArgs); err != nil {
		return 2
	}

	rest := fs.Args()
	if len(rest) == 0 {
		printPairingUsage()
		return 2
	}
	action := rest[0]
	code := ""

	switch action {
	case pairingCmdList:
	case pairingCmdApprove:
		if len(rest) < 2 {
			fmt.Fprintln(os.Stderr, "missing pairing code")
			printPairingUsage()
			return 2
		}
		code = rest[1]
	default:
		fmt.Fprintf(os.Stderr, "unknown pairing command: %s\n", action)
		printPairingUsage()
		return 2
	}

	ctx := context.Background()

	parseArgs := make([]string, 0, 4)
	if strings.TrimSpace(*configPath) != "" {
		parseArgs = append(
			parseArgs,
			"-"+flagConfig,
			strings.TrimSpace(*configPath),
		)
	}
	if strings.TrimSpace(*stateDir) != "" {
		parseArgs = append(
			parseArgs,
			"-"+flagStateDir,
			strings.TrimSpace(*stateDir),
		)
	}

	runOpts, err := parseRunOptions(parseArgs)
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

	store, err := openPairingStore(ctx, runOpts, *channelName)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}

	if action == pairingCmdList {
		return runPairingList(ctx, store)
	}
	return runPairingApprove(ctx, store, code)
}

func printPairingUsage() {
	fmt.Fprintln(os.Stderr, "Usage:")
	fmt.Fprintf(
		os.Stderr,
		"  openclaw pairing list -%s <CONFIG> [-%s <DIR>] [-%s <NAME>]\n",
		flagConfig,
		flagStateDir,
		flagChannel,
	)
	fmt.Fprintf(
		os.Stderr,
		"  openclaw pairing approve <CODE> -%s <CONFIG> [-%s <DIR>] [-%s <NAME>]\n",
		flagConfig,
		flagStateDir,
		flagChannel,
	)
}

func runPairingList(
	ctx context.Context,
	store *pairing.FileStore,
) int {
	pending, err := store.ListPending(ctx)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	sort.Slice(pending, func(i, j int) bool {
		return pending[i].CreatedAt.Before(pending[j].CreatedAt)
	})

	w := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
	fmt.Fprintln(w, "CODE\tUSER_ID\tEXPIRES_AT")
	for _, req := range pending {
		fmt.Fprintf(
			w,
			"%s\t%s\t%s\n",
			req.Code,
			req.UserID,
			req.ExpiresAt.UTC().Format(time.RFC3339),
		)
	}
	_ = w.Flush()
	return 0
}

func runPairingApprove(
	ctx context.Context,
	store *pairing.FileStore,
	code string,
) int {
	userID, ok, err := store.Approve(ctx, code)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	if !ok {
		fmt.Fprintln(os.Stderr, "pairing code not found or expired")
		return 1
	}
	fmt.Printf("approved user: %s\n", userID)
	return 0
}

func openPairingStore(
	ctx context.Context,
	opts runOptions,
	wantChannel string,
) (*pairing.FileStore, error) {
	spec, err := resolveTelegramPairingChannel(opts, wantChannel)
	if err != nil {
		return nil, err
	}

	var cfg telegramChannelConfig
	if err := registry.DecodeStrict(spec.Config, &cfg); err != nil {
		return nil, err
	}

	token := strings.TrimSpace(cfg.Token)
	if token == "" {
		return nil, errors.New("pairing: missing telegram channel token")
	}

	resolvedStateDir, err := resolveStateDir(opts.StateDir)
	if err != nil {
		return nil, err
	}

	netOpts, err := telegramClientNetOptions(cfg)
	if err != nil {
		return nil, err
	}
	apiOpts, err := tgapi.BuildClientOptionsFromEnv(netOpts)
	if err != nil {
		return nil, err
	}

	bot, err := probeBotInfo(ctx, token, apiOpts...)
	if err != nil {
		return nil, err
	}

	path, err := tgch.PairingStorePath(resolvedStateDir, bot)
	if err != nil {
		return nil, err
	}

	storeOpts, err := pairingStoreOptions(cfg.PairingTTL)
	if err != nil {
		return nil, err
	}
	return pairing.NewFileStore(path, storeOpts...)
}

func resolveTelegramPairingChannel(
	opts runOptions,
	wantChannel string,
) (pluginSpec, error) {
	specs := resolveTelegramChannelSpecs(opts.Channels)

	if len(specs) == 0 {
		return pluginSpec{}, errors.New("pairing: telegram not configured")
	}

	name := strings.TrimSpace(wantChannel)
	if name == "" {
		if len(specs) == 1 {
			return specs[0], nil
		}
		return pluginSpec{}, errors.New(
			"pairing: multiple telegram channels configured; " +
				"use -channel to select one",
		)
	}

	for i := range specs {
		spec := specs[i]
		if strings.EqualFold(strings.TrimSpace(spec.Name), name) {
			return spec, nil
		}
	}
	return pluginSpec{}, fmt.Errorf(
		"pairing: telegram channel not found: %s",
		name,
	)
}

func normalizePairingArgs(args []string) ([]string, error) {
	var (
		flagArgs []string
		posArgs  []string
	)

	for i := 0; i < len(args); {
		arg := args[i]
		if arg == "--" {
			posArgs = append(posArgs, args[i+1:]...)
			break
		}
		if !strings.HasPrefix(arg, "-") || arg == "-" {
			posArgs = append(posArgs, arg)
			i++
			continue
		}

		name := flagName(arg)
		if strings.Contains(arg, "=") ||
			(name != flagConfig && name != flagChannel &&
				name != flagStateDir) {
			flagArgs = append(flagArgs, arg)
			i++
			continue
		}

		if i+1 >= len(args) {
			return nil, fmt.Errorf("pairing: missing value for %s", arg)
		}
		flagArgs = append(flagArgs, arg, args[i+1])
		i += 2
	}

	out := make([]string, 0, len(args))
	out = append(out, flagArgs...)
	out = append(out, posArgs...)
	return out, nil
}

func flagName(arg string) string {
	trimmed := strings.TrimLeft(arg, "-")
	if idx := strings.IndexByte(trimmed, '='); idx >= 0 {
		return trimmed[:idx]
	}
	return trimmed
}
