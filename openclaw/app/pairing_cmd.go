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
)

const (
	subcmdPairing = "pairing"

	flagTelegramToken = "telegram-token"
	flagStateDir      = "state-dir"

	pairingCmdList    = "list"
	pairingCmdApprove = "approve"
)

var probeBotInfo = func(
	ctx context.Context,
	token string,
) (tgch.BotInfo, error) {
	return tgch.ProbeBotInfo(ctx, token)
}

func runPairing(args []string) int {
	fs := flag.NewFlagSet(subcmdPairing, flag.ContinueOnError)
	fs.SetOutput(os.Stderr)

	token := fs.String(
		flagTelegramToken,
		"",
		"Telegram bot token (required)",
	)
	stateDir := fs.String(
		flagStateDir,
		"",
		"State dir (default: $HOME/.trpc-agent-go/openclaw)",
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

	ctx := context.Background()
	switch action {
	case pairingCmdList:
		return runPairingList(ctx, *token, *stateDir)
	case pairingCmdApprove:
		if len(rest) < 2 {
			fmt.Fprintln(os.Stderr, "missing pairing code")
			printPairingUsage()
			return 2
		}
		return runPairingApprove(ctx, *token, *stateDir, rest[1])
	default:
		fmt.Fprintf(os.Stderr, "unknown pairing command: %s\n", action)
		printPairingUsage()
		return 2
	}
}

func printPairingUsage() {
	fmt.Fprintln(os.Stderr, "Usage:")
	fmt.Fprintf(
		os.Stderr,
		"  openclaw pairing list -%s <TOKEN> [-%s <DIR>]\n",
		flagTelegramToken,
		flagStateDir,
	)
	fmt.Fprintf(
		os.Stderr,
		"  openclaw pairing approve <CODE> -%s <TOKEN> [-%s <DIR>]\n",
		flagTelegramToken,
		flagStateDir,
	)
}

func runPairingList(
	ctx context.Context,
	token string,
	rawStateDir string,
) int {
	store, err := openPairingStore(ctx, token, rawStateDir)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}

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
	token string,
	rawStateDir string,
	code string,
) int {
	store, err := openPairingStore(ctx, token, rawStateDir)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}

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
	token string,
	rawStateDir string,
) (*pairing.FileStore, error) {
	if strings.TrimSpace(token) == "" {
		return nil, errors.New("pairing: missing -" + flagTelegramToken)
	}

	resolvedStateDir, err := resolveStateDir(rawStateDir)
	if err != nil {
		return nil, err
	}

	bot, err := probeBotInfo(ctx, token)
	if err != nil {
		return nil, err
	}

	path, err := tgch.PairingStorePath(resolvedStateDir, bot)
	if err != nil {
		return nil, err
	}

	return pairing.NewFileStore(path)
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
			(name != flagTelegramToken && name != flagStateDir) {
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
