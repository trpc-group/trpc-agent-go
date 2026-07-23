//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package reviewer

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"

	"trpc.group/trpc-go/trpc-agent-go/tool"
)

// Approver owns the terminal side of permission requests for one reviewer run.
// The mutex prevents overlapping prompts. terminalErr permanently disables
// further reads after cancellation so a late response cannot approve a later
// request. skip is the fake-model path through the same permission pipeline.
type Approver struct {
	mu          sync.Mutex
	reader      *bufio.Reader
	writer      io.Writer
	skip        bool
	terminalErr error
}

type approvalResponse struct {
	answer string
	err    error
}

var errApprovalInputUnavailable = errors.New(
	"interactive approval input is unavailable after a canceled decision",
)

// newApprover creates the concrete terminal approver shared by real and fake
// runs. Fake mode sets skip; it does not replace the permission pipeline.
func newApprover(config ApprovalConfig, skip bool) *Approver {
	var reader *bufio.Reader
	if config.Input != nil {
		reader = bufio.NewReader(config.Input)
	}
	return &Approver{
		reader: reader,
		writer: config.Output,
		skip:   skip,
	}
}

// readResponse binds one terminal read to one approval decision. A generic
// io.Reader cannot be canceled, so the private buffered channel lets a late
// response finish without blocking or becoming input for another decision.
func (a *Approver) readResponse() <-chan approvalResponse {
	responses := make(chan approvalResponse, 1)
	go func() {
		answer, err := a.reader.ReadString('\n')
		responses <- approvalResponse{answer: answer, err: err}
	}()
	return responses
}

// decide performs one serialized user decision. skip is the fake-model path;
// missing terminal I/O returns Ask instead of silently approving. Cancellation
// permanently closes this Approver because a generic Reader cannot retract a
// late response safely for reuse by another decision.
func (a *Approver) decide(
	ctx context.Context,
	toolName string,
	command string,
	reason string,
) (tool.PermissionDecision, error) {
	if err := ctx.Err(); err != nil {
		return tool.PermissionDecision{}, err
	}
	if a != nil && a.skip {
		return tool.AllowPermission(), nil
	}
	if a == nil || a.reader == nil || a.writer == nil {
		return tool.PermissionDecision{}, fmt.Errorf("interactive approval is not available")
	}

	a.mu.Lock()
	defer a.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return tool.PermissionDecision{}, err
	}
	if a.terminalErr != nil {
		return tool.PermissionDecision{}, a.terminalErr
	}
	for {
		if _, err := fmt.Fprintf(a.writer,
			"\nThe review agent requests permission to use a governed tool.\nTarget tool: %s\nTarget arguments: %s\nReason: %s\nApprove? [Y/n] ",
			toolName,
			command,
			reason,
		); err != nil {
			return tool.PermissionDecision{}, fmt.Errorf("write approval prompt: %w", err)
		}
		responses := a.readResponse()
		var response approvalResponse
		select {
		case <-ctx.Done():
			a.terminalErr = errApprovalInputUnavailable
			return tool.PermissionDecision{}, ctx.Err()
		case response = <-responses:
		}
		if response.err != nil && len(response.answer) == 0 {
			if response.err == io.EOF {
				return tool.AskPermission("interactive approval input ended before a decision"), nil
			}
			return tool.PermissionDecision{}, fmt.Errorf("read approval response: %w", response.err)
		}
		switch strings.ToLower(strings.TrimSpace(response.answer)) {
		case "", "y", "yes":
			return tool.AllowPermission(), nil
		case "n", "no":
			return tool.DenyPermission("user denied the requested tool execution"), nil
		}
	}
}