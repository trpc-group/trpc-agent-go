package compiler

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
	"trpc.group/trpc-go/trpc-agent-go/dsl"
	dslcel "trpc.group/trpc-go/trpc-agent-go/dsl/internal/cel"
	"trpc.group/trpc-go/trpc-agent-go/dsl/internal/modelspec"
	"trpc.group/trpc-go/trpc-agent-go/dsl/internal/numconv"
	"trpc.group/trpc-go/trpc-agent-go/dsl/internal/outputformat"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/graph"
	"trpc.group/trpc-go/trpc-agent-go/log"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool/mcp"
)

// resolveModelFromConfig constructs a concrete model instance from config.
// Required: model_spec (provider/model_name/base_url/api_key/headers/extra_fields).
func resolveModelFromConfig(cfg map[string]any, allowEnvSecrets bool) (model.Model, string, error) {
	if cfg == nil {
		return nil, "", fmt.Errorf("model config is nil")
	}

	specRaw, ok := cfg["model_spec"]
	if !ok || specRaw == nil {
		return nil, "", fmt.Errorf("model_spec is required")
	}

	spec, err := modelspec.Parse(specRaw)
	if err != nil {
		return nil, "", err
	}

	spec, err = modelspec.ResolveEnv(spec, allowEnvSecrets)
	if err != nil {
		return nil, "", err
	}

	return modelspec.NewModel(spec)
}

