package react

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/memory"
	"trpc.group/trpc-go/trpc-agent-go/message"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/model/models"
	"trpc.group/trpc-go/trpc-agent-go/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool/tools"
)

// MockThoughtGenerator for testing.
type MockThoughtGenerator struct {
	GenerateFunc func(ctx context.Context, history []*message.Message, previousCycles []*Cycle) (*Thought, error)
}

func (m *MockThoughtGenerator) Generate(ctx context.Context, history []*message.Message, previousCycles []*Cycle) (*Thought, error) {
	if m.GenerateFunc != nil {
		return m.GenerateFunc(ctx, history, previousCycles)
	}
	return &Thought{ID: "mock-thought", Content: "Mocked thought"}, nil
}

// MockActionSelector for testing.
type MockActionSelector struct {
	SelectFunc func(ctx context.Context, thought *Thought, availableTools []tool.Tool) (*Action, error)
}

func (m *MockActionSelector) Select(ctx context.Context, thought *Thought, availableTools []tool.Tool) (*Action, error) {
	if m.SelectFunc != nil {
		return m.SelectFunc(ctx, thought, availableTools)
	}
	return &Action{ID: "mock-action", ToolName: "mock_tool"}, nil
}

// MockResponseGenerator for testing.
type MockResponseGenerator struct {
	GenerateFunc func(ctx context.Context, goal string, history []*message.Message, cycles []*Cycle) (*message.Message, error)
}

func (m *MockResponseGenerator) Generate(ctx context.Context, goal string, history []*message.Message, cycles []*Cycle) (*message.Message, error) {
	if m.GenerateFunc != nil {
		return m.GenerateFunc(ctx, goal, history, cycles)
	}
	return message.NewAssistantMessage("Mocked final response"), nil
}

// MockCycleManager for testing.
type MockCycleManager struct {
	StartCycleFunc        func(ctx context.Context, thought *Thought) error
	RecordActionFunc      func(ctx context.Context, action *Action) error
	RecordObservationFunc func(ctx context.Context, observation *CycleObservation) error
	EndCycleFunc          func(ctx context.Context) (*Cycle, error)
	GetHistoryFunc        func(ctx context.Context) ([]*Cycle, error)
	CurrentCycleFunc      func(ctx context.Context) (*Cycle, error)
}

func (m *MockCycleManager) StartCycle(ctx context.Context, thought *Thought) error {
	if m.StartCycleFunc != nil {
		return m.StartCycleFunc(ctx, thought)
	}
	return nil
}
func (m *MockCycleManager) RecordAction(ctx context.Context, action *Action) error {
	if m.RecordActionFunc != nil {
		return m.RecordActionFunc(ctx, action)
	}
	return nil
}
func (m *MockCycleManager) RecordObservation(ctx context.Context, observation *CycleObservation) error {
	if m.RecordObservationFunc != nil {
		return m.RecordObservationFunc(ctx, observation)
	}
	return nil
}
func (m *MockCycleManager) EndCycle(ctx context.Context) (*Cycle, error) {
	if m.EndCycleFunc != nil {
		return m.EndCycleFunc(ctx)
	}
	return &Cycle{ID: "mock-cycle"}, nil
}
func (m *MockCycleManager) GetHistory(ctx context.Context) ([]*Cycle, error) {
	if m.GetHistoryFunc != nil {
		return m.GetHistoryFunc(ctx)
	}
	return []*Cycle{}, nil
}
func (m *MockCycleManager) CurrentCycle(ctx context.Context) (*Cycle, error) {
	if m.CurrentCycleFunc != nil {
		return m.CurrentCycleFunc(ctx)
	}
	return nil, nil
}

// Mocks for testing

// mockModel mocks a model for testing.
type mockModel struct {
	response *model.Response
	error    error
	tools    []model.ToolDefinition
	name     string
	provider string
}

func (m *mockModel) Name() string {
	return m.name
}

func (m *mockModel) Provider() string {
	return m.provider
}

func (m *mockModel) Generate(ctx context.Context, prompt string, opts model.GenerationOptions) (*model.Response, error) {
	if m.error != nil {
		return nil, m.error
	}
	return m.response, nil
}

