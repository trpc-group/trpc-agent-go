//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package email provides send email tools for AI agents.
// This tool can send emails to personal email and some Corporate Email.
package email

import (
	"context"

	"trpc.group/trpc-go/trpc-agent-go/tool"
)

const (
	// default name
	defaultName = "email"
)

// MailboxType mailbox
type MailboxType int32

const (
	// MAIL_UNKNOWN unknown mail
	MAIL_UNKNOWN MailboxType = 0
	// MAIL_QQ qq mail
	MAIL_QQ MailboxType = 1
	// MAIL_163 163 mail
	MAIL_163 MailboxType = 2
	// MAIL_GMAIL google mail
	MAIL_GMAIL MailboxType = 3
)

// MailboxTypeToString convert mailbox type to string
func MailboxTypeToString(mailboxType MailboxType) string {
	switch mailboxType {
	// qq mail
	case MAIL_QQ:
		return "qq"
	// 163 mail
	case MAIL_163:
		return "163"
	// google mail
	case MAIL_GMAIL:
		return "gmail"
	// unknown mail
	default:
		return "unknown"
	}
}

// Option is a functional option for configuring the file tool set.
type Option func(*emailToolSet)

// emailToolSet implements the ToolSet interface for file operations.
type emailToolSet struct {
	sendEmailEnabled bool
	tools            []tool.Tool
}

// Tools implements the ToolSet interface.
func (e *emailToolSet) Tools(_ context.Context) []tool.Tool {
	return e.tools
}

// Name implements the ToolSet interface.
func (e *emailToolSet) Name() string {
	return "email"
}

// Close implements the ToolSet interface.
func (e *emailToolSet) Close() error {
	// No resources to clean up for file tools.
	return nil
}

// NewToolSet creates a new file tool set with the given options.
func NewToolSet(opts ...Option) (tool.ToolSet, error) {
	emailToolSet := &emailToolSet{
		sendEmailEnabled: true,
		tools:            nil,
	}

	// Apply user-provided options.
	for _, opt := range opts {
		opt(emailToolSet)
	}

	// Create function tools based on enabled features.
	var tools []tool.Tool
	if emailToolSet.sendEmailEnabled {
		tools = append(tools, emailToolSet.sendMailTool())
	}
	emailToolSet.tools = tools
	return emailToolSet, nil
}