// newLLMAgentNodeFuncFromConfig creates a NodeFunc for a builtin.llmagent node
// given its ID, configuration and model/tool providers. It is a refactoring
// of the compiler's createLLMAgentNodeFunc method so that codegen and other
// callers can reuse the same behavior while remaining agnostic to how
// models are sourced (env vars, platform model service, etc.).
func newLLMAgentNodeFuncFromConfig(
	nodeID string,
	cfg map[string]any,
	toolsProvider dsl.ToolProvider,
	allowEnvSecrets bool,
) (graph.NodeFunc, error) {
	if nodeID == "" {
		return nil, fmt.Errorf("nodeID is required for NewLLMAgentNodeFuncFromConfig")
	}

	llmModel, modelName, err := resolveModelFromConfig(cfg, allowEnvSecrets)
	if err != nil {
		return nil, err
	}

	instruction := ""
	if inst, ok := cfg["instruction"].(string); ok {
		instruction = inst
	}

	description := ""
	if desc, ok := cfg["description"].(string); ok {
		description = desc
	}

	// Resolve tools from provider (if provided).
	var tools []tool.Tool
	if toolsProvider != nil {
		if toolsConfig, ok := cfg["tools"]; ok {
			switch v := toolsConfig.(type) {
			case []interface{}:
				for _, toolNameInterface := range v {
					if toolName, ok := toolNameInterface.(string); ok {
						if t, err := toolsProvider.Get(toolName); err == nil {
							tools = append(tools, t)
						}
					}
				}
			case []string:
				for _, toolName := range v {
					if t, err := toolsProvider.Get(toolName); err == nil {
						tools = append(tools, t)
					}
				}
			}
		}
	}

	// Resolve MCP toolsets from config (if any).
	var mcpToolSets []tool.ToolSet
	if mcpToolsConfig, ok := cfg["mcp_tools"]; ok {
		if mcpToolsList, ok := mcpToolsConfig.([]interface{}); ok {
			for idx, mcpToolInterface := range mcpToolsList {
				mcpToolConfig, ok := mcpToolInterface.(map[string]interface{})
				if !ok {
					return nil, fmt.Errorf("builtin.llmagent[%s]: mcp_tools[%d] must be an object", nodeID, idx)
				}

				rawServerURL, ok := mcpToolConfig["server_url"].(string)
				serverURL := strings.TrimSpace(rawServerURL)
				if !ok || serverURL == "" {
					return nil, fmt.Errorf("builtin.llmagent[%s]: mcp_tools[%d].server_url is required", nodeID, idx)
				}

				transport := "streamable_http"
				if t, ok := mcpToolConfig["transport"].(string); ok && strings.TrimSpace(t) != "" {
					transport = strings.TrimSpace(t)
				}
				if transport != "streamable_http" && transport != "sse" {
					return nil, fmt.Errorf("builtin.llmagent[%s]: mcp_tools[%d].transport %q is not supported (must be \"streamable_http\" or \"sse\")", nodeID, idx, transport)
				}

				var headers map[string]any
				if h, ok := mcpToolConfig["headers"].(map[string]any); ok && len(h) > 0 {
					headers = h
				}

				var toolFilter []interface{}
				if allowed, ok := mcpToolConfig["allowed_tools"]; ok {
					switch v := allowed.(type) {
					case []interface{}:
						for _, elem := range v {
							if name, ok := elem.(string); ok && strings.TrimSpace(name) != "" {
								toolFilter = append(toolFilter, strings.TrimSpace(name))
							}
						}
					case []string:
						for _, name := range v {
							if strings.TrimSpace(name) != "" {
								toolFilter = append(toolFilter, strings.TrimSpace(name))
							}
						}
					}
				}

				cfgMap := map[string]any{
					"transport":  transport,
					"server_url": serverURL,
				}
				if headers != nil {
					cfgMap["headers"] = headers
				}
				if len(toolFilter) > 0 {
					cfgMap["tool_filter"] = toolFilter
				}

				toolSet, err := createMCPToolSet(cfgMap)
				if err != nil {
					return nil, fmt.Errorf("builtin.llmagent[%s]: failed to create MCP toolset for server %q: %w", nodeID, serverURL, err)
				}
				mcpToolSets = append(mcpToolSets, toolSet)
			}
		}
	}

	// Structured output configuration via output_format. When
	// output_format.type == "json", we treat output_format.schema as the
	// JSON Schema for structured output and expose it via
	// node_structured[<id>].output_parsed.
	structuredOutput := outputformat.StructuredSchema(cfg["output_format"])

	var genConfig model.GenerationConfig
	hasGenConfig := false

	if temperatureRaw, ok := cfg["temperature"]; ok {
		temperature, err := numconv.Float64(temperatureRaw, "temperature")
		if err != nil {
			return nil, fmt.Errorf("builtin.llmagent[%s]: %w", nodeID, err)
		}
		genConfig.Temperature = &temperature
		hasGenConfig = true
	}

	if maxTokensRaw, ok := cfg["max_tokens"]; ok {
		tokens, err := numconv.Int(maxTokensRaw, "max_tokens")
		if err != nil {
			return nil, fmt.Errorf("builtin.llmagent[%s]: %w", nodeID, err)
		}
		if tokens <= 0 {
			return nil, fmt.Errorf("builtin.llmagent[%s]: max_tokens must be positive", nodeID)
		}
		genConfig.MaxTokens = &tokens
		hasGenConfig = true
	}

	if topPRaw, ok := cfg["top_p"]; ok {
		topP, err := numconv.Float64(topPRaw, "top_p")
		if err != nil {
			return nil, fmt.Errorf("builtin.llmagent[%s]: %w", nodeID, err)
		}
		genConfig.TopP = &topP
		hasGenConfig = true
	}

	if stopRaw, ok := cfg["stop"]; ok {
		switch v := stopRaw.(type) {
		case []interface{}:
			stop := make([]string, 0, len(v))
			for _, item := range v {
				if s, ok := item.(string); ok {
					stop = append(stop, s)
				}
			}
			if len(stop) > 0 {
				genConfig.Stop = stop
				hasGenConfig = true
			}
		case []string:
			if len(v) > 0 {
				genConfig.Stop = append([]string(nil), v...)
				hasGenConfig = true
			}
		}
	}

	if presenceRaw, ok := cfg["presence_penalty"]; ok {
		presence, err := numconv.Float64(presenceRaw, "presence_penalty")
		if err != nil {
			return nil, fmt.Errorf("builtin.llmagent[%s]: %w", nodeID, err)
		}
		genConfig.PresencePenalty = &presence
		hasGenConfig = true
	}
	if freqRaw, ok := cfg["frequency_penalty"]; ok {
		freq, err := numconv.Float64(freqRaw, "frequency_penalty")
		if err != nil {
			return nil, fmt.Errorf("builtin.llmagent[%s]: %w", nodeID, err)
		}
		genConfig.FrequencyPenalty = &freq
		hasGenConfig = true
	}

	if re, ok := cfg["reasoning_effort"].(string); ok && re != "" {
		genConfig.ReasoningEffort = &re
		hasGenConfig = true
	}

	if thinkingEnabled, ok := cfg["thinking_enabled"].(bool); ok {
		genConfig.ThinkingEnabled = &thinkingEnabled
		hasGenConfig = true
	}
	if thinkingTokensRaw, ok := cfg["thinking_tokens"]; ok {
		tokens, err := numconv.Int(thinkingTokensRaw, "thinking_tokens")
		if err != nil {
			return nil, fmt.Errorf("builtin.llmagent[%s]: %w", nodeID, err)
		}
		if tokens <= 0 {
			return nil, fmt.Errorf("builtin.llmagent[%s]: thinking_tokens must be positive", nodeID)
		}
		genConfig.ThinkingTokens = &tokens
		hasGenConfig = true
	}

	if stream, ok := cfg["stream"].(bool); ok {
		genConfig.Stream = stream
		hasGenConfig = true
	}

	return func(ctx context.Context, state graph.State) (interface{}, error) {
		var opts []llmagent.Option

		opts = append(opts, llmagent.WithModel(llmModel))

		if instruction != "" {
			opts = append(opts, llmagent.WithInstruction(instruction))
		}

		if description != "" {
			opts = append(opts, llmagent.WithDescription(description))
		}

		if len(tools) > 0 {
			opts = append(opts, llmagent.WithTools(tools))
		}

		if len(mcpToolSets) > 0 {
			opts = append(opts, llmagent.WithToolSets(mcpToolSets))
		}

		if len(structuredOutput) > 0 {
			opts = append(opts, llmagent.WithOutputSchema(structuredOutput))
			opts = append(opts, llmagent.WithOutputKey("output_parsed"))
		}

		if hasGenConfig {
			opts = append(opts, llmagent.WithGenerationConfig(genConfig))
		}

		agentName := fmt.Sprintf("llmagent_%s_%s", nodeID, modelName)
		llmAgent := llmagent.New(agentName, opts...)

		parentInvocation, ok := agent.InvocationFromContext(ctx)
		if !ok || parentInvocation == nil {
			return nil, fmt.Errorf("invocation not found in context")
		}

		var parentEventChan chan<- *event.Event
		if execCtx, exists := state[graph.StateKeyExecContext]; exists {
			if execContext, ok := execCtx.(*graph.ExecutionContext); ok {
				parentEventChan = execContext.EventChan
			}
		}

		var userInput string
		if input, exists := state[graph.StateKeyUserInput]; exists {
			if inputStr, ok := input.(string); ok {
				userInput = inputStr
			}
		}

		invocation := parentInvocation.Clone(
			agent.WithInvocationAgent(llmAgent),
			agent.WithInvocationMessage(model.NewUserMessage(userInput)),
			agent.WithInvocationRunOptions(agent.RunOptions{RuntimeState: state}),
		)

		subCtx := agent.NewInvocationContext(ctx, invocation)

		agentEventChan, err := llmAgent.Run(subCtx, invocation)
		if err != nil {
			return nil, fmt.Errorf("failed to run LLM agent: %w", err)
		}

		var lastResponse string
		var messages []model.Message
		var outputParsed any
		hasOutputParsed := false

		for {
			ev, ok := <-agentEventChan
			if !ok {
				goto done
			}

			if ev.Error != nil {
				return nil, fmt.Errorf("LLM agent error: %s", ev.Error.Message)
			}

			if ev.RequiresCompletion {
				// In graph execution, events are forwarded to the parent event channel
				// and the runner will handle persistence and completion notification.
				// Only self‑notify when there's no parent channel (standalone agent run),
				// otherwise we risk double notifications and spurious WARN logs.
				if parentEventChan == nil {
					completionID := agent.GetAppendEventNoticeKey(ev.ID)
					if err := invocation.NotifyCompletion(subCtx, completionID); err != nil {
						log.Warnf("builtin.llmagent: failed to notify completion for %s: %v", completionID, err)
					}
				}
			}

			if parentEventChan != nil {
				if err := event.EmitEvent(ctx, parentEventChan, ev); err != nil {
					return nil, fmt.Errorf("failed to forward event: %w", err)
				}
			}

			if ev.Response != nil {
				for _, ch := range ev.Response.Choices {
					if ch.Message.Role == model.RoleAssistant && ch.Message.Content != "" {
						lastResponse = ch.Message.Content
					}
				}
				if len(ev.Response.Choices) > 0 {
					messages = append(messages, ev.Response.Choices[0].Message)
				}
			}
		}

	done:
		// When structured_output is configured, try to extract the first JSON
		// object/array from the final assistant response and treat it as the
		// structured result. This mirrors the internal OutputResponseProcessor
		// behavior but keeps everything within the DSL layer so that both
		// DSL‑run and codegen‑run can rely on node_structured[<id>].output_parsed.
		if structuredOutput != nil && strings.TrimSpace(lastResponse) != "" {
			if jsonText, ok := extractFirstJSONObjectFromText(lastResponse); ok {
				var parsed any
				if err := json.Unmarshal([]byte(jsonText), &parsed); err != nil {
					log.Warnf("builtin.llmagent[%s]: failed to parse structured output JSON: %v", nodeID, err)
				} else {
					outputParsed = parsed
					hasOutputParsed = true
				}
			}
		}

		result := graph.State{}
		if lastResponse != "" {
			result[graph.StateKeyLastResponse] = lastResponse
		}
		if len(messages) > 0 {
			result[graph.StateKeyMessages] = messages
		}
		if hasOutputParsed {
			// Merge with existing node_structured cache (if any) to avoid
			// clobbering structured outputs from other nodes.
			nodeStructured := map[string]any{}
			if existingRaw, ok := state["node_structured"]; ok {
				if existingMap, ok := existingRaw.(map[string]any); ok && existingMap != nil {
					for k, v := range existingMap {
						nodeStructured[k] = v
					}
				}
			}
			nodeStructured[nodeID] = map[string]any{
				"output_parsed": outputParsed,
			}
			result["node_structured"] = nodeStructured
		}
		if len(result) == 0 {
			return nil, nil
		}
		return result, nil
	}, nil
}

