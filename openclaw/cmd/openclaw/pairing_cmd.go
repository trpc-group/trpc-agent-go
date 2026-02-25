//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package main

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

	pairingCmdList    = "list"
	pairingCmdApprove = "approve"
)

func runPairing(args []string) int {
	fs := flag.NewFlagSet(subcmdPairing, flag.ContinueOnError)
	fs.SetOutput(os.Stderr)

	token := fs.String(
		"telegram-token",
		"",
		"Telegram bot token (required)",
	)
	stateDir := fs.String(
		"state-dir",
		"",
		"State dir (default: $HOME/.trpc-agent-go/openclaw)",
	)

	if err := fs.Parse(args); err != nil {
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
	fmt.Fprintln(os.Stderr,
		"  openclaw pairing list -telegram-token <TOKEN> [-state-dir <DIR>]",
	)
	fmt.Fprintln(os.Stderr,
		"  openclaw pairing approve <CODE> -telegram-token <TOKEN>"+
			" [-state-dir <DIR>]",
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
		return nil, errors.New("pairing: missing -telegram-token")
	}

	resolvedStateDir, err := resolveStateDir(rawStateDir)
	if err != nil {
		return nil, err
	}

	bot, err := tgch.ProbeBotInfo(ctx, token)
	if err != nil {
		return nil, err
	}

	path, err := tgch.PairingStorePath(resolvedStateDir, bot)
	if err != nil {
		return nil, err
	}

	return pairing.NewFileStore(path)
}
