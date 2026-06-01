//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package openviking

import (
	"context"
	"encoding/json"
	"fmt"

	"trpc.group/trpc-go/trpc-agent-go/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool/function"
	"trpc.group/trpc-go/trpc-agent-go/tool/openviking/internal/client"
)

// ToolName identifies an OpenViking tool. Use the exported Tool* constants
// with WithTools; the string values use the viking_* prefix so models see the
// same operations exposed by OpenViking's MCP/plugin integrations.
type ToolName string

// Exported tool names for use with WithTools.
const (
	ToolFind        ToolName = "viking_find"
	ToolSearch      ToolName = "viking_search"
	ToolBrowse      ToolName = "viking_browse"
	ToolRead        ToolName = "viking_read"
	ToolGrep        ToolName = "viking_grep"
	ToolStore       ToolName = "viking_store"
	ToolAddResource ToolName = "viking_add_resource"
	ToolAddSkill    ToolName = "viking_add_skill"
	ToolHealth      ToolName = "viking_health"
	ToolForget      ToolName = "viking_forget"
)

const defaultRetrievalLimit = 8

// buildTools constructs the requested tools in the given order. It fails fast
// on any unknown tool name so a typo in WithTools surfaces immediately
// instead of silently shrinking the exposed capability set.
func buildTools(c *client.Client, names []ToolName) ([]tool.Tool, error) {
	// hasRead controls whether retrieval tools advertise viking_read; the
	// "search then read" hint must not point the model at an absent tool.
	hasRead := containsTool(names, ToolRead)
	factories := map[ToolName]func(*client.Client) tool.Tool{
		ToolFind:        func(c *client.Client) tool.Tool { return newFindTool(c, hasRead) },
		ToolSearch:      func(c *client.Client) tool.Tool { return newSearchTool(c, hasRead) },
		ToolBrowse:      newBrowseTool,
		ToolRead:        newReadTool,
		ToolGrep:        newGrepTool,
		ToolStore:       newStoreTool,
		ToolAddResource: newAddResourceTool,
		ToolAddSkill:    newAddSkillTool,
		ToolHealth:      newHealthTool,
		ToolForget:      newForgetTool,
	}
	tools := make([]tool.Tool, 0, len(names))
	for _, name := range names {
		factory, ok := factories[name]
		if !ok {
			return nil, fmt.Errorf("openviking: unknown tool name %q", name)
		}
		tools = append(tools, factory(c))
	}
	return tools, nil
}

// retrievalHit is a flattened retrieval result item returned to the model.
type retrievalHit struct {
	Type     string  `json:"type"`
	URI      string  `json:"uri"`
	Score    float64 `json:"score"`
	Level    int     `json:"level"`
	Abstract string  `json:"abstract"`
}

// retrievalOutput is the output of viking_find / viking_search.
type retrievalOutput struct {
	Hits []retrievalHit `json:"hits"`
	Hint string         `json:"hint,omitempty"`
}

func flatten(res *client.RetrievalResult, hasRead bool) retrievalOutput {
	out := retrievalOutput{}
	add := func(typ string, items []client.Item) {
		for _, it := range items {
			out.Hits = append(out.Hits, retrievalHit{
				Type:     typ,
				URI:      it.URI,
				Score:    it.Score,
				Level:    it.Level,
				Abstract: firstNonEmpty(it.Abstract, it.Overview),
			})
		}
	}
	add("memory", res.Memories)
	add("resource", res.Resources)
	add("skill", res.Skills)
	switch {
	case len(out.Hits) == 0:
		out.Hint = "No OpenViking contexts matched."
	case hasRead:
		out.Hint = "Use viking_read with a uri above to fetch full content."
	default:
		out.Hint = "Each hit includes a short summary above."
	}
	return out
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}

func scorePtr(minScore float64) *float64 {
	if minScore <= 0 {
		return nil
	}
	return &minScore
}

func normalizeLimit(limit int) int {
	if limit <= 0 {
		return defaultRetrievalLimit
	}
	return limit
}

// truncateRunes caps the content to maxChars runes (not bytes), so multi-byte
// UTF-8 text is never cut mid-character. maxChars <= 0 means no limit.
func truncateRunes(content string, maxChars int) (string, bool) {
	if maxChars <= 0 {
		return content, false
	}
	runes := []rune(content)
	if len(runes) <= maxChars {
		return content, false
	}
	return string(runes[:maxChars]), true
}

