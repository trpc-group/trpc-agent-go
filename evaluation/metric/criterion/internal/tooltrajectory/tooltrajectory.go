package tooltrajectory

import (
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"sort"

	"google.golang.org/genai"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evalset"
	imaptext "trpc.group/trpc-go/trpc-agent-go/evaluation/metric/criterion/internal/maptext"
	itext "trpc.group/trpc-go/trpc-agent-go/evaluation/metric/criterion/internal/text"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/metric/criterion/maptext"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/metric/criterion/text"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/metric/criterion/tooltrajectory"
)

// Match compares actual and expected invocations according to tool trajectory rules.
func Match(t *tooltrajectory.ToolTrajectoryCriterion, actual, expected *evalset.Invocation) error {
	if t.Compare != nil {
		return t.Compare(actual, expected)
	}
	if actual == nil || expected == nil {
		return fmt.Errorf("actual or expected invocation is nil")
	}
	if actual.IntermediateData == nil || expected.IntermediateData == nil {
		return fmt.Errorf("actual or expected intermediate data is nil")
	}
	// Ensure one-to-one mapping between tool calls and responses on actual invocation.
	if len(actual.IntermediateData.ToolUses) != len(actual.IntermediateData.ToolResponses) {
		return fmt.Errorf("tool uses and tool responses count mismatch: %d != %d",
			len(actual.IntermediateData.ToolUses), len(actual.IntermediateData.ToolResponses))
	}
	// Ensure one-to-one mapping between tool calls and responses on expected invocation.
	if len(expected.IntermediateData.ToolUses) != len(expected.IntermediateData.ToolResponses) {
		return fmt.Errorf("tool uses and tool responses count mismatch: %d != %d",
			len(expected.IntermediateData.ToolUses), len(expected.IntermediateData.ToolResponses))
	}
	// Ensure the same number of tool uses before detailed comparison.
	if len(actual.IntermediateData.ToolUses) != len(expected.IntermediateData.ToolUses) {
		return fmt.Errorf("tool uses count mismatch: %d != %d",
			len(actual.IntermediateData.ToolUses), len(expected.IntermediateData.ToolUses))
	}
	if len(actual.IntermediateData.ToolUses) == 0 {
		return nil
	}
	actualTools, err := getToolComparers(t,
		actual.IntermediateData.ToolUses,
		actual.IntermediateData.ToolResponses,
		t.OrderInsensitive,
	)
	if err != nil {
		return fmt.Errorf("get actual tools: %w", err)
	}
	expectedTools, err := getToolComparers(t,
		expected.IntermediateData.ToolUses,
		expected.IntermediateData.ToolResponses,
		t.OrderInsensitive,
	)
	if err != nil {
		return fmt.Errorf("get expected tools: %w", err)
	}
	if t.OrderInsensitive {
		sort.Slice(actualTools, func(i, j int) bool {
			return actualTools[i].lessThan(actualTools[j])
		})
		sort.Slice(expectedTools, func(i, j int) bool {
			return expectedTools[i].lessThan(expectedTools[j])
		})
	}
	for i := range len(actualTools) {
		strategy := getStrategy(t, actualTools[i], expectedTools[i])
		if err := MatchStrategy(strategy, actualTools[i], expectedTools[i]); err != nil {
			return fmt.Errorf("tool %s mismatch: %w", actualTools[i].name, err)
		}
	}
	return nil
}

// getToolComparers aligns tool uses with their responses and builds toolComparer.
func getToolComparers(t *tooltrajectory.ToolTrajectoryCriterion, toolUses []*genai.FunctionCall,
	toolResponses []*genai.FunctionResponse, orderInsensitive bool) ([]*toolComparer, error) {
	// toolCallIDs ensures every tool use can be matched by ID.
	// Map from tool call id to index.
	toolCallIDs := make(map[string]int)
	for i := range len(toolUses) {
		if toolUses[i].ID == "" {
			return nil, fmt.Errorf("tool use id is empty")
		}
		if _, ok := toolCallIDs[toolUses[i].ID]; ok {
			return nil, fmt.Errorf("tool use id %s is duplicated", toolUses[i].ID)
		}
		toolCallIDs[toolUses[i].ID] = i
	}
	// toolResponseIDs ensures every tool response can be matched by ID.
	// Map from tool response id to index.
	toolResponseIDs := make(map[string]int)
	for i := range len(toolResponses) {
		if toolResponses[i].ID == "" {
			return nil, fmt.Errorf("tool response id is empty")
		}
		if _, ok := toolResponseIDs[toolResponses[i].ID]; ok {
			return nil, fmt.Errorf("tool response id %s is duplicated", toolResponses[i].ID)
		}
		toolResponseIDs[toolResponses[i].ID] = i
	}
	for toolID := range toolCallIDs {
		if _, ok := toolResponseIDs[toolID]; !ok {
			return nil, fmt.Errorf("tool id %s is missing response", toolID)
		}
	}
	toolComparers := make([]*toolComparer, 0, len(toolUses))
	for i := range len(toolUses) {
		toolComparer, err := getToolComparer(
			toolUses[i],
			toolResponses[toolResponseIDs[toolUses[i].ID]],
			orderInsensitive,
		)
		if err != nil {
			return nil, fmt.Errorf("get tool comparer: %w", err)
		}
		toolComparers = append(toolComparers, toolComparer)
	}
	return toolComparers, nil
}

