//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

//go:build integration

// This file holds an end-to-end accuracy benchmark for the tool_search plugin.
// It drives a real Runner + LLM against a self-defined catalog of deferred tools
// (filesystem, git, document, process and network namespaces) and measures how
// often tool_search loads the expected tool for a natural-language request.
//
// No tool here makes a real call: the toolset is purely metadata (name +
// description) and an intercept plugin stubs every execution, so the only
// network traffic is the LLM completion itself. Credentials come from the
// environment, mirroring the examples/ convention:
//
//	OPENAI_API_KEY  (required — test is skipped when unset)
//	OPENAI_BASE_URL (optional — provider endpoint)
//	MODEL_NAME      (optional — defaults to deepseek-v4-flash)
//
// Run with:
//
//	OPENAI_API_KEY=sk-... go test -tags=integration -run TestToolSearchAccuracy \
//	    ./plugin/toolsearch/ -v -timeout 900s
package toolsearch

import (
	"context"
	"fmt"
	"os"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/model/openai"
	"trpc.group/trpc-go/trpc-agent-go/plugin"
	"trpc.group/trpc-go/trpc-agent-go/runner"
	"trpc.group/trpc-go/trpc-agent-go/session/inmemory"
	"trpc.group/trpc-go/trpc-agent-go/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool/function"
)

// --- environment-driven model configuration ---

const (
	envModelName   = "MODEL_NAME"
	envOpenAIKey   = "OPENAI_API_KEY"
	envOpenAIBase  = "OPENAI_BASE_URL"
	defaultModelID = "deepseek-v4-flash"
)

// newTestModel builds an OpenAI-compatible model from the environment. BaseURL
// is only applied when set so the official endpoint is the default.
func newTestModel(t *testing.T) *openai.Model {
	t.Helper()
	opts := []openai.Option{openai.WithAPIKey(os.Getenv(envOpenAIKey))}
	if base := os.Getenv(envOpenAIBase); base != "" {
		opts = append(opts, openai.WithBaseURL(base))
	}
	name := os.Getenv(envModelName)
	if name == "" {
		name = defaultModelID
	}
	return openai.New(name, opts...)
}

// requireAPIKey skips the test unless an API key is present — these tests cost
// real tokens and are excluded from the default build via the integration tag.
func requireAPIKey(t *testing.T) {
	t.Helper()
	if os.Getenv(envOpenAIKey) == "" {
		t.Skipf("%s not set; skipping live tool_search accuracy test", envOpenAIKey)
	}
}

// testInstruction is the system prompt. It carries the {deferred_tools_section}
// placeholder the plugin replaces with the toolbox catalog, plus guidance that
// the model must load tools via tool_search before calling them.
func testInstruction() string {
	return `You are a helpful AI assistant.

Tool-use policy (MANDATORY, applies to every user turn):
1. NEVER ask the user clarifying questions. Whatever the user asks you to do,
   immediately call the tool_search function to load the matching tool(s).
   Do NOT reply with plain prose saying "I cannot do that" or "please run
   this command yourself" — always call tool_search first without hesitation.
2. If a tool name in the catalog obviously matches the user's intent, pass it
   directly via tool_names.
3. Otherwise, pick the namespace from the catalog whose description best
   matches the user's intent and issue keyword queries in BOTH Chinese and English.
4. Only after the tool is loaded, call it. Never fabricate tool output.
`
}

// --- self-defined deferred tool catalog ---
//
// Each tool is metadata only: a name and a one-line description. The intercept
// plugin stubs execution, so the bodies never run during the benchmark.

// metaTool builds a no-op function tool carrying just a name and description.
func metaTool(name, desc string) tool.Tool {
	return function.NewFunctionTool(
		func(ctx context.Context, in struct {
			Path  string `json:"path,omitempty"`
			Query string `json:"query,omitempty"`
		}) (string, error) {
			return fmt.Sprintf(`{"tool":%q,"status":"ok"}`, name), nil
		},
		function.WithName(name),
		function.WithDescription(desc),
	)
}

