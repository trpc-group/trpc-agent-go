package react

import (
	"context"
	"fmt"
	"strings"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/log"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

// RuleBasedActionSelector selects actions based on predefined rules.
type RuleBasedActionSelector struct {
	rules        map[string]string // Map of patterns to tool names
	defaultTool  string            // Default tool to use if no rules match
}

// NewRuleBasedActionSelector creates a new rule-based action selector.
// The rules parameter is a map of patterns to tool names.
// The defaultTool parameter is the name of the tool to use if no rules match.
func NewRuleBasedActionSelector(rules map[string]string, defaultTool ...string) *RuleBasedActionSelector {
	defTool := ""
	if len(defaultTool) > 0 {
		defTool = defaultTool[0]
	}
	return &RuleBasedActionSelector{
		rules:       rules,
		defaultTool: defTool,
	}
}

// Select selects an action based on predefined rules.
func (s *RuleBasedActionSelector) Select(ctx context.Context, thought *Thought, tools []tool.Tool) (*Action, error) {
	log.Debugf("RuleBasedActionSelector: Selecting action based on thought: %s", thought.Content)
	
	// Find a matching tool based on rules
	var selectedToolName string
	
	// Check if any rule pattern matches the thought content
	thoughtLower := strings.ToLower(thought.Content)
	for pattern, toolName := range s.rules {
		if strings.Contains(thoughtLower, strings.ToLower(pattern)) {
			selectedToolName = toolName
			log.Debugf("RuleBasedActionSelector: Found matching rule '%s' -> '%s'", pattern, toolName)
			break
		}
	}
	
	// If no rule matched, use the default tool
	if selectedToolName == "" && s.defaultTool != "" {
		selectedToolName = s.defaultTool
		log.Debugf("RuleBasedActionSelector: No matching rule found, using default tool: %s", s.defaultTool)
	}
	
	// If no tool was selected, return an error
	if selectedToolName == "" {
		return nil, fmt.Errorf("no matching rule found and no default tool specified")
	}
	
	// Find the selected tool in the available tools
	var selectedTool tool.Tool
	for _, t := range tools {
		if t.Name() == selectedToolName {
			selectedTool = t
			break
		}
	}
	
	// If the selected tool doesn't exist, return an error
	if selectedTool == nil {
		return nil, fmt.Errorf("selected tool '%s' not found in available tools", selectedToolName)
	}
	
	// Create an empty tool input - in a real implementation, you might want to
	// extract parameters from the thought content using some parsing logic
	toolInput := map[string]interface{}{}
	
	// Create and return the action
	action := &Action{
		ID:        fmt.Sprintf("action-%d", time.Now().UnixNano()),
		ThoughtID: thought.ID,
		ToolName:  selectedToolName,
		ToolInput: toolInput,
		Timestamp: time.Now().Unix(),
	}
	
	log.Debugf("RuleBasedActionSelector: Selected action: %s", selectedToolName)
	return action, nil
} 