package react

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/message"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

// mockTestTool implements the tool.Tool interface for testing.
type mockTestTool struct {
	name        string
	description string
	parameters  map[string]interface{}
}

func newMockTestTool(name, description string) *mockTestTool {
	return &mockTestTool{
		name:        name,
		description: description,
		parameters: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"query": map[string]interface{}{
					"type":        "string",
					"description": "The query to process",
				},
			},
			"required": []string{"query"},
		},
	}
}

func (t *mockTestTool) Name() string {
	return t.name
}

func (t *mockTestTool) Description() string {
	return t.description
}

func (t *mockTestTool) Parameters() map[string]interface{} {
	return t.parameters
}

func (t *mockTestTool) Execute(_ context.Context, _ map[string]interface{}) (*tool.Result, error) {
	return &tool.Result{
		Output: map[string]interface{}{
			"result": "This is a mock result",
		},
	}, nil
}

func (t *mockTestTool) GetDefinition() *tool.ToolDefinition {
	def := tool.NewToolDefinition(t.name, t.description)

	// Handle parameters if this is an object type with properties
	if props, ok := t.parameters["properties"].(map[string]interface{}); ok {
		for propName, propDef := range props {
			if propObj, ok := propDef.(map[string]interface{}); ok {
				prop := &tool.Property{
					Type:        propObj["type"].(string),
					Description: propObj["description"].(string),
				}

				// Determine if the parameter is required
				required := false
				if reqList, ok := t.parameters["required"].([]string); ok {
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

	return def
}

// mockTestModel implements the model.Model interface for testing.
type mockTestModel struct {
	responseJSON string
}

func (m *mockTestModel) Name() string {
	return "test-model"
}

func (m *mockTestModel) Provider() string {
	return "test-provider"
}

func (m *mockTestModel) Generate(_ context.Context, _ string, _ model.GenerationOptions) (*model.Response, error) {
	return &model.Response{
		Text: m.responseJSON,
	}, nil
}

func (m *mockTestModel) GenerateWithMessages(_ context.Context, _ []*message.Message, _ model.GenerationOptions) (*model.Response, error) {
	return &model.Response{
		Text: m.responseJSON,
		Messages: []*message.Message{
			message.NewAssistantMessage(m.responseJSON),
		},
	}, nil
}

func (m *mockTestModel) SetTools(_ []model.ToolDefinition) error {
	return nil
}

func (m *mockTestModel) GetDefaultOptions() model.GenerationOptions {
	return model.GenerationOptions{
		Temperature: 0.7,
		MaxTokens:   1000,
	}
}

func (m *mockTestModel) SupportsToolCalls() bool {
	return true
}

// mockWorkingMemory is a simple mock implementation of ReactWorkingMemory.
type mockWorkingMemory struct {
	*BaseReactMemory
	items []*WorkingMemoryItem
}

func newMockWorkingMemory() *mockWorkingMemory {
	return &mockWorkingMemory{
		BaseReactMemory: NewBaseReactMemory(),
		items:           []*WorkingMemoryItem{},
	}
}

func (m *mockWorkingMemory) StoreItem(_ context.Context, item *WorkingMemoryItem) error {
	m.items = append(m.items, item)
	return nil
}

func (m *mockWorkingMemory) RetrieveItem(_ context.Context, _ string) (*WorkingMemoryItem, error) {
	return nil, nil
}

func (m *mockWorkingMemory) RetrieveItemsByType(_ context.Context, _ string) ([]*WorkingMemoryItem, error) {
	return nil, nil
}

func (m *mockWorkingMemory) RetrieveItemsByName(_ context.Context, _ string) ([]*WorkingMemoryItem, error) {
	return nil, nil
}

func (m *mockWorkingMemory) SearchItems(_ context.Context, _ string) ([]*WorkingMemoryItem, error) {
	return nil, nil
}

func (m *mockWorkingMemory) ListItems(_ context.Context, _ map[string]interface{}, _ string) ([]*WorkingMemoryItem, error) {
	return nil, nil
}

func (m *mockWorkingMemory) RemoveItem(_ context.Context, _ string) error {
	return nil
}

func (m *mockWorkingMemory) RelateItems(_ context.Context, _, _ string) error {
	return nil
}

func (m *mockWorkingMemory) GetContext(_ context.Context) string {
	return "Mock working memory context"
}

// TestContextAwareToolSelector_Select tests the Select method.
func TestContextAwareToolSelector_Select(t *testing.T) {
	// Setup
	testTools := []tool.Tool{
		newMockTestTool("search", "Search for information"),
		newMockTestTool("calculator", "Perform calculations"),
	}

	testModel := &mockTestModel{
		responseJSON: `{"tool_name":"search","tool_input":{"query":"test"},"explanation":"This is the most relevant tool"}`,
	}

	testMemory := newMockWorkingMemory()

	selector := NewContextAwareToolSelector(ContextAwareToolSelectorConfig{
		Model:             testModel,
		ContextWindow:     2,
		SelectionStrategy: SelectMostRelevant,
		Memory:            testMemory,
	})

	// Test basic selection
	thought := &Thought{
		ID:        "thought-1",
		Content:   "I need to search for information about tests",
		Timestamp: time.Now().Unix(),
	}

	action, err := selector.Select(context.Background(), thought, testTools)

	// Assertions
	require.NoError(t, err)
	assert.NotNil(t, action)
	assert.Equal(t, "search", action.ToolName)
	assert.Equal(t, thought.ID, action.ThoughtID)
	assert.NotEmpty(t, action.ID)
	assert.NotEmpty(t, action.Timestamp)

	// Test with working memory
	assert.Equal(t, 1, len(testMemory.items))
	assert.Equal(t, "tool_selection", testMemory.items[0].Type)
	assert.Contains(t, testMemory.items[0].Name, "search")

	// Test error cases
	_, err = selector.Select(context.Background(), nil, testTools)
	assert.Error(t, err)

	_, err = selector.Select(context.Background(), thought, []tool.Tool{})
	assert.Error(t, err)
}

// TestContextAwareToolSelector_WithCycleContext tests with cycle context.
func TestContextAwareToolSelector_WithCycleContext(t *testing.T) {
	// Setup
	testTools := []tool.Tool{
		newMockTestTool("search", "Search for information"),
		newMockTestTool("calculator", "Perform calculations"),
	}

	testModel := &mockTestModel{
		responseJSON: `{"tool_name":"calculator","tool_input":{"query":"calculate"},"explanation":"Based on previous context, calculation is needed"}`,
	}

	selector := NewContextAwareToolSelector(ContextAwareToolSelectorConfig{
		Model:             testModel,
		ContextWindow:     2,
		SelectionStrategy: RankByRelevance,
	})

	// Create mock cycle manager
	cycleManager := NewInMemoryCycleManager()

	// Create a previous cycle
	previousThought := &Thought{
		ID:        "thought-1",
		Content:   "I need to search for information",
		Timestamp: time.Now().Unix() - 100,
	}

	previousAction := &Action{
		ID:        "action-1",
		ThoughtID: "thought-1",
		ToolName:  "search",
		ToolInput: map[string]interface{}{"query": "test search"},
		Timestamp: time.Now().Unix() - 90,
	}

	previousObservation := &CycleObservation{
		ID:         "obs-1",
		ActionID:   "action-1",
		ToolOutput: map[string]interface{}{"result": "Found information about calculations"},
		Timestamp:  time.Now().Unix() - 80,
	}

	// Add the cycle properly using the cycle manager interface
	err := cycleManager.StartCycle(context.Background(), previousThought)
	require.NoError(t, err)

	err = cycleManager.RecordActions(context.Background(), []*Action{previousAction})
	require.NoError(t, err)

	err = cycleManager.RecordObservations(context.Background(), []*CycleObservation{previousObservation})
	require.NoError(t, err)

	_, err = cycleManager.EndCycle(context.Background())
	require.NoError(t, err)

	// Create context with cycle manager
	ctx := context.WithValue(context.Background(), "cycleManager", cycleManager)

	// Test
	thought := &Thought{
		ID:        "thought-2",
		Content:   "Now I need to calculate something",
		Timestamp: time.Now().Unix(),
	}

	action, err := selector.Select(ctx, thought, testTools)

	// Assertions
	require.NoError(t, err)
	assert.NotNil(t, action)
	assert.Equal(t, "calculator", action.ToolName)
	assert.Equal(t, thought.ID, action.ThoughtID)
}