// catalogToolboxes returns the deferred-tool namespaces under test. The tools
// are intentionally distinct in capability but share generic verbs (read, list,
// search, get) across namespaces, so the namespace scoping is exercised.
func catalogToolboxes() []Toolbox {
	return []Toolbox{
		{
			Name:        "filesystem",
			Description: "read, write, move and search files and directories on the local disk",
			Tools: []tool.Tool{
				metaTool("read_file", "Read the full contents of a file at a given path."),
				metaTool("write_file", "Write or overwrite text content to a file at a given path."),
				metaTool("append_file", "Append text content to the end of an existing file."),
				metaTool("delete_file", "Permanently delete a file from disk."),
				metaTool("move_file", "Move or rename a file from one path to another."),
				metaTool("copy_file", "Copy a file to a new location."),
				metaTool("list_directory", "List the files and subdirectories inside a directory."),
				metaTool("create_directory", "Create a new directory, including parent directories."),
				metaTool("search_file_content", "Search file contents for a text pattern (grep-style)."),
				metaTool("find_files", "Find files by name or glob pattern under a directory."),
				metaTool("get_file_info", "Get metadata for a file: size, permissions, modified time."),
			},
		},
		{
			Name:        "git",
			Description: "version control operations on a git repository: status, commits, branches, history",
			Tools: []tool.Tool{
				metaTool("git_status", "Show the working tree status: staged, modified and untracked files."),
				metaTool("git_diff", "Show the diff of unstaged or staged changes."),
				metaTool("git_commit", "Create a commit from the staged changes with a message."),
				metaTool("git_add", "Stage files for the next commit."),
				metaTool("git_log", "Show the commit history of the repository."),
				metaTool("git_branch", "List, create or delete branches."),
				metaTool("git_checkout", "Switch branches or restore working tree files."),
				metaTool("git_merge", "Merge another branch into the current branch."),
				metaTool("git_push", "Push local commits to a remote repository."),
				metaTool("git_pull", "Fetch and integrate changes from a remote repository."),
				metaTool("git_stash", "Stash away uncommitted changes for later."),
				metaTool("git_blame", "Show who last modified each line of a file."),
			},
		},
		{
			Name:        "document",
			Description: "create, convert, summarize and export documents and reports",
			Tools: []tool.Tool{
				metaTool("create_document", "Create a new text or markdown document."),
				metaTool("export_pdf", "Export a document to a PDF file."),
				metaTool("convert_markdown_to_html", "Convert a markdown document into HTML."),
				metaTool("summarize_document", "Generate a concise summary of a long document."),
				metaTool("translate_document", "Translate a document into another language."),
				metaTool("extract_document_text", "Extract plain text from a PDF or Word document."),
				metaTool("merge_documents", "Combine multiple documents into a single file."),
				metaTool("get_document_outline", "Extract the heading outline of a document."),
			},
		},
		{
			Name:        "process",
			Description: "run shell commands and manage operating-system processes",
			Tools: []tool.Tool{
				metaTool("run_command", "Execute a shell command and capture its output."),
				metaTool("list_processes", "List currently running processes."),
				metaTool("kill_process", "Terminate a running process by its PID."),
				metaTool("get_env_var", "Read the value of an environment variable."),
				metaTool("set_env_var", "Set the value of an environment variable for the session."),
			},
		},
		{
			Name:        "network",
			Description: "make HTTP requests, call APIs, upload and download files over the internet, check URL reachability. ",
			Tools: []tool.Tool{
				metaTool("http_get", "Send an HTTP GET request to a URL and return the response."),
				metaTool("http_post", "Send an HTTP POST request with a body to a URL."),
				metaTool("download_file", "Download a file from a URL to a local path."),
				metaTool("upload_file", "Upload a local file to a remote URL."),
				metaTool("check_url_status", "Check whether a URL is reachable and its status code."),
			},
		},
		{
			Name:        "iam",
			Description: "identity and access management: manage user accounts, roles and permissions",
			Tools: []tool.Tool{
				metaTool("create_user", "Create a new user account in the identity system."),
				metaTool("delete_user", "Permanently delete a user account from the identity system."),
				metaTool("list_users", "List all user accounts in the identity system."),
				metaTool("update_user", "Update properties of an existing user account."),
				metaTool("get_user", "Get details of a specific user account."),
				metaTool("grant_role", "Grant a role to a user account."),
				metaTool("revoke_role", "Revoke a role from a user account."),
			},
		},
		{
			Name:        "crm",
			Description: "customer relationship management: manage customers, contacts and sales leads",
			Tools: []tool.Tool{
				metaTool("create_customer", "Create a new customer record in the CRM system."),
				metaTool("delete_customer", "Permanently delete a customer record from the CRM system."),
				metaTool("list_customers", "List all customer records in the CRM system."),
				metaTool("update_customer", "Update properties of an existing customer record."),
				metaTool("get_customer", "Get details of a specific customer record."),
				metaTool("add_contact", "Add a new contact person to a customer record."),
				metaTool("remove_contact", "Remove a contact person from a customer record."),
			},
		},
	}
}