func (m *mockModel) GenerateWithMessages(ctx context.Context, messages []*message.Message, opts model.GenerationOptions) (*model.Response, error) {
	if m.error != nil {
		return nil, m.error
	}
	return m.response, nil
}

func (m *mockModel) SetTools(tools []model.ToolDefinition) error {
	m.tools = tools
	return nil
}

// SupportsToolCalls implements the model.Model interface
func (m *mockModel) SupportsToolCalls() bool {
	return true
}

// RegisterTools implements the model.ToolCallSupportingModel interface
func (m *mockModel) RegisterTools(toolDefs []*tool.ToolDefinition) error {
	return nil
}

// Implement model.ToolCallSupportingModel interface
var _ model.ToolCallSupportingModel = (*mockModel)(nil)

// mockTool mocks a tool for testing.
type mockTool struct {
	name        string
	description string
	parameters  map[string]interface{}
	output      *tool.Result
	error       error
	executeFunc func(ctx context.Context, args map[string]interface{}) (*tool.Result, error)
}

func (t *mockTool) Name() string {
	return t.name
}

func (t *mockTool) Description() string {
	return t.description
}

func (t *mockTool) Parameters() map[string]interface{} {
	return t.parameters
}

// GetDefinition implements the tool.Tool interface
func (t *mockTool) GetDefinition() *tool.ToolDefinition {
	def := tool.NewToolDefinition(t.name, t.description)

	// Add parameters if any
	for _, val := range t.parameters {
		if propMap, ok := val.(map[string]interface{}); ok {
			// For object type with properties
			if properties, ok := propMap["properties"].(map[string]interface{}); ok {
				for propName, propDef := range properties {
					if propObj, ok := propDef.(map[string]interface{}); ok {
						prop := createPropertyFromMap(propObj)
						// Check if it's required (simplified logic)
						required := false
						if reqList, ok := propMap["required"].([]string); ok {
							for _, req := range reqList {
								if req == propName {
									required = true
									break
								}
							}
						}
						def.AddParameter(propName, prop, required)
					}
				}
			}
		}
	}

	return def
}

// Helper function to create a Property from a map
func createPropertyFromMap(m map[string]interface{}) *tool.Property {
	prop := &tool.Property{}

	if typ, ok := m["type"].(string); ok {
		prop.Type = typ
	}

	if desc, ok := m["description"].(string); ok {
		prop.Description = desc
	}

	if def, ok := m["default"]; ok {
		prop.Default = def
	}

	if enum, ok := m["enum"].([]interface{}); ok {
		prop.Enum = enum
	}

	return prop
}

func (t *mockTool) Execute(ctx context.Context, args map[string]interface{}) (*tool.Result, error) {
	if t.executeFunc != nil {
		return t.executeFunc(ctx, args)
	}
	if t.error != nil {
		return nil, t.error
	}
	return t.output, nil
}

// TestNewReActAgent tests the creation of a ReAct agent.
func TestNewReActAgent(t *testing.T) {
	// Create mocks
	mockMdl := &mockModel{
		name:     "test-model",
		provider: "test-provider",
		response: &model.Response{
			Messages: []*message.Message{
				message.NewAssistantMessage("Test response"),
			},
		},
	}

	mockToolDef := mockTool{
		name:        "test_tool",
		description: "A test tool",
		parameters: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"query": map[string]interface{}{
					"type":        "string",
					"description": "The query to process",
				},
			},
		},
		output: tool.NewResult("Tool executed successfully"),
	}

	// Create the components
	thoughtGen := NewTemplateThoughtGenerator(map[string]string{
		"default": "I'll use the test_tool to process this query. Final Answer: This is a test answer.",
	})

	actionSel := NewRuleBasedActionSelector(map[string]string{
		"test": "test_tool",
	}, "test_tool")

	respGen := NewDirectResponseGenerator(true)

	cycleMan := NewInMemoryCycleManager()

	// Create the agent
	agent, err := NewReActAgent(ReActAgentConfig{
		Name:              "TestReActAgent",
		Description:       "A test ReAct agent",
		Model:             mockMdl,
		Tools:             []tool.Tool{&mockToolDef},
		MaxIterations:     3,
		ThoughtGenerator:  thoughtGen,
		ActionSelector:    actionSel,
		ResponseGenerator: respGen,
		CycleManager:      cycleMan,
	})

	// Assertions
	assert.NoError(t, err)
	assert.NotNil(t, agent)
	assert.Equal(t, "TestReActAgent", agent.LLMAgent.Name())
	assert.Equal(t, 3, agent.MaxIterations())
	assert.NotNil(t, agent.thoughtGenerator)
	assert.NotNil(t, agent.actionSelector)
	assert.NotNil(t, agent.responseGenerator)
	assert.NotNil(t, agent.cycleManager)
}

