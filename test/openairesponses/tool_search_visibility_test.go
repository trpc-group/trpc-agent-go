//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent. All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package openairesponses_test

import (
	"bytes"
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
	"trpc.group/trpc-go/trpc-agent-go/model"
	responses "trpc.group/trpc-go/trpc-agent-go/model/openai/sdkv3/responses"
	"trpc.group/trpc-go/trpc-agent-go/runner"
	"trpc.group/trpc-go/trpc-agent-go/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool/deferred"
)

func TestRunnerToolSearchPreservesExposedWeatherIdentity(t *testing.T) {
	const call = `[{"type":"function_call","id":"fc_weather","status":"completed","call_id":"weather_1","namespace":"weather","name":"weather","arguments":"{\"id\":\"city\"}"}]`
	for _, tc := range []struct {
		name  string
		setup string
		want  string
		wire  []string
	}{
		{"deferred top level and active namespace", "deferred_top", "eager_member", []string{"namespace:weather[weather]", "tool_search:client"}},
		{"other original-name namespace and active namespace", "deferred_crm", "eager_member", []string{"namespace:weather[weather]", "tool_search:client"}},
		{"eager standalone and flattened member", "flat", "eager_standalone", []string{"function:weather", "function:weather_weather", "tool_search:client"}},
	} {
		for _, stream := range []bool{false, true} {
			for _, edit := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s/stream=%v/edit=%v", tc.name, stream, edit), func(t *testing.T) {
					server, srv := newSearchServer(t, call, messageOutput("msg_done", "done"))
					executions := &searchExecutions{}
					searchCalls := 0
					handler := func(context.Context, *responses.ToolSearchRequest) ([]responses.ToolSearchEntry, error) {
						searchCalls++
						return nil, nil
					}
					eagerMember := executions.tool("weather", "Eager weather member", "eager_member")
					opts := []llmagent.Option{
						llmagent.WithModel(newSearchModel(srv, responses.WithClientToolSearch(handler))),
						llmagent.WithGenerationConfig(model.GenerationConfig{Stream: stream}),
					}
					switch tc.setup {
					case "deferred_top":
						set := deferred.ToolSet(searchToolSet{name: "weather", tools: []tool.Tool{eagerMember,
							executions.tool("forecast", "Deferred forecast", "forecast")}},
							deferred.WithFilter(tool.NewIncludeToolNamesFilter("forecast")))
						opts = append(opts, llmagent.WithTools([]tool.Tool{deferred.Tool(executions.tool("weather", "Deferred top level", "deferred_top"))}), llmagent.WithToolSets([]tool.ToolSet{set}))
					case "deferred_crm":
						weather := deferred.ToolSet(searchToolSet{name: "weather", tools: []tool.Tool{eagerMember,
							executions.tool("forecast", "Deferred forecast", "forecast")}},
							deferred.WithFilter(tool.NewIncludeToolNamesFilter("forecast")))
						crm := deferred.ToolSet(searchToolSet{name: "crm", tools: []tool.Tool{executions.tool("weather", "Deferred CRM weather", "crm")}})
						opts = append(opts, llmagent.WithToolSets([]tool.ToolSet{crm, weather}), llmagent.WithToolSetToolNameMode("crm", tool.ToolSetToolNameModeOriginal))
					case "flat":
						opts = append(opts, llmagent.WithTools([]tool.Tool{executions.tool("weather", "Eager top level", "eager_standalone")}),
							llmagent.WithToolSets([]tool.ToolSet{searchToolSet{name: "weather", tools: []tool.Tool{eagerMember}}}))
					}
					edits := 0
					var before []string
					if edit {
						callbacks := model.NewCallbacks().RegisterAfterModel(func(_ context.Context, args *model.AfterModelArgs) (*model.AfterModelResult, error) {
							if args.Response == nil || args.Response.IsPartial || len(args.Response.Choices) == 0 {
								return nil, nil
							}
							msg := &args.Response.Choices[0].Message
							if msg.ProviderData == nil || !bytes.Contains(msg.ProviderData.Payload, []byte("fc_weather")) {
								return nil, nil
							}
							for _, call := range msg.ToolCalls {
								before = append(before, call.Function.Name)
							}
							msg.ProviderData.Payload = bytes.ReplaceAll(msg.ProviderData.Payload, []byte("city"), []byte("town"))
							edits++
							return nil, nil
						})
						opts = append(opts, llmagent.WithModelCallbacks(callbacks))
					}
					r := runner.NewRunner("search", llmagent.New("search", opts...))
					t.Cleanup(func() { _ = r.Close() })
					events, failures := runSearch(t, r, model.NewUserMessage("call the exposed weather tool"))
					require.Empty(t, failures)
					require.Equal(t, "done", finalText(events))
					require.Zero(t, searchCalls, "no deferred tool was selected")
					require.Equal(t, []string{tc.want}, executions.executed())
					requests := server.requests()
					require.Len(t, requests, 2)
					require.Equal(t, tc.wire, toolSummary(t, requests[0]["tools"]))
					wantArg := "city"
					if edit {
						wantArg = "town"
						require.Equal(t, 1, edits)
						wantName := "weather_weather"
						if tc.setup == "flat" {
							wantName = "weather"
						}
						require.Equal(t, []string{wantName}, before)
					}
					require.Contains(t, string(requests[1]["input"]), tc.want+":"+wantArg)
				})
			}
		}
	}
}