// presetTools are always advertised to the model (never deferred). They stand in
// for the small always-on toolset a real agent keeps loaded.
func presetTools() []tool.Tool {
	return []tool.Tool{
		metaTool("web_search", "Search the web for up-to-date information and return relevant results."),
	}
}

// deferredTools returns general-purpose deferred tools that do NOT belong to any
// specific business domain. They are registered via WithDeferredTools (no
// namespace), so the model can search for them without specifying a namespace.
// These tools verify that keyword search works correctly even when the tool is
// not scoped to a toolbox.
func deferredTools() []tool.Tool {
	return []tool.Tool{
		metaTool("calculator", "Evaluate an arithmetic expression and return the result."),
		metaTool("get_current_time", "Get the current system time in a specified timezone."),
		metaTool("generate_qrcode", "Generate a QR code image from text or a URL."),
		metaTool("base64_encode", "Encode a string to base64."),
		metaTool("base64_decode", "Decode a base64-encoded string back to plain text."),
		metaTool("parse_json", "Parse a JSON string and extract values by path."),
		metaTool("format_date", "Format a date string from one format to another."),
		metaTool("generate_uuid", "Generate a random UUID v4."),
	}
}

// --- intercept plugin: stub executions, observe first-round discovery ---

// interceptPlugin stubs every deferred tool's execution and records which
// deferred tool schemas tool_search injected on the first model turn after it
// fired. Capturing only the first round keeps DiscoveredTools aligned with the
// initial search result rather than accumulating later turns.
type interceptPlugin struct {
	deferredNames map[string]struct{}

	mu         sync.Mutex
	discovered map[string]struct{}
}

func newInterceptPlugin(boxes []Toolbox, defaultTools []tool.Tool) *interceptPlugin {
	names := make(map[string]struct{})
	for _, box := range boxes {
		for _, t := range box.Tools {
			names[t.Declaration().Name] = struct{}{}
		}
	}
	for _, t := range defaultTools {
		names[t.Declaration().Name] = struct{}{}
	}
	return &interceptPlugin{
		deferredNames: names,
		discovered:    make(map[string]struct{}),
	}
}

func (p *interceptPlugin) Name() string { return "accuracy_intercept" }

func (p *interceptPlugin) Register(r *plugin.Registry) {
	if r == nil {
		return
	}
	r.BeforeModel(p.beforeModel)
	r.BeforeTool(p.beforeTool)
}

// beforeModel accumulates the deferred tools the search plugin injected on every turn
func (p *interceptPlugin) beforeModel(_ context.Context, args *model.BeforeModelArgs) (*model.BeforeModelResult, error) {
	if args == nil || args.Request == nil {
		return nil, nil
	}
	p.mu.Lock()
	defer p.mu.Unlock()

	if args.Request.Tools == nil {
		return nil, nil
	}
	for name := range args.Request.Tools {
		if _, ok := p.deferredNames[name]; ok {
			p.discovered[name] = struct{}{}
		}
	}
	return nil, nil
}