// newUserApprovalNodeFuncFromConfig creates a NodeFunc for a builtin.user_approval
// node given its ID and configuration. It mirrors the compiler's
// createUserApprovalNodeFunc behavior.
func newUserApprovalNodeFuncFromConfig(nodeID string, cfg map[string]any) (graph.NodeFunc, error) {
	if nodeID == "" {
		return nil, fmt.Errorf("nodeID is required for NewUserApprovalNodeFuncFromConfig")
	}

	message := "Please approve this action (yes/no):"
	if msg, ok := cfg["message"].(string); ok && strings.TrimSpace(msg) != "" {
		message = msg
	}

	autoApprove := false
	if v, ok := cfg["auto_approve"].(bool); ok {
		autoApprove = v
	}

	interruptKey := nodeID

	return func(ctx context.Context, state graph.State) (any, error) {
		if autoApprove {
			return graph.State{
				"approval_result": "approve",
			}, nil
		}

		payload := map[string]any{
			"message": message,
			"node_id": nodeID,
		}

		resumeValue, err := graph.Interrupt(ctx, state, interruptKey, payload)
		if err != nil {
			return nil, err
		}

		decisionRaw, _ := resumeValue.(string)
		decision := strings.ToLower(strings.TrimSpace(decisionRaw))

		normalized := "reject"
		if decision == "approve" || decision == "yes" || decision == "y" {
			normalized = "approve"
		}

		return graph.State{
			"approval_result": normalized,
		}, nil
	}, nil
}