// ===== viking_find =====

type findArgs struct {
	Query     string  `json:"query" jsonschema:"description=Semantic query to recall OpenViking contexts"`
	TargetURI string  `json:"target_uri,omitempty" jsonschema:"description=Optional viking:// URI prefix to limit the search scope"`
	Limit     int     `json:"limit,omitempty" jsonschema:"description=Maximum number of hits (default 8)"`
	MinScore  float64 `json:"min_score,omitempty" jsonschema:"description=Minimum relevance score threshold"`
}

func newFindTool(c *client.Client, hasRead bool) tool.Tool {
	fn := func(ctx context.Context, a findArgs) (retrievalOutput, error) {
		res, err := c.Find(ctx, client.FindRequest{
			Query:          a.Query,
			TargetURI:      a.TargetURI,
			Limit:          normalizeLimit(a.Limit),
			ScoreThreshold: scorePtr(a.MinScore),
		})
		if err != nil {
			return retrievalOutput{}, err
		}
		return flatten(res, hasRead), nil
	}
	desc := "Quick semantic recall from OpenViking without session context. " +
		"Returns matching viking:// URIs with short summaries (not full content)."
	if hasRead {
		desc += " Call viking_read on a URI to fetch full content."
	}
	return function.NewFunctionTool(fn,
		function.WithName(string(ToolFind)),
		function.WithDescription(desc))
}

// ===== viking_search =====

type searchArgs struct {
	Query     string  `json:"query" jsonschema:"description=Semantic query to recall OpenViking contexts"`
	TargetURI string  `json:"target_uri,omitempty" jsonschema:"description=Optional viking:// URI prefix to limit the search scope"`
	SessionID string  `json:"session_id,omitempty" jsonschema:"description=Optional session id for context-aware retrieval"`
	Limit     int     `json:"limit,omitempty" jsonschema:"description=Maximum number of hits (default 8)"`
	MinScore  float64 `json:"min_score,omitempty" jsonschema:"description=Minimum relevance score threshold"`
}

func newSearchTool(c *client.Client, hasRead bool) tool.Tool {
	fn := func(ctx context.Context, a searchArgs) (retrievalOutput, error) {
		res, err := c.Search(ctx, client.FindRequest{
			Query:          a.Query,
			TargetURI:      a.TargetURI,
			SessionID:      a.SessionID,
			Limit:          normalizeLimit(a.Limit),
			ScoreThreshold: scorePtr(a.MinScore),
		})
		if err != nil {
			return retrievalOutput{}, err
		}
		return flatten(res, hasRead), nil
	}
	desc := "Session-aware hierarchical retrieval over OpenViking memories, resources, and skills. " +
		"Returns matching viking:// URIs with short summaries."
	if hasRead {
		desc += " Call viking_read on a URI to fetch full content."
	}
	return function.NewFunctionTool(fn,
		function.WithName(string(ToolSearch)),
		function.WithDescription(desc))
}

// ===== viking_browse =====

type browseArgs struct {
	URI       string `json:"uri,omitempty" jsonschema:"description=viking:// URI to list (default viking://)"`
	Recursive bool   `json:"recursive,omitempty" jsonschema:"description=List recursively"`
	Pattern   string `json:"pattern,omitempty" jsonschema:"description=Optional glob pattern; when set a glob search is performed instead of ls and recursive is ignored"`
}

func newBrowseTool(c *client.Client) tool.Tool {
	fn := func(ctx context.Context, a browseArgs) (string, error) {

		uri := a.URI
		if uri == "" {
			uri = "viking://"
		}
		if a.Pattern != "" {
			raw, err := c.Glob(ctx, a.Pattern, uri)
			if err != nil {
				return "", err
			}
			return string(raw), nil
		}
		raw, err := c.Ls(ctx, uri, a.Recursive)
		if err != nil {
			return "", err
		}
		return string(raw), nil
	}
	return function.NewFunctionTool(fn,
		function.WithName(string(ToolBrowse)),
		function.WithDescription("Browse OpenViking namespaces: list a viking:// URI, or glob when a pattern is provided."))
}

// ===== viking_read =====