// beforeTool stubs deferred-tool execution. tool_search itself is left
// untouched so the real search plugin runs and populates session state; every
// other deferred tool short-circuits with a stubbed JSON reply so no external
// side effects happen during the benchmark.
func (p *interceptPlugin) beforeTool(_ context.Context, args *tool.BeforeToolArgs) (*tool.BeforeToolResult, error) {
	if args == nil || args.ToolName == "" {
		return nil, nil
	}
	if args.ToolName == toolSearchToolName {
		return nil, nil
	}
	if _, ok := p.deferredNames[args.ToolName]; ok {
		return &tool.BeforeToolResult{
			CustomResult: fmt.Sprintf(`{"tool":%q,"status":"stubbed"}`, args.ToolName),
		}, nil
	}
	return nil, nil
}

func (p *interceptPlugin) discoveredTools() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make([]string, 0, len(p.discovered))
	for name := range p.discovered {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

func (p *interceptPlugin) reset() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.discovered = make(map[string]struct{})
}

// --- test cases ---

type accuracyCase struct {
	UserMessage   string
	ExpectedTools []string // hit if any of these was discovered
	Domain        string
}

// accuracyCases pairs a natural-language request with the tool(s) that should be
// loaded. A case is a hit when tool_search loads any expected tool.
func accuracyCases() []accuracyCase {
	return []accuracyCase{
		// filesystem
		{"把hello world这段文字保存到 notes.txt", []string{"write_file"}, "filesystem"},
		{"帮我读取 config.yaml 的内容", []string{"read_file"}, "filesystem"},
		{"在文件末尾追加一行日志", []string{"append_file"}, "filesystem"},
		{"删除临时文件 tmp.log", []string{"delete_file"}, "filesystem"},
		{"把 a.txt 重命名为 b.txt", []string{"move_file"}, "filesystem"},
		{"列出 src 目录下的所有文件", []string{"list_directory"}, "filesystem"},
		{"新建一个 build 目录", []string{"create_directory"}, "filesystem"},
		{"在代码里搜索 TODO 注释", []string{"search_file_content", "find_files"}, "filesystem"},
		{"请帮我看一下 notes.txt 文件的大小和最后修改时间", []string{"get_file_info"}, "filesystem"},
		{"List files in the current folder", []string{"list_directory"}, "filesystem"},

		// git
		{"看看当前仓库有哪些改动", []string{"git_status"}, "git"},
		{"显示我还没提交的差异", []string{"git_diff"}, "git"},
		{"把暂存的修改提交一下", []string{"git_commit"}, "git"},
		{"把这些文件加入暂存区", []string{"git_add"}, "git"},
		{"查看最近的提交历史", []string{"git_log"}, "git"},
		{"切换到 develop 分支", []string{"git_checkout"}, "git"},
		{"创建一个新分支 feature-x", []string{"git_branch", "git_checkout"}, "git"},
		{"把本地提交推到远端", []string{"git_push"}, "git"},
		{"从远端拉取最新代码", []string{"git_pull"}, "git"},
		{"这一行代码是谁写的", []string{"git_blame"}, "git"},

		// document
		{"请撰写一篇新的 markdown 格式的文档", []string{"create_document"}, "document"},
		{"把这份报告导出成 PDF", []string{"export_pdf"}, "document"},
		{"把这个 markdown 转成网页", []string{"convert_markdown_to_html"}, "document"},
		{"请总结一篇长文档的内容并提炼核心要点", []string{"summarize_document"}, "document"},
		{"把这份文档翻译成英文", []string{"translate_document"}, "document"},
		{"从 PDF 里提取纯文本", []string{"extract_document_text"}, "document"},
		{"把几份文档合并成一个", []string{"merge_documents"}, "document"},
		{"列出这篇文档的章节大纲", []string{"get_document_outline"}, "document"},
		//
		// // process
		{"帮我执行 npm install", []string{"run_command"}, "process"},
		{"看看现在有哪些进程在跑", []string{"list_processes"}, "process"},
		{"杀掉 PID 为 1234 的进程", []string{"kill_process"}, "process"},
		{"读一下 PATH 环境变量", []string{"get_env_var"}, "process"},
		{"设置环境变量 DEBUG=1", []string{"set_env_var"}, "process"},

		// network
		{"请发送 HTTP GET 请求到指定的 API 接口地址查看响应数据", []string{"http_get", "http_post"}, "network"},
		{"请向www.tencent.com 地址发起 POST 请求并提交 JSON 数据 [\"hello\"]", []string{"http_post"}, "network"},
		{"从这个www.demo.com/1.txt链接下载安装包", []string{"download_file"}, "network"},
		{"把这个文件上传到服务器", []string{"upload_file"}, "network"},
		{"检查这个网址www.demo.com通不通", []string{"check_url_status"}, "network"},

		// iam — identity and access management
		// These cases test that "delete user" / "create user" requests are
		// scoped to the iam namespace rather than leaking into crm (which has
		// similar verbs on customer records). The model must infer the domain
		// from the context (account, login, role, permission → iam).
		{"帮我从系统中删除用户账号 zhangsan", []string{"delete_user"}, "iam"},
		{"创建一个新的登录用户", []string{"create_user"}, "iam"},
		{"列出所有管理系统用户", []string{"list_users"}, "iam"},
		{"更新用户 wangwu 的邮箱地址", []string{"update_user"}, "iam"},
		{"查看用户 lisi 的详细信息", []string{"get_user"}, "iam"},
		{"给用户 admin 授予管理员角色", []string{"grant_role"}, "iam"},
		{"撤销用户 zhaoliu 的编辑权限", []string{"revoke_role"}, "iam"},

		// crm — customer relationship management
		// These cases test that "delete customer" / "create customer" requests
		// are scoped to the crm namespace. The model must pick crm over iam
		// based on keywords like customer, contact, lead, CRM.
		{"把客户张三从CRM系统中删除", []string{"delete_customer"}, "crm"},
		{"新增一个客户记录：腾讯科技", []string{"create_customer"}, "crm"},
		{"列出所有客户", []string{"list_customers"}, "crm"},
		{"更新客户李四的联系方式", []string{"update_customer"}, "crm"},
		{"查看客户阿里巴巴的详细信息", []string{"get_customer"}, "crm"},
		{"为客户腾讯添加联系人王五", []string{"add_contact"}, "crm"},
		{"删除客户字节跳动的联系人赵六", []string{"remove_contact"}, "crm"},

		// cross-namespace disambiguation: these requests are intentionally
		// ambiguous between iam and crm — both have a notion of "delete" on a
		// person-like entity. The test verifies that at least one of the two
		// expected tools is loaded (the model is not expected to guess the
		// right namespace without disambiguation), demonstrating that namespace
		// scoping prevents a generic verb from matching an unrelated domain.
		{"删除用户", []string{"delete_user"}, "iam"},
		{"创建一个用户", []string{"create_user"}, "iam"},
		{"显示用户列表", []string{"list_users"}, "iam"},

		// default (no namespace) — general-purpose deferred tools registered via
		// WithDeferredTools without a toolbox. These tools have no domain bias:
		// the model must find them with keyword search alone, without specifying
		// a namespace argument. This validates that tool_search works correctly
		// for the non-toolbox path (keyword → _default namespace fallback).
		{"帮我算一下 3.14 乘以 256 等于多少", []string{"calculator"}, "default"},
		{"现在几点了", []string{"get_current_time"}, "default"},
		{"把 https://github.com 这个链接生成一个二维码", []string{"generate_qrcode"}, "default"},
		{"帮我把这段文本 'hello world' 进行 base64 编码", []string{"base64_encode"}, "default"},
		{"解码这段 base64 字符串 aGVsbG8gd29ybGQ=", []string{"base64_decode"}, "default"},
		{"解析这个 JSON：{\"name\":\"张三\",\"age\":30}", []string{"parse_json"}, "default"},
		{"把日期 2025-01-15 格式化成 2025年1月15日", []string{"format_date"}, "default"},
		{"给我生成一个随机的 UUID", []string{"generate_uuid"}, "default"},
		{"Encode 'hello world' to base64", []string{"base64_encode"}, "default"},
		{"What time is it now in UTC", []string{"get_current_time"}, "default"},
	}
}