// TestReActAgent_Errors tests error cases when creating a ReAct agent.
func TestReActAgent_Errors(t *testing.T) {
	// Create mocks
	mockMdl := &mockModel{
		name:     "test-model",
		provider: "test-provider",
		response: &model.Response{
			Messages: []*message.Message{
				message.NewAssistantMessage("Test response"),
			},
		},
	}

	mockToolDef := mockTool{
		name:        "test_tool",
		description: "A test tool",
		output:      tool.NewResult("Tool executed successfully"),
	}

	// Component mocks
	thoughtGen := NewTemplateThoughtGenerator(map[string]string{
		"default": "Test thought",
	})
	actionSel := NewRuleBasedActionSelector(map[string]string{
		"test": "test_tool",
	}, "test_tool")
	respGen := NewDirectResponseGenerator(true)
	cycleMan := NewInMemoryCycleManager()

	// Test missing model
	_, err := NewReActAgent(ReActAgentConfig{
		Name:              "TestReActAgent",
		Tools:             []tool.Tool{&mockToolDef},
		ThoughtGenerator:  thoughtGen,
		ActionSelector:    actionSel,
		ResponseGenerator: respGen,
		CycleManager:      cycleMan,
	})
	assert.Error(t, err)
	assert.Equal(t, ErrModelRequired, err)

	// Test missing tools
	_, err = NewReActAgent(ReActAgentConfig{
		Name:              "TestReActAgent",
		Model:             mockMdl,
		ThoughtGenerator:  thoughtGen,
		ActionSelector:    actionSel,
		ResponseGenerator: respGen,
		CycleManager:      cycleMan,
	})
	assert.Error(t, err)
	assert.Equal(t, ErrNoToolsProvided, err)

	// Test missing thought generator
	_, err = NewReActAgent(ReActAgentConfig{
		Name:              "TestReActAgent",
		Model:             mockMdl,
		Tools:             []tool.Tool{&mockToolDef},
		ActionSelector:    actionSel,
		ResponseGenerator: respGen,
		CycleManager:      cycleMan,
	})
	assert.Error(t, err)
	assert.Equal(t, ErrThoughtGeneratorRequired, err)

	// Test missing action selector
	_, err = NewReActAgent(ReActAgentConfig{
		Name:              "TestReActAgent",
		Model:             mockMdl,
		Tools:             []tool.Tool{&mockToolDef},
		ThoughtGenerator:  thoughtGen,
		ResponseGenerator: respGen,
		CycleManager:      cycleMan,
	})
	assert.Error(t, err)
	assert.Equal(t, ErrActionSelectorRequired, err)

	// Test missing response generator
	_, err = NewReActAgent(ReActAgentConfig{
		Name:             "TestReActAgent",
		Model:            mockMdl,
		Tools:            []tool.Tool{&mockToolDef},
		ThoughtGenerator: thoughtGen,
		ActionSelector:   actionSel,
		CycleManager:     cycleMan,
	})
	assert.Error(t, err)
	assert.Equal(t, ErrResponseGeneratorRequired, err)

	// Test missing cycle manager
	_, err = NewReActAgent(ReActAgentConfig{
		Name:              "TestReActAgent",
		Model:             mockMdl,
		Tools:             []tool.Tool{&mockToolDef},
		ThoughtGenerator:  thoughtGen,
		ActionSelector:    actionSel,
		ResponseGenerator: respGen,
	})
	assert.Error(t, err)
	assert.Equal(t, ErrCycleManagerRequired, err)
}