// getStrategy picks the comparison strategy for a specific tool pair.
func getStrategy(t *tooltrajectory.ToolTrajectoryCriterion, actualTool, expectedTool *toolComparer) *tooltrajectory.ToolTrajectoryStrategy {
	if t.ToolStrategy != nil {
		strategy, ok := t.ToolStrategy[actualTool.name]
		if ok {
			return strategy
		}
		strategy, ok = t.ToolStrategy[expectedTool.name]
		if ok {
			return strategy
		}
	}
	if t.DefaultStrategy != nil {
		return t.DefaultStrategy
	}
	return &tooltrajectory.ToolTrajectoryStrategy{
		Name: &text.TextCriterion{
			MatchStrategy: text.TextMatchStrategyExact,
		},
		Arguments: &maptext.MapTextCriterion{
			Compare: func(actual, expected map[string]any) error {
				if !reflect.DeepEqual(actual, expected) {
					return fmt.Errorf("actual %v and expected %v do not match", actual, expected)
				}
				return nil
			},
		},
		Response: &maptext.MapTextCriterion{
			Compare: func(actual, expected map[string]any) error {
				if !reflect.DeepEqual(actual, expected) {
					return fmt.Errorf("actual %v and expected %v do not match", actual, expected)
				}
				return nil
			},
		},
	}
}

// Match validates a single tool call pair using configured criteria.
func MatchStrategy(t *tooltrajectory.ToolTrajectoryStrategy, actual, expected *toolComparer) error {
	if t.Name != nil {
		if err := itext.Match(t.Name, actual.name, expected.name); err != nil {
			return fmt.Errorf("name mismatch: %w", err)
		}
	}
	if t.Arguments != nil {
		if err := imaptext.Match(t.Arguments, actual.args, expected.args); err != nil {
			return fmt.Errorf("arguments mismatch: %w", err)
		}
	}
	if t.Response != nil {
		if err := imaptext.Match(t.Response, actual.response, expected.response); err != nil {
			return fmt.Errorf("response mismatch: %w", err)
		}
	}
	return nil
}

// toolComparer normalizes tool call and response data for comparison.
type toolComparer struct {
	name          string         // name holds the tool name.
	args          map[string]any // args holds parsed tool arguments.
	response      map[string]any // response holds parsed tool response payload.
	argsOrder     string         // argsOrder caches JSON for order-insensitive compare.
	responseOrder string         // responseOrder caches JSON for order-insensitive compare.
}

// lessThan provides deterministic ordering when order-insensitive compares require sorting.
func (t *toolComparer) lessThan(other *toolComparer) bool {
	if t.name != other.name {
		return t.name < other.name
	}
	if t.argsOrder != other.argsOrder {
		return t.argsOrder < other.argsOrder
	}
	if t.responseOrder != other.responseOrder {
		return t.responseOrder < other.responseOrder
	}
	return false
}

// getToolComparer pairs a tool use with its response and precomputes ordering hints.
func getToolComparer(toolUse *genai.FunctionCall, toolResponse *genai.FunctionResponse,
	orderInsensitive bool) (*toolComparer, error) {
	if toolUse == nil || toolResponse == nil {
		return nil, errors.New("tool use or tool response is nil")
	}
	tool := &toolComparer{
		name:     toolUse.Name,
		args:     toolUse.Args,
		response: toolResponse.Response,
	}
	if orderInsensitive {
		args, err := json.Marshal(toolUse.Args)
		if err != nil {
			return nil, fmt.Errorf("marshal arguments: %w", err)
		}
		response, err := json.Marshal(toolResponse.Response)
		if err != nil {
			return nil, fmt.Errorf("marshal response: %w", err)
		}
		tool.argsOrder = string(args)
		tool.responseOrder = string(response)
	}
	return tool, nil
}