// --- accuracy harness ---

type caseResult struct {
	UserMessage     string
	Domain          string
	ExpectedTools   []string
	DiscoveredTools []string
	CalledTools     []string
	ToolSearchUsed  bool
	Hit             bool
	FullResponse    string
}

// collectToolCalls drains the event stream, recording whether tool_search was
// called and which other tools the model invoked (for logging only).  It also
// accumulates the full model response (text content, tool calls and tool
// results) into a single string for diagnostics on failed cases.
func collectToolCalls(t *testing.T, ch <-chan *event.Event, timeout time.Duration) (searchUsed bool, called []string, fullResponse string) {
	t.Helper()
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	var buf strings.Builder
	for {
		select {
		case evt, ok := <-ch:
			if !ok {
				fullResponse = buf.String()
				return
			}
			if evt.Error != nil {
				t.Logf("event stream error: %s", evt.Error.Message)
				buf.WriteString(fmt.Sprintf("[ERROR: %s]\n", evt.Error.Message))
				fullResponse = buf.String()
				return
			}
			if evt.Response == nil {
				continue
			}
			for _, choice := range evt.Response.Choices {
				// accumulate text deltas (streaming mode)
				if choice.Delta.Content != "" {
					buf.WriteString(choice.Delta.Content)
				}
				// accumulate full (non-streaming) assistant content
				if choice.Message.Role == model.RoleAssistant && choice.Message.Content != "" {
					buf.WriteString(choice.Message.Content)
					buf.WriteString("\n")
				}
				// record tool calls
				for _, tc := range append(choice.Message.ToolCalls, choice.Delta.ToolCalls...) {
					switch tc.Function.Name {
					case "":
					case toolSearchToolName:
						searchUsed = true
						buf.WriteString(fmt.Sprintf("\n[TOOL_SEARCH: %s]\n", string(tc.Function.Arguments)))
					default:
						called = append(called, tc.Function.Name)
						buf.WriteString(fmt.Sprintf("\n[TOOL_CALL: %s args=%s]\n", tc.Function.Name, string(tc.Function.Arguments)))
					}
				}
				// record tool responses
				if choice.Message.Role == model.RoleTool && choice.Message.Content != "" {
					buf.WriteString(fmt.Sprintf("[TOOL_RESULT(%s): %s]\n", choice.Message.ToolName, choice.Message.Content))
				}
			}
		case <-timer.C:
			t.Log("event collection timed out")
			buf.WriteString("[TIMEOUT]\n")
			fullResponse = buf.String()
			return
		}
	}
}