// TestReActAgent_Run tests the Run method of the ReAct agent.
func TestReActAgent_Run(t *testing.T) {
	// Create a tool
	calculator := &mockTool{
		name:        "calculator",
		description: "Performs arithmetic operations",
		parameters: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"operation": map[string]interface{}{
					"type":        "string",
					"description": "The operation to perform (add, subtract, multiply, divide)",
				},
				"a": map[string]interface{}{
					"type":        "number",
					"description": "First operand",
				},
				"b": map[string]interface{}{
					"type":        "number",
					"description": "Second operand",
				},
			},
		},
		output: tool.NewResult("42"),
	}

	// Create a model that simulates the thought generation process
	// This model will be used for all the LLM-based components
	mdl := &mockModel{
		name:     "test-model",
		provider: "test-provider",
		response: &model.Response{
			Messages: []*message.Message{
				message.NewAssistantMessage("I'll use the calculator to add 2 and 2. Final Answer: The result is 4."),
			},
		},
	}

	// Create the components with fixed responses for deterministic testing
	thoughtGen := NewTemplateThoughtGenerator(map[string]string{
		"default": "I need to calculate 2+2. I'll use the calculator tool.",
	})

	actionSel := NewTemplateActionSelector(map[string]interface{}{
		"tool_name": "calculator",
		"tool_input": map[string]interface{}{
			"operation": "add",
			"a":         2,
			"b":         2,
		},
	})

	respGen := NewDirectResponseGenerator(true)
	cycleMan := NewInMemoryCycleManager()

	// Create the agent
	agent, err := NewReActAgent(ReActAgentConfig{
		Name:              "TestReActAgent",
		Description:       "A test ReAct agent",
		Model:             mdl,
		Tools:             []tool.Tool{calculator},
		MaxIterations:     3,
		ThoughtGenerator:  thoughtGen,
		ActionSelector:    actionSel,
		ResponseGenerator: respGen,
		CycleManager:      cycleMan,
	})
	assert.NoError(t, err)

	// Run the agent
	userMsg := message.NewUserMessage("What is 2+2?")
	response, err := agent.Run(context.Background(), userMsg)

	// Assertions
	assert.NoError(t, err)
	assert.NotNil(t, response)
	// Add more specific assertions based on expected behavior
}

// A special action selector that always returns a fixed action (for testing)
type TemplateActionSelector struct {
	action map[string]interface{}
}

func NewTemplateActionSelector(action map[string]interface{}) *TemplateActionSelector {
	return &TemplateActionSelector{
		action: action,
	}
}

func (s *TemplateActionSelector) Select(ctx context.Context, thought *Thought, tools []tool.Tool) (*Action, error) {
	toolName, ok := s.action["tool_name"].(string)
	if !ok {
		return nil, fmt.Errorf("tool_name not found or not a string")
	}

	toolInput, ok := s.action["tool_input"].(map[string]interface{})
	if !ok {
		return nil, fmt.Errorf("tool_input not found or not a map")
	}

	return &Action{
		ID:        "action-test-1",
		ThoughtID: thought.ID,
		ToolName:  toolName,
		ToolInput: toolInput,
		Timestamp: 0, // Use 0 for testing
	}, nil
}

func TestNewReActAgent_Success(t *testing.T) {
	mockModel := models.NewMockModel("test-react-model", "test-provider", models.WithToolCallSupport())
	mockTool := tools.NewCalculatorTool()
	mockTG := &MockThoughtGenerator{}
	mockAS := &MockActionSelector{}
	mockRG := &MockResponseGenerator{}
	mockCM := &MockCycleManager{}

	config := ReActAgentConfig{
		Name:              "TestReActAgent",
		Description:       "A test ReAct agent",
		Model:             mockModel,
		Tools:             []tool.Tool{mockTool},
		MaxIterations:     5,
		ThoughtGenerator:  mockTG,
		ActionSelector:    mockAS,
		ResponseGenerator: mockRG,
		CycleManager:      mockCM,
		Memory:            memory.NewBaseMemory(),
	}

	agent, err := NewReActAgent(config)
	require.NoError(t, err)
	require.NotNil(t, agent)
	assert.Equal(t, "TestReActAgent", agent.Name())
	assert.Equal(t, "A test ReAct agent", agent.Description())
	assert.NotNil(t, agent.LLMAgent)
	assert.Equal(t, 5, agent.MaxIterations())
	assert.Equal(t, mockTG, agent.thoughtGenerator)
	assert.Equal(t, mockAS, agent.actionSelector)
	assert.Equal(t, mockRG, agent.responseGenerator)
	assert.Equal(t, mockCM, agent.cycleManager)
	assert.NotEmpty(t, agent.LLMAgent.GetSystemPrompt())
}

