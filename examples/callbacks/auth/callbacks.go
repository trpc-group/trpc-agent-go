//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
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
	"fmt"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

// createAgentCallbacks creates agent callbacks for user context injection.
func (e *userContextExample) createAgentCallbacks() *agent.Callbacks {
	callbacks := agent.NewCallbacks()
	callbacks.RegisterBeforeAgent(e.createBeforeAgentCallback())
	callbacks.RegisterAfterAgent(e.createAfterAgentCallback())
	return callbacks
}

// createToolCallbacks creates tool callbacks for authorization and audit.
func (e *userContextExample) createToolCallbacks() *tool.Callbacks {
	callbacks := tool.NewCallbacks()
	callbacks.RegisterBeforeTool(e.createBeforeToolCallback())
	callbacks.RegisterAfterTool(e.createAfterToolCallback())
	return callbacks
}

// createBeforeAgentCallback creates the before agent callback.
func (e *userContextExample) createBeforeAgentCallback() agent.BeforeAgentCallbackStructured {
	return func(ctx context.Context, args *agent.BeforeAgentArgs) (*agent.BeforeAgentResult, error) {
		// Inject user context into invocation state.
		// In a real application, you would get this from request metadata,
		// JWT token, session, etc.
		userCtx := &UserContext{
			UserID:      e.userID,
			Role:        e.role,
			Permissions: getPermissionsForRole(e.role),
		}

		args.Invocation.SetState("custom:user_context", userCtx)

		fmt.Printf("👤 User Context Injected: %s (role: %s, permissions: %v)\n",
			userCtx.UserID, userCtx.Role, userCtx.Permissions)
		fmt.Println()

		return nil, nil
	}
}

// createAfterAgentCallback creates the after agent callback.
func (e *userContextExample) createAfterAgentCallback() agent.AfterAgentCallbackStructured {
	return func(ctx context.Context, args *agent.AfterAgentArgs) (*agent.AfterAgentResult, error) {
		// Print audit summary.
		if auditLogVal, ok := args.Invocation.GetState("custom:audit_log"); ok {
			auditLog := auditLogVal.([]AuditEntry)
			if len(auditLog) > 0 {
				fmt.Println()
				fmt.Println("📋 Audit Summary:")
				for _, entry := range auditLog {
					status := "✅"
					if entry.Error != "" {
						status = "❌"
					}
					fmt.Printf("   %s [%s] %s (%s) → %s\n",
						status, entry.Timestamp, entry.UserID, entry.Role, entry.ToolName)
					if entry.Error != "" {
						fmt.Printf("      Error: %s\n", entry.Error)
					}
				}
			}
			// Clean up audit log.
			args.Invocation.DeleteState("custom:audit_log")
		}

		// Clean up user context.
		args.Invocation.DeleteState("custom:user_context")

		return nil, nil
	}
}

// createBeforeToolCallback creates the before tool callback for authorization.
func (e *userContextExample) createBeforeToolCallback() tool.BeforeToolCallbackStructured {
	return func(ctx context.Context, args *tool.BeforeToolArgs) (*tool.BeforeToolResult, error) {
		// Get invocation from context.
		inv, ok := agent.InvocationFromContext(ctx)
		if !ok || inv == nil {
			return nil, errors.New("invocation not found in context")
		}

		// Get user context from invocation state.
		userCtxVal, ok := inv.GetState("custom:user_context")
		if !ok {
			return nil, errors.New("user context not found - authentication required")
		}
		userCtx := userCtxVal.(*UserContext)

		// Check if user has permission to use this tool.
		if !hasPermission(userCtx, args.ToolName) {
			errMsg := fmt.Sprintf("permission denied: user %s (role: %s) cannot use tool %s",
				userCtx.UserID, userCtx.Role, args.ToolName)
			fmt.Printf("❌ %s\n", errMsg)

			// Log failed authorization attempt.
			e.appendAuditLog(inv, AuditEntry{
				Timestamp: time.Now().Format(time.RFC3339),
				UserID:    userCtx.UserID,
				Role:      userCtx.Role,
				ToolName:  args.ToolName,
				Error:     "permission denied",
			})

			return nil, errors.New(errMsg)
		}

		fmt.Printf("✅ Permission granted: %s (%s) can use %s\n",
			userCtx.UserID, userCtx.Role, args.ToolName)

		return nil, nil
	}
}

// createAfterToolCallback creates the after tool callback for audit logging.
func (e *userContextExample) createAfterToolCallback() tool.AfterToolCallbackStructured {
	return func(ctx context.Context, args *tool.AfterToolArgs) (*tool.AfterToolResult, error) {
		// Get invocation from context.
		inv, ok := agent.InvocationFromContext(ctx)
		if !ok || inv == nil {
			return nil, nil
		}

		// Get user context from invocation state.
		userCtxVal, ok := inv.GetState("custom:user_context")
		if !ok {
			return nil, nil
		}
		userCtx := userCtxVal.(*UserContext)

		// Create audit entry.
		entry := AuditEntry{
			Timestamp: time.Now().Format(time.RFC3339),
			UserID:    userCtx.UserID,
			Role:      userCtx.Role,
			ToolName:  args.ToolName,
			Args:      string(args.Arguments),
		}

		if args.Error != nil {
			entry.Error = args.Error.Error()
		} else if args.Result != nil {
			entry.Result = fmt.Sprintf("%v", args.Result)
		}

		// Append to audit log.
		e.appendAuditLog(inv, entry)

		fmt.Printf("📝 Audit: User %s (%s) called %s\n", userCtx.UserID, userCtx.Role, args.ToolName)
		if args.Error != nil {
			fmt.Printf("   Error: %v\n", args.Error)
		}

		return nil, nil
	}
}

// appendAuditLog appends an audit entry to the invocation state.
func (e *userContextExample) appendAuditLog(inv *agent.Invocation, entry AuditEntry) {
	var auditLog []AuditEntry

	if auditLogVal, ok := inv.GetState("custom:audit_log"); ok {
		auditLog = auditLogVal.([]AuditEntry)
	}

	auditLog = append(auditLog, entry)
	inv.SetState("custom:audit_log", auditLog)
}