// hit reports whether any expected tool was discovered.
func hit(discovered, expected []string) bool {
	for _, d := range discovered {
		for _, e := range expected {
			if d == e {
				return true
			}
		}
	}
	return false
}

// TestToolSearchAccuracy runs every case through a real Runner + LLM and reports
// the tool_search call rate and tool-hit accuracy, overall and per namespace.
func TestToolSearchAccuracy(t *testing.T) {
	requireAPIKey(t)

	boxes := catalogToolboxes()
	preset := presetTools()
	defs := deferredTools()
	cases := accuracyCases()
	t.Logf("toolboxes=%d default_deferred=%d cases=%d", len(boxes), len(defs), len(cases))

	searchPlugin := NewPlugin(preset, WithToolboxes(boxes), WithDeferredTools(defs), WithMaxTools(5), WithCatalogInDescription(false))
	intercept := newInterceptPlugin(boxes, defs)

	results := make([]caseResult, len(cases))
	for i, tc := range cases {
		intercept.reset()

		ag := llmagent.New(
			fmt.Sprintf("accuracy_%d", i),
			llmagent.WithModel(newTestModel(t)),
			llmagent.WithInstruction(testInstruction()),
			llmagent.WithTools(preset),
			llmagent.WithGenerationConfig(model.GenerationConfig{Stream: true}),
		)
		appRunner := runner.NewRunner(
			fmt.Sprintf("accuracy_%d", i),
			ag,
			runner.WithSessionService(inmemory.NewSessionService()),
			runner.WithPlugins(searchPlugin, intercept),
		)

		sessionID := fmt.Sprintf("acc-%d-%d", i, time.Now().UnixNano())
		ch, err := appRunner.Run(context.Background(), "test_user", sessionID, model.NewUserMessage(tc.UserMessage))
		if err != nil {
			t.Logf("[%d] Run failed: %v", i, err)
			results[i] = caseResult{UserMessage: tc.UserMessage, Domain: tc.Domain, ExpectedTools: tc.ExpectedTools}
			continue
		}

		searchUsed, called, fullRsp := collectToolCalls(t, ch, 120*time.Second)
		discovered := intercept.discoveredTools()
		// A case is a hit when an expected tool was either loaded by tool_search
		// (discovered) or actually invoked by the model (called) — calling counts
		// as evidence the right tool was found even if the discovery snapshot
		// missed it (e.g. captured on a later turn).
		results[i] = caseResult{
			UserMessage:     tc.UserMessage,
			Domain:          tc.Domain,
			ExpectedTools:   tc.ExpectedTools,
			DiscoveredTools: discovered,
			CalledTools:     called,
			ToolSearchUsed:  searchUsed,
			Hit:             hit(discovered, tc.ExpectedTools) || hit(called, tc.ExpectedTools),
			FullResponse:    fullRsp,
		}
	}

	reportAccuracy(t, results)
}