func TestNewReActAgent_ErrorConditions(t *testing.T) {
	mockModel := models.NewMockModel("test-react-model", "test-provider", models.WithToolCallSupport())
	mockTool := tools.NewCalculatorTool()
	mockTG := &MockThoughtGenerator{}
	mockAS := &MockActionSelector{}
	mockRG := &MockResponseGenerator{}
	mockCM := &MockCycleManager{}
	baseMem := memory.NewBaseMemory()

	validConfig := func() ReActAgentConfig {
		return ReActAgentConfig{
			Name:              "TestReActAgent",
			Model:             mockModel,
			Tools:             []tool.Tool{mockTool},
			ThoughtGenerator:  mockTG,
			ActionSelector:    mockAS,
			ResponseGenerator: mockRG,
			CycleManager:      mockCM,
			Memory:            baseMem,
		}
	}

	tests := []struct {
		name          string
		config        ReActAgentConfig
		expectedError error
	}{
		{"NilModel", func() ReActAgentConfig { c := validConfig(); c.Model = nil; return c }(), ErrModelRequired},
		{"NoTools", func() ReActAgentConfig { c := validConfig(); c.Tools = []tool.Tool{}; return c }(), ErrNoToolsProvided},
		{"NilThoughtGenerator", func() ReActAgentConfig { c := validConfig(); c.ThoughtGenerator = nil; return c }(), ErrThoughtGeneratorRequired},
		{"NilActionSelector", func() ReActAgentConfig { c := validConfig(); c.ActionSelector = nil; return c }(), ErrActionSelectorRequired},
		{"NilResponseGenerator", func() ReActAgentConfig { c := validConfig(); c.ResponseGenerator = nil; return c }(), ErrResponseGeneratorRequired},
		{"NilCycleManager", func() ReActAgentConfig { c := validConfig(); c.CycleManager = nil; return c }(), ErrCycleManagerRequired},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := NewReActAgent(tt.config)
			assert.ErrorIs(t, err, tt.expectedError)
		})
	}
}

func TestReActAgent_DefaultMaxIterations(t *testing.T) {
	config := ReActAgentConfig{
		Name:              "TestReActAgent",
		Model:             models.NewMockModel("m", "p", models.WithToolCallSupport()),
		Tools:             []tool.Tool{tools.NewCalculatorTool()},
		ThoughtGenerator:  &MockThoughtGenerator{},
		ActionSelector:    &MockActionSelector{},
		ResponseGenerator: &MockResponseGenerator{},
		CycleManager:      &MockCycleManager{},
		Memory:            memory.NewBaseMemory(),
		MaxIterations:     0,
	}
	agent, err := NewReActAgent(config)
	require.NoError(t, err)
	assert.Equal(t, 10, agent.MaxIterations())
}

func TestReActAgent_CustomSystemPrompt(t *testing.T) {
	customPrompt := "This is a custom system prompt."
	config := ReActAgentConfig{
		Name:              "TestReActAgent",
		Model:             models.NewMockModel("m", "p", models.WithToolCallSupport()),
		Tools:             []tool.Tool{tools.NewCalculatorTool()},
		ThoughtGenerator:  &MockThoughtGenerator{},
		ActionSelector:    &MockActionSelector{},
		ResponseGenerator: &MockResponseGenerator{},
		CycleManager:      &MockCycleManager{},
		Memory:            memory.NewBaseMemory(),
		SystemPrompt:      customPrompt,
	}
	agent, err := NewReActAgent(config)
	require.NoError(t, err)
	require.NotNil(t, agent.LLMAgent)
	assert.Equal(t, customPrompt, agent.LLMAgent.GetSystemPrompt())
}

