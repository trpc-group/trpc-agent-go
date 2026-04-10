//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package main

import (
	"context"
	"fmt"
	"log"
	"strings"

	mcp "trpc.group/trpc-go/trpc-mcp-go"
)

func main() {
	server := mcp.NewStdioServer("mcpbroker-demo-server", "1.0.0")

	echoTool := mcp.NewTool(
		"echo",
		mcp.WithDescription("Echo text back to the caller with an optional prefix."),
		mcp.WithString("text", mcp.Required(), mcp.Description("Text to echo.")),
		mcp.WithString("prefix", mcp.Description("Optional prefix. Defaults to 'Echo: '.")),
	)
	server.RegisterTool(echoTool, func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		text, _ := req.Params.Arguments["text"].(string)
		prefix, _ := req.Params.Arguments["prefix"].(string)
		if strings.TrimSpace(prefix) == "" {
			prefix = "Echo: "
		}
		return mcp.NewTextResult(prefix + text), nil
	})

	addTool := mcp.NewTool(
		"add",
		mcp.WithDescription("Add two numbers and return the numeric result."),
		mcp.WithNumber("a", mcp.Required(), mcp.Description("First number.")),
		mcp.WithNumber("b", mcp.Required(), mcp.Description("Second number.")),
	)
	server.RegisterTool(addTool, func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		a, _ := req.Params.Arguments["a"].(float64)
		b, _ := req.Params.Arguments["b"].(float64)
		return mcp.NewTextResult(fmt.Sprintf("%g", a+b)), nil
	})

	issueCreateTool := mcp.NewTool(
		"issue_create",
		mcp.WithDescription("Create a project issue with title, optional description, and priority."),
		mcp.WithString("title", mcp.Required(), mcp.Description("Issue title.")),
		mcp.WithString("description", mcp.Description("Optional issue description.")),
		mcp.WithString("priority", mcp.Description("Optional priority such as low, medium, or high.")),
	)
	server.RegisterTool(issueCreateTool, func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		title, _ := req.Params.Arguments["title"].(string)
		description, _ := req.Params.Arguments["description"].(string)
		priority, _ := req.Params.Arguments["priority"].(string)
		if strings.TrimSpace(priority) == "" {
			priority = "medium"
		}
		return mcp.NewTextResult(
			fmt.Sprintf("Created issue %q with priority=%s description=%q", title, priority, description),
		), nil
	})

	issueListTool := mcp.NewTool(
		"issue_list",
		mcp.WithDescription("List project issues filtered by optional status or assignee."),
		mcp.WithString("status", mcp.Description("Optional status filter such as open or closed.")),
		mcp.WithString("assignee", mcp.Description("Optional assignee name.")),
	)
	server.RegisterTool(issueListTool, func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		status, _ := req.Params.Arguments["status"].(string)
		assignee, _ := req.Params.Arguments["assignee"].(string)
		if strings.TrimSpace(status) == "" {
			status = "open"
		}
		if strings.TrimSpace(assignee) == "" {
			assignee = "anyone"
		}
		return mcp.NewTextResult(
			fmt.Sprintf("Listing %s issues assigned to %s", status, assignee),
		), nil
	})

	issueCommentTool := mcp.NewTool(
		"issue_comment",
		mcp.WithDescription("Add a comment to an existing issue."),
		mcp.WithString("issue_id", mcp.Required(), mcp.Description("Issue identifier.")),
		mcp.WithString("body", mcp.Required(), mcp.Description("Comment content.")),
	)
	server.RegisterTool(issueCommentTool, func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		issueID, _ := req.Params.Arguments["issue_id"].(string)
		body, _ := req.Params.Arguments["body"].(string)
		return mcp.NewTextResult(
			fmt.Sprintf("Added comment to issue %s: %q", issueID, body),
		), nil
	})

	docSearchTool := mcp.NewTool(
		"doc_search",
		mcp.WithDescription("Search documentation articles by query and optional product area."),
		mcp.WithString("query", mcp.Required(), mcp.Description("Search query.")),
		mcp.WithString("product", mcp.Description("Optional product area filter.")),
	)
	server.RegisterTool(docSearchTool, func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		query, _ := req.Params.Arguments["query"].(string)
		product, _ := req.Params.Arguments["product"].(string)
		if strings.TrimSpace(product) == "" {
			product = "all"
		}
		return mcp.NewTextResult(
			fmt.Sprintf("Found docs for query=%q in product=%q", query, product),
		), nil
	})

	docReadTool := mcp.NewTool(
		"doc_read",
		mcp.WithDescription("Read a documentation article by document identifier."),
		mcp.WithString("doc_id", mcp.Required(), mcp.Description("Document identifier.")),
	)
	server.RegisterTool(docReadTool, func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		docID, _ := req.Params.Arguments["doc_id"].(string)
		return mcp.NewTextResult(fmt.Sprintf("Reading document %s", docID)), nil
	})

	calendarCreateTool := mcp.NewTool(
		"calendar_create",
		mcp.WithDescription("Create a calendar event with title and start time."),
		mcp.WithString("title", mcp.Required(), mcp.Description("Event title.")),
		mcp.WithString("start_time", mcp.Required(), mcp.Description("Event start time in ISO-8601 format.")),
		mcp.WithString("attendee", mcp.Description("Optional attendee email.")),
	)
	server.RegisterTool(calendarCreateTool, func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		title, _ := req.Params.Arguments["title"].(string)
		startTime, _ := req.Params.Arguments["start_time"].(string)
		attendee, _ := req.Params.Arguments["attendee"].(string)
		return mcp.NewTextResult(
			fmt.Sprintf("Created calendar event %q at %s attendee=%q", title, startTime, attendee),
		), nil
	})

	calendarListTool := mcp.NewTool(
		"calendar_list",
		mcp.WithDescription("List upcoming calendar events for an optional day."),
		mcp.WithString("day", mcp.Description("Optional day filter in YYYY-MM-DD format.")),
	)
	server.RegisterTool(calendarListTool, func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		day, _ := req.Params.Arguments["day"].(string)
		if strings.TrimSpace(day) == "" {
			day = "today"
		}
		return mcp.NewTextResult(fmt.Sprintf("Listing calendar events for %s", day)), nil
	})

	meetingScheduleTool := mcp.NewTool(
		"meeting_schedule",
		mcp.WithDescription("Schedule an online meeting with topic and participant count."),
		mcp.WithString("topic", mcp.Required(), mcp.Description("Meeting topic.")),
		mcp.WithNumber("participants", mcp.Description("Optional participant count estimate.")),
	)
	server.RegisterTool(meetingScheduleTool, func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		topic, _ := req.Params.Arguments["topic"].(string)
		participants, _ := req.Params.Arguments["participants"].(float64)
		return mcp.NewTextResult(
			fmt.Sprintf("Scheduled meeting topic=%q participants=%g", topic, participants),
		), nil
	})

	meetingCancelTool := mcp.NewTool(
		"meeting_cancel",
		mcp.WithDescription("Cancel an online meeting by meeting identifier."),
		mcp.WithString("meeting_id", mcp.Required(), mcp.Description("Meeting identifier.")),
	)
	server.RegisterTool(meetingCancelTool, func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		meetingID, _ := req.Params.Arguments["meeting_id"].(string)
		return mcp.NewTextResult(fmt.Sprintf("Cancelled meeting %s", meetingID)), nil
	})

	if err := server.Start(); err != nil {
		log.Fatal(err)
	}
}