// newBuiltinConditionFunc creates a ConditionalFunc for a builtin CEL-based
// condition. It mirrors the compiler's createBuiltinCondition behavior and is
// used internally so that conditional routing logic is defined in a single place.
func newBuiltinConditionFunc(fromNodeID string, cond dsl.Condition) (graph.ConditionalFunc, error) {
	if len(cond.Cases) == 0 {
		return nil, fmt.Errorf("builtin condition requires at least one case")
	}

	// Create a local copy of cases and compile their predicates once so that
	// runtime evaluation only needs to execute the compiled CEL programs.
	type compiledCase struct {
		caseDef dsl.Case
		prog    *dslcel.BoolProgram
	}
	compiled := make([]compiledCase, 0, len(cond.Cases))
	for idx, kase := range cond.Cases {
		expr := strings.TrimSpace(kase.Predicate.Expression)
		if expr == "" {
			return nil, fmt.Errorf("builtin case %d predicate.expression is required", idx)
		}
		prog, err := dslcel.CompileBool(expr)
		if err != nil {
			return nil, fmt.Errorf("failed to compile builtin case %d expression: %w", idx, err)
		}
		compiled = append(compiled, compiledCase{
			caseDef: kase,
			prog:    prog,
		})
	}

	return func(ctx context.Context, state graph.State) (string, error) {
		input := buildNodeInputView(state, fromNodeID)

		for idx, c := range compiled {
			ok, err := c.prog.Eval(state, input)
			if err != nil {
				return "", fmt.Errorf("failed to evaluate builtin case %d: %w", idx, err)
			}
			if ok {
				log.Debugf("[COND] builtin case matched index=%d name=%q target=%q", idx, c.caseDef.Name, c.caseDef.Target)
				if c.caseDef.Target == "" {
					return "", fmt.Errorf("builtin case %d has empty target", idx)
				}
				return c.caseDef.Target, nil
			}
		}

		if cond.Default != "" {
			log.Debugf("[COND] builtin no case matched, using default target=%q", cond.Default)
			return cond.Default, nil
		}
		return "", fmt.Errorf("no builtin case matched and no default specified")
	}, nil
}