// A fully deferred ToolSet has no namespace in the request. A searched
// standalone function of the same name must therefore keep its self-namespace.
func TestRunnerToolSearchStandaloneBeforeFullyDeferredNamespace(t *testing.T) {
	const call = `[{"type":"function_call","id":"fc_weather","status":"completed","call_id":"weather_1","namespace":"weather","name":"weather","arguments":"{\"id\":\"city\"}"}]`
	for _, stream := range []bool{false, true} {
		for _, edit := range []bool{false, true} {
			t.Run(fmt.Sprintf("stream=%v/edit=%v", stream, edit), func(t *testing.T) {
				server, srv := newSearchServer(t, clientSearchRound, call, messageOutput("msg_done", "done"))
				executions := &searchExecutions{}
				handler := func(context.Context, *responses.ToolSearchRequest) ([]responses.ToolSearchEntry, error) {
					return []responses.ToolSearchEntry{{Name: "weather", Tool: executions.tool("external_weather", "External weather", "external")}}, nil
				}
				set := deferred.ToolSet(searchToolSet{name: "weather", tools: []tool.Tool{
					executions.tool("weather", "Deferred weather member", "deferred_member"),
				}})
				callbacks := model.NewCallbacks().RegisterAfterModel(func(_ context.Context, args *model.AfterModelArgs) (*model.AfterModelResult, error) {
					if edit && args.Response != nil && !args.Response.IsPartial && len(args.Response.Choices) > 0 {
						if data := args.Response.Choices[0].Message.ProviderData; data != nil {
							data.Payload = bytes.ReplaceAll(data.Payload, []byte("city"), []byte("town"))
						}
					}
					return nil, nil
				})
				r := runner.NewRunner("search", llmagent.New("search",
					llmagent.WithModel(newSearchModel(srv, responses.WithClientToolSearch(handler))),
					llmagent.WithToolSets([]tool.ToolSet{set}),
					llmagent.WithGenerationConfig(model.GenerationConfig{Stream: stream}), llmagent.WithModelCallbacks(callbacks)))
				t.Cleanup(func() { _ = r.Close() })
				events, failures := runSearch(t, r, model.NewUserMessage("find weather"))
				require.Empty(t, failures)
				require.Equal(t, "done", finalText(events))
				require.Equal(t, []string{"external"}, executions.executed())
				requests := server.requests()
				require.Len(t, requests, 3)
				require.Equal(t, []string{"tool_search:client"}, toolSummary(t, requests[0]["tools"]))
				wantArg := "city"
				if edit {
					wantArg = "town"
				}
				require.Contains(t, string(requests[2]["input"]), "external:"+wantArg)
			})
		}
	}
}