type readArgs struct {
	URI         string `json:"uri" jsonschema:"description=viking:// URI to read"`
	ContentMode string `json:"content_mode,omitempty" jsonschema:"description=One of read (L2 full content, default)|overview (L1)|abstract (L0). overview/abstract require a DIRECTORY URI (isDir=true); never append a filename. A search/find hit ending in .abstract.md or .overview.md is itself the summary file - read it with read"`
	Offset      int    `json:"offset,omitempty" jsonschema:"description=Start line for read mode (0-indexed)"`
	Limit       int    `json:"limit,omitempty" jsonschema:"description=Number of lines for read mode (-1 reads to end)"`
	MaxChars    int    `json:"max_chars,omitempty" jsonschema:"description=Truncate the returned content to this many characters (0 means no limit)"`
}

type readOutput struct {
	URI         string `json:"uri"`
	ContentMode string `json:"content_mode"`
	Content     string `json:"content"`
	Truncated   bool   `json:"truncated,omitempty"`
}

func newReadTool(c *client.Client) tool.Tool {
	fn := func(ctx context.Context, a readArgs) (readOutput, error) {
		mode := a.ContentMode
		if mode == "" {
			mode = "read"
		}
		var (
			content string
			err     error
		)
		switch mode {
		case "abstract":
			content, err = c.Abstract(ctx, a.URI)
		case "overview":
			content, err = c.Overview(ctx, a.URI)
		case "read":
			limit := a.Limit
			if limit == 0 {
				limit = -1
			}
			content, err = c.Read(ctx, a.URI, a.Offset, limit)
		default:
			return readOutput{}, fmt.Errorf("invalid content_mode %q: must be abstract, overview, or read", a.ContentMode)
		}
		if err != nil {
			return readOutput{}, err
		}
		content, truncated := truncateRunes(content, a.MaxChars)
		return readOutput{URI: a.URI, ContentMode: mode, Content: content, Truncated: truncated}, nil
	}
	return function.NewFunctionTool(fn,
		function.WithName(string(ToolRead)),
		function.WithDescription("Read content at an OpenViking URI. content_mode picks the level: "+
			"read (default, L2 full content of any file/leaf) | overview (L1) | abstract (L0). "+
			"overview/abstract resolve the summary OF A DIRECTORY, so pass a directory URI "+
			"(isDir=true from viking_browse) and never append a filename to it. "+
			"viking_search/viking_find hits frequently point at .abstract.md/.overview.md files: "+
			"those files ARE the summary, so read them with read (the default), not overview/abstract. "+
			"Use read with offset/limit to page through long files."))
}

// ===== viking_grep =====

type grepArgs struct {
	URI             string `json:"uri" jsonschema:"description=viking:// URI to search within"`
	Pattern         string `json:"pattern" jsonschema:"description=Pattern to match in node content"`
	CaseInsensitive bool   `json:"case_insensitive,omitempty" jsonschema:"description=Case-insensitive match"`
	NodeLimit       int    `json:"node_limit,omitempty" jsonschema:"description=Maximum number of nodes to scan"`
}

func newGrepTool(c *client.Client) tool.Tool {
	fn := func(ctx context.Context, a grepArgs) (string, error) {
		raw, err := c.Grep(ctx, a.URI, a.Pattern, a.CaseInsensitive, a.NodeLimit)
		if err != nil {
			return "", err
		}
		return string(raw), nil
	}
	return function.NewFunctionTool(fn,
		function.WithName(string(ToolGrep)),
		function.WithDescription("Grep-style content search within an OpenViking URI subtree."))
}

// ===== viking_store =====

type storeArgs struct {
	Content   string `json:"content" jsonschema:"description=Text content of the message to store"`
	Role      string `json:"role,omitempty" jsonschema:"description=Message role: user or assistant (default user)"`
	SessionID string `json:"session_id,omitempty" jsonschema:"description=Existing session id; a new session is created when empty"`
	Commit    bool   `json:"commit,omitempty" jsonschema:"description=Commit the session after storing to trigger memory extraction"`
}

type storeOutput struct {
	SessionID string `json:"session_id"`
	Committed bool   `json:"committed"`
}