// createMCPToolSet is a helper that constructs an MCP ToolSet from DSL
// configuration.
func createMCPToolSet(config map[string]interface{}) (tool.ToolSet, error) {
	transport, ok := config["transport"].(string)
	if !ok || transport == "" {
		return nil, fmt.Errorf("transport is required in MCP tool config")
	}

	connConfig := mcp.ConnectionConfig{
		Transport: transport,
	}

	timeout := 10 * time.Second
	if timeoutVal, ok := config["timeout"]; ok {
		switch v := timeoutVal.(type) {
		case float64:
			timeout = time.Duration(v) * time.Second
		case int:
			timeout = time.Duration(v) * time.Second
		}
	}
	connConfig.Timeout = timeout

	switch transport {
	case "stdio":
		command, ok := config["command"].(string)
		if !ok || command == "" {
			return nil, fmt.Errorf("command is required for stdio transport")
		}
		connConfig.Command = command

		if argsVal, ok := config["args"]; ok {
			if argsList, ok := argsVal.([]interface{}); ok {
				args := make([]string, 0, len(argsList))
				for _, arg := range argsList {
					if argStr, ok := arg.(string); ok {
						args = append(args, argStr)
					}
				}
				connConfig.Args = args
			}
		}

	case "streamable_http", "sse":
		serverURL, ok := config["server_url"].(string)
		if !ok || serverURL == "" {
			return nil, fmt.Errorf("server_url is required for %s transport", transport)
		}
		connConfig.ServerURL = serverURL

		if headersVal, ok := config["headers"]; ok {
			if headersMap, ok := headersVal.(map[string]interface{}); ok {
				headers := make(map[string]string)
				for k, v := range headersMap {
					if vStr, ok := v.(string); ok {
						headers[k] = vStr
					}
				}
				connConfig.Headers = headers
			}
		}

	default:
		return nil, fmt.Errorf("unsupported transport type: %s", transport)
	}

	var mcpOpts []mcp.ToolSetOption

	if toolFilterVal, ok := config["tool_filter"]; ok {
		if toolFilterList, ok := toolFilterVal.([]interface{}); ok {
			toolNames := make([]string, 0, len(toolFilterList))
			for _, name := range toolFilterList {
				if nameStr, ok := name.(string); ok {
					toolNames = append(toolNames, nameStr)
				}
			}
			if len(toolNames) > 0 {
				mcpOpts = append(mcpOpts, mcp.WithToolFilterFunc(tool.NewIncludeToolNamesFilter(toolNames...)))
			}
		}
	}

	return mcp.NewMCPToolSet(connConfig, mcpOpts...), nil
}