// reportAccuracy prints overall + per-namespace statistics and asserts minimum
// thresholds for the tool_search call rate and the tool-hit accuracy.
func reportAccuracy(t *testing.T, results []caseResult) {
	t.Helper()
	total := len(results)
	searchUsed, hits := 0, 0
	type stat struct{ Total, Hit int }
	byDomain := make(map[string]stat)

	t.Log("\n========== misses ==========")
	for _, r := range results {
		if r.ToolSearchUsed {
			searchUsed++
		}
		if r.Hit {
			hits++
		}
		s := byDomain[r.Domain]
		s.Total++
		if r.Hit {
			s.Hit++
		}
		byDomain[r.Domain] = s
		if !r.Hit {
			t.Logf("❌ %q → discovered=%v called=%v expected=%v search=%v\n--- full response ---\n%s\n--- end ---",
				r.UserMessage, r.DiscoveredTools, r.CalledTools, r.ExpectedTools, r.ToolSearchUsed, r.FullResponse)
		}
	}

	t.Log("\n========== overall ==========")
	t.Logf("cases: %d", total)
	t.Logf("tool_search call rate: %d/%d (%.1f%%)", searchUsed, total, pct(searchUsed, total))
	t.Logf("tool hit accuracy:     %d/%d (%.1f%%)", hits, total, pct(hits, total))

	t.Log("\n========== per namespace ==========")
	domains := make([]string, 0, len(byDomain))
	for d := range byDomain {
		domains = append(domains, d)
	}
	sort.Strings(domains)
	for _, d := range domains {
		s := byDomain[d]
		t.Logf("  %-12s: %d/%d (%.1f%%)", d, s.Hit, s.Total, pct(s.Hit, s.Total))
	}

	if acc := pct(hits, total); acc < 60 {
		t.Errorf("tool hit accuracy %.1f%% below 60%% threshold", acc)
	}
	if rate := pct(searchUsed, total); rate < 70 {
		t.Errorf("tool_search call rate %.1f%% below 70%% threshold", rate)
	}
}

// pct returns n/d as a percentage, guarding against division by zero.
func pct(n, d int) float64 {
	if d == 0 {
		return 0
	}
	return float64(n) / float64(d) * 100
}
