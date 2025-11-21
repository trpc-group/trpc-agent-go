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

type MailboxType int32

const (
	// unknown mail
	MAIL_UNKNOWN MailboxType = 0
	// qq mail
	MAIL_QQ MailboxType = 1
	// 163 mail
	MAIL_163 MailboxType = 2
	// google email
	MAIL_GMAIL MailboxType = 3
)

func MailboxTypeToString(mailboxType MailboxType) string {
	switch mailboxType {
	case MAIL_QQ:
		return "qq"
	case MAIL_163:
		return "163"
	case MAIL_GMAIL:
		return "gmail"
	default:
		return "unknown"
	}
}

// Option is a functional option for configuring the file tool set.
type Option func(*emailToolSet)

// WithSendEmailEnabled enables or disables the send email functionality, default is true.
func WithSendEmailEnabled(enabled bool) Option {
	return func(f *emailToolSet) {
		f.sendEmailEnabled = enabled
	}
}

// WithName sets the name of the email tool set.
func WithName(name string) Option {
	return func(f *emailToolSet) {
		f.name = name
	}
}

// emailToolSet implements the ToolSet interface for file operations.
type emailToolSet struct {
	sendEmailEnabled bool
	tools            []tool.Tool
	name             string
}

// Tools implements the ToolSet interface.
func (e *emailToolSet) Tools(ctx context.Context) []tool.Tool {
	return e.tools
}

// Name implements the ToolSet interface.
func (e *emailToolSet) Name() string {
	return e.name
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
		name:             defaultName,
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