func newStoreTool(c *client.Client) tool.Tool {
	fn := func(ctx context.Context, a storeArgs) (storeOutput, error) {
		sessionID := a.SessionID
		if sessionID == "" {
			raw, err := c.CreateSession(ctx, "")
			if err != nil {
				return storeOutput{}, err
			}
			sessionID = extractSessionID(raw)
			if sessionID == "" {
				return storeOutput{}, fmt.Errorf("openviking: could not determine session_id from create response")
			}
		}
		role := a.Role
		if role == "" {
			role = "user"
		}
		if role != "user" && role != "assistant" {
			return storeOutput{}, fmt.Errorf("openviking: invalid role %q: must be user or assistant", a.Role)
		}
		if _, err := c.AddMessage(ctx, sessionID, role, a.Content); err != nil {
			return storeOutput{}, err
		}
		if a.Commit {
			if _, err := c.CommitSession(ctx, sessionID); err != nil {
				return storeOutput{}, err
			}
		}
		return storeOutput{SessionID: sessionID, Committed: a.Commit}, nil
	}
	return function.NewFunctionTool(fn,
		function.WithName(string(ToolStore)),
		function.WithDescription("Store a message into an OpenViking session, optionally committing to trigger memory extraction."))
}

// ===== viking_add_resource =====

type addResourceArgs struct {
	Path   string `json:"path" jsonschema:"description=A URL or remote path/repository to import into OpenViking resources"`
	To     string `json:"to,omitempty" jsonschema:"description=Optional destination viking:// URI"`
	Parent string `json:"parent,omitempty" jsonschema:"description=Optional parent viking:// URI"`
	Wait   bool   `json:"wait,omitempty" jsonschema:"description=Wait for semantic processing to finish before returning"`
}

func newAddResourceTool(c *client.Client) tool.Tool {
	fn := func(ctx context.Context, a addResourceArgs) (string, error) {
		raw, err := c.AddResource(ctx, a.Path, a.To, a.Parent, a.Wait)
		if err != nil {
			return "", err
		}
		return string(raw), nil
	}
	return function.NewFunctionTool(fn,
		function.WithName(string(ToolAddResource)),
		function.WithDescription("Import a URL, remote path, or repository into OpenViking resources for later retrieval. "+
			"For large imports leave wait=false (the default) to avoid the per-request timeout; the server keeps "+
			"processing in the background after the call returns."))
}

// ===== viking_add_skill =====

type addSkillArgs struct {
	Data string `json:"data" jsonschema:"description=Skill definition (text or a path/URL accepted by OpenViking)"`
	Wait bool   `json:"wait,omitempty" jsonschema:"description=Wait for processing to finish before returning"`
}

func newAddSkillTool(c *client.Client) tool.Tool {
	fn := func(ctx context.Context, a addSkillArgs) (string, error) {
		raw, err := c.AddSkill(ctx, a.Data, a.Wait)
		if err != nil {
			return "", err
		}
		return string(raw), nil
	}
	return function.NewFunctionTool(fn,
		function.WithName(string(ToolAddSkill)),
		function.WithDescription("Register a reusable skill in OpenViking."))
}

// ===== viking_health =====

type healthArgs struct{}

func newHealthTool(c *client.Client) tool.Tool {
	fn := func(ctx context.Context, _ healthArgs) (string, error) {
		raw, err := c.Status(ctx)
		if err != nil {
			return "", err
		}
		return string(raw), nil
	}
	return function.NewFunctionTool(fn,
		function.WithName(string(ToolHealth)),
		function.WithDescription("Check OpenViking server status."))
}

// ===== viking_forget =====

type forgetArgs struct {
	URI       string `json:"uri" jsonschema:"description=viking:// URI to remove"`
	Recursive bool   `json:"recursive,omitempty" jsonschema:"description=Remove recursively"`
}

func newForgetTool(c *client.Client) tool.Tool {
	fn := func(ctx context.Context, a forgetArgs) (string, error) {
		raw, err := c.Remove(ctx, a.URI, a.Recursive)
		if err != nil {
			return "", err
		}
		return string(raw), nil
	}
	return function.NewFunctionTool(fn,
		function.WithName(string(ToolForget)),
		function.WithDescription("Remove a URI from OpenViking. Destructive; only available with the admin profile."))
}

// extractSessionID pulls the session_id field from a CreateSession result.
func extractSessionID(raw []byte) string {
	var parsed struct {
		SessionID string `json:"session_id"`
	}
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return ""
	}
	return parsed.SessionID
}
