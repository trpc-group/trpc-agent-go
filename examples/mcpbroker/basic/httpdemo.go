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
	"net/http/httptest"
	"strings"

	mcp "trpc.group/trpc-go/trpc-mcp-go"
)

const remoteSkillName = "remote-http-mcp"

type remoteHTTPDemo struct {
	server *httptest.Server
	url    string
}

func startRemoteHTTPDemoServer() *remoteHTTPDemo {
	server := mcp.NewServer(
		"mcpbroker-remote-http-demo",
		"1.0.0",
		mcp.WithServerPath("/mcp"),
	)

	pingTool := mcp.NewTool(
		"service_ping",
		mcp.WithDescription("Check whether the remote announcement MCP service is reachable."),
	)
	server.RegisterTool(pingTool, func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return mcp.NewTextResult("remote announcement MCP service is healthy"), nil
	})

	announcementPublishTool := mcp.NewTool(
		"announcement_publish",
		mcp.WithDescription("Publish an internal announcement with title, audience, and body."),
		mcp.WithString("title", mcp.Required(), mcp.Description("Announcement title.")),
		mcp.WithString("audience", mcp.Required(), mcp.Description("Target audience such as engineers or sales.")),
		mcp.WithString("body", mcp.Required(), mcp.Description("Announcement body text.")),
	)
	server.RegisterTool(announcementPublishTool, func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		_ = ctx
		title, _ := req.Params.Arguments["title"].(string)
		audience, _ := req.Params.Arguments["audience"].(string)
		body, _ := req.Params.Arguments["body"].(string)
		return mcp.NewTextResult(
			fmt.Sprintf("Published announcement %q to %s: %s", title, audience, body),
		), nil
	})

	announcementListTool := mcp.NewTool(
		"announcement_list",
		mcp.WithDescription("List recent announcements for an optional audience."),
		mcp.WithString("audience", mcp.Description("Optional target audience filter.")),
	)
	server.RegisterTool(announcementListTool, func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		_ = ctx
		audience, _ := req.Params.Arguments["audience"].(string)
		if strings.TrimSpace(audience) == "" {
			audience = "everyone"
		}
		return mcp.NewTextResult(
			fmt.Sprintf("Recent announcements for %s: release-freeze, broker-rollout", audience),
		), nil
	})

	faqSearchTool := mcp.NewTool(
		"faq_search",
		mcp.WithDescription("Search remote FAQ entries by query."),
		mcp.WithString("query", mcp.Required(), mcp.Description("FAQ search query.")),
	)
	server.RegisterTool(faqSearchTool, func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		_ = ctx
		query, _ := req.Params.Arguments["query"].(string)
		return mcp.NewTextResult(
			fmt.Sprintf("FAQ matches for %q: faq-remote-setup, faq-broker-routing", query),
		), nil
	})

	faqReadTool := mcp.NewTool(
		"faq_read",
		mcp.WithDescription("Read a remote FAQ entry by id."),
		mcp.WithString("entry_id", mcp.Required(), mcp.Description("FAQ entry id.")),
	)
	server.RegisterTool(faqReadTool, func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		_ = ctx
		entryID, _ := req.Params.Arguments["entry_id"].(string)
		return mcp.NewTextResult(
			fmt.Sprintf("FAQ %s: use mcp_list_tools and mcp_inspect_tools before mcp_call when the remote tool surface is unknown", entryID),
		), nil
	})

	httpServer := httptest.NewServer(server.HTTPHandler())
	return &remoteHTTPDemo{
		server: httpServer,
		url:    httpServer.URL + "/mcp",
	}
}

func (d *remoteHTTPDemo) Close() {
	if d == nil || d.server == nil {
		return
	}
	d.server.Close()
}
