//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package main

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"strings"
)

func (a *application) printBanner(output io.Writer) {
	fmt.Fprintln(output, "System One approval agent")
	fmt.Fprintf(output, "Main model: %s\nReviewer: %s (%s)\n", a.config.modelName, a.config.reviewModel, a.config.reviewBaseURL)
	fmt.Fprintf(output, "Host working directory: %s\nSession: %s\n", a.config.baseDir, a.sessionID)
	fmt.Fprintf(output, "Approval: risk < %d, confidence/probability >= %.2f\n", a.config.riskThreshold, a.config.minConfidence)
	fmt.Fprintln(output, "Approved commands execute on this host. The working directory is not a sandbox.")
	fmt.Fprintln(output, "Commands: /help, /exit")
}

func (a *application) chat(ctx context.Context, input io.Reader, output io.Writer) error {
	scanner := bufio.NewScanner(input)
	scanner.Buffer(make([]byte, 4096), 1024*1024)
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		fmt.Fprint(output, "You: ")
		if !scanner.Scan() {
			if err := ctx.Err(); err != nil {
				return err
			}
			return scanner.Err()
		}
		prompt := strings.TrimSpace(scanner.Text())
		switch prompt {
		case "":
			continue
		case "/exit":
			return nil
		case "/help":
			fmt.Fprintln(output, "Try: Run pwd and list the files in the current directory.")
			fmt.Fprintln(output, "Follow up: Explain the command output from the previous turn.")
			fmt.Fprintln(output, "All hostexec tools require review. Denials and review failures are returned to the agent as tool results.")
			continue
		}
		if err := a.runTurn(ctx, prompt, output); err != nil {
			return err
		}
	}
}