func TestReActAgent_Run_Placeholder(t *testing.T) {
	mockModel := models.NewMockModel("test-react-model", "test-provider", models.WithToolCallSupport())
	mockTool := tools.NewCalculatorTool()
	mockTG := &MockThoughtGenerator{}
	mockAS := &MockActionSelector{}
	mockRG := &MockResponseGenerator{}
	mockCM := &MockCycleManager{}

	config := ReActAgentConfig{
		Name:              "TestRunAgent",
		Model:             mockModel,
		Tools:             []tool.Tool{mockTool},
		ThoughtGenerator:  mockTG,
		ActionSelector:    mockAS,
		ResponseGenerator: mockRG,
		CycleManager:      mockCM,
		Memory:            memory.NewBaseMemory(),
	}
	agent, err := NewReActAgent(config)
	require.NoError(t, err)

	// Configure the mock ThoughtGenerator to respond with a final answer
	mockTG.GenerateFunc = func(ctx context.Context, history []*message.Message, previousCycles []*Cycle) (*Thought, error) {
		return &Thought{ID: "run-thought", Content: "Final Answer: All done!"}, nil
	}

	// Configure the mock ResponseGenerator to pass through the "All done!" message
	mockRG.GenerateFunc = func(ctx context.Context, goal string, history []*message.Message, cycles []*Cycle) (*message.Message, error) {
		return message.NewAssistantMessage("All done!"), nil
	}

	ctx := context.Background()
	resp, err := agent.Run(ctx, message.NewUserMessage("Test run"))
	require.NoError(t, err)
	require.NotNil(t, resp)

	// Changed assertion to check for just the content without expecting the "Final Answer:" prefix,
	// since generateResponseFromContent might strip it
	assert.Equal(t, "All done!", resp.Content)
}

// TestDynamicErrorRecovery tests the agent's ability to handle errors using the
// enhanced error recovery mechanisms.
func TestDynamicErrorRecovery(t *testing.T) {
	// Skipping this test since it requires more setup
	t.Skip("This test is not yet ready for execution")

	// This test will be properly implemented in a future PR to test the enhanced
	// error handling mechanisms added to the dynamic reasoning capabilities.
}

// TemplateThoughtGenerator is a simple thought generator that uses templates.
type TemplateThoughtGenerator struct {
	templates map[string]string
	fallback  string
}

// NewTemplateThoughtGenerator creates a new template-based thought generator.
func NewTemplateThoughtGenerator(templates map[string]string) *TemplateThoughtGenerator {
	fallback := "I'll think about how to solve this problem. After careful consideration, I think the best approach is to respond directly. Final Answer: This is a test response."
	if templates["default"] != "" {
		fallback = templates["default"]
	}

	return &TemplateThoughtGenerator{
		templates: templates,
		fallback:  fallback,
	}
}

// Generate implements the ThoughtGenerator interface.
func (g *TemplateThoughtGenerator) Generate(ctx context.Context, messages []*message.Message, history []*Cycle) (*Thought, error) {
	// Find the last user message if any
	var msg *message.Message
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Role == message.RoleUser {
			msg = messages[i]
			break
		}
	}
	if msg == nil && len(messages) > 0 {
		msg = messages[len(messages)-1]
	}
	if msg == nil {
		return nil, fmt.Errorf("no message found for thought generation")
	}

	content := msg.Content

	// Choose the appropriate template
	template := g.fallback
	for keyword, tmpl := range g.templates {
		if keyword != "default" && strings.Contains(strings.ToLower(content), strings.ToLower(keyword)) {
			template = tmpl
			break
		}
	}

	// Generate the thought
	thought := &Thought{
		ID:        fmt.Sprintf("thought-%d", time.Now().UnixNano()),
		Content:   strings.ReplaceAll(template, "{{input}}", content),
		Type:      "template",
		Timestamp: time.Now().Unix(),
	}

	return thought, nil
}