func TestRunnerAfterModelChangesNativeCallIdentity(t *testing.T) {
	const probe = `{"type":"function_call","id":"fc_probe","status":"completed","call_id":"probe_1","name":"probe","arguments":"{\"id\":\"city\"}"}`
	const member = `{"type":"function_call","id":"fc_weather","status":"completed","call_id":"member_1","namespace":"weather","name":"weather","arguments":"{\"id\":\"city\"}"}`
	for _, tc := range []struct {
		name           string
		top            bool
		previousMember bool
		ambiguous      bool
	}{
		{name: "ambiguous new identity", top: true, ambiguous: true},
		{name: "unique member"},
		{name: "reuse identity from another call", top: true, previousMember: true},
	} {
		for _, stream := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/stream=%v", tc.name, stream), func(t *testing.T) {
				call := "[" + probe + "]"
				if tc.previousMember {
					call = "[" + member + "," + probe + "]"
				}
				server, srv := newSearchServer(t, call, messageOutput("msg_done", "done"))
				executions := &searchExecutions{}
				handler := func(context.Context, *responses.ToolSearchRequest) ([]responses.ToolSearchEntry, error) {
					return nil, nil
				}
				weather := deferred.ToolSet(searchToolSet{name: "weather", tools: []tool.Tool{
					executions.tool("weather", "Eager member", "member"),
					executions.tool("forecast", "Deferred forecast", "forecast"),
				}}, deferred.WithFilter(tool.NewIncludeToolNamesFilter("forecast")))
				tools := []tool.Tool{executions.tool("probe", "Initial call", "probe")}
				if tc.top {
					tools = append(tools, deferred.Tool(executions.tool("weather", "Deferred top level", "hidden")))
				}
				callbacks := model.NewCallbacks().RegisterAfterModel(func(_ context.Context, args *model.AfterModelArgs) (*model.AfterModelResult, error) {
					if args.Response != nil && !args.Response.IsPartial && len(args.Response.Choices) > 0 {
						if data := args.Response.Choices[0].Message.ProviderData; data != nil {
							data.Payload = bytes.ReplaceAll(data.Payload, []byte(`"name":"probe"`), []byte(`"name":"weather","namespace":"weather"`))
						}
					}
					return nil, nil
				})
				r := runner.NewRunner("search", llmagent.New("search",
					llmagent.WithModel(newSearchModel(srv, responses.WithClientToolSearch(handler))),
					llmagent.WithTools(tools), llmagent.WithToolSets([]tool.ToolSet{weather}),
					llmagent.WithGenerationConfig(model.GenerationConfig{Stream: stream}), llmagent.WithModelCallbacks(callbacks)))
				t.Cleanup(func() { _ = r.Close() })
				events, failures := runSearch(t, r, model.NewUserMessage("call weather"))
				if tc.ambiguous {
					require.NotEmpty(t, failures)
					require.Contains(t, failures[0], `function call "weather" in namespace "weather" is ambiguous after callback`)
					require.Empty(t, executions.executed(), "ambiguity must fail before any tool execution")
					require.Len(t, server.requests(), 1)
					return
				}
				require.Empty(t, failures)
				require.Equal(t, "done", finalText(events))
				expected := []string{"member"}
				if tc.previousMember {
					expected = append(expected, "member")
				}
				require.Equal(t, expected, executions.executed())
			})
		}
	}
}

func TestRunnerToolSearchSelfNamespaceAfterMemberSearch(t *testing.T) {
	const call = `[{"type":"function_call","id":"fc_weather","status":"completed","call_id":"weather_1","namespace":"weather","name":"weather","arguments":"{\"id\":\"city\"}"}]`
	for _, tc := range []struct{ loaded, want string }{
		{"forecast", "top"},
		{"weather", "member"},
	} {
		for _, stream := range []bool{false, true} {
			t.Run(fmt.Sprintf("loaded=%s/stream=%v", tc.loaded, stream), func(t *testing.T) {
				_, srv := newSearchServer(t, clientSearchRound, call, messageOutput("msg_done", "done"))
				executions := &searchExecutions{}
				set := deferred.ToolSet(searchToolSet{name: "weather", tools: []tool.Tool{
					executions.tool("weather", "Group weather", "member"),
					executions.tool("forecast", "Group forecast", "forecast"),
				}})
				handler := func(_ context.Context, req *responses.ToolSearchRequest) ([]responses.ToolSearchEntry, error) {
					for _, entry := range req.Candidates {
						if entry.Namespace == "weather" && entry.Name == tc.loaded {
							return []responses.ToolSearchEntry{entry}, nil
						}
					}
					return nil, fmt.Errorf("missing weather.%s candidate", tc.loaded)
				}
				r := runner.NewRunner("search", llmagent.New("search",
					llmagent.WithModel(newSearchModel(srv, responses.WithClientToolSearch(handler))),
					llmagent.WithTools([]tool.Tool{executions.tool("weather", "Top-level weather", "top")}),
					llmagent.WithToolSets([]tool.ToolSet{set}),
					llmagent.WithGenerationConfig(model.GenerationConfig{Stream: stream})))
				t.Cleanup(func() { _ = r.Close() })
				events, failures := runSearch(t, r, model.NewUserMessage("check weather"))
				require.Empty(t, failures)
				require.Equal(t, "done", finalText(events))
				require.Equal(t, []string{tc.want}, executions.executed())
			})
		}
	}
}
