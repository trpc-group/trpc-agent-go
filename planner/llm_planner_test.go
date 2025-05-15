package planner

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"

	"trpc.group/trpc-go/trpc-agent-go/message"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/model/models"
)

func TestNewLLMPlanner(t *testing.T) {
	// Create a mock model
	mockModel := models.NewMockModel("test-model", "test-provider")

	// Test successful creation
	config := LLMPlannerConfig{
		Name:        "TestPlanner",
		Description: "Test description",
		Model:       mockModel,
	}

	planner, err := NewLLMPlanner(config)
	assert.NoError(t, err)
	assert.NotNil(t, planner)
	assert.Equal(t, "TestPlanner", planner.Name())
	assert.Equal(t, "Test description", planner.Description())
	assert.Equal(t, mockModel, planner.GetModel())

	// Test creation with nil model
	config.Model = nil
	planner, err = NewLLMPlanner(config)
	assert.Error(t, err)
	assert.Equal(t, ErrNoModel, err)
	assert.Nil(t, planner)
}

func TestDefaultLLMPlannerConfig(t *testing.T) {
	config := DefaultLLMPlannerConfig()

	assert.Equal(t, "LLMPlanner", config.Name)
	assert.Equal(t, "A planner that uses language models to generate plans", config.Description)
	assert.Equal(t, float32(0.2), config.Temperature)
	assert.Equal(t, 2048, config.MaxTokens)
	assert.NotEmpty(t, config.SystemPrompt)
}

// MockPlanGenerator is a test implementation of PlanGenerator.
func MockPlanGenerator(ctx context.Context, goal string, options map[string]interface{}) (PlanDescription, error) {
	return PlanDescription{
		Goal: goal,
		Tasks: []TaskDescription{
			{
				ID:          "task-1",
				Description: "First task",
				Priority:    1,
				Steps: []StepDescription{
					{
						ID:          "step-1",
						Description: "First step",
						Action:      "test_action",
						Parameters: map[string]interface{}{
							"param1": "value1",
						},
					},
				},
			},
			{
				ID:           "task-2",
				Description:  "Second task",
				Dependencies: []string{"task-1"},
				Priority:     2,
				Steps: []StepDescription{
					{
						ID:          "step-2",
						Description: "Second step",
						Action:      "test_action",
						Parameters: map[string]interface{}{
							"param2": "value2",
						},
					},
				},
			},
		},
	}, nil
}

// ErrorPlanGenerator always returns an error.
func ErrorPlanGenerator(ctx context.Context, goal string, options map[string]interface{}) (PlanDescription, error) {
	return PlanDescription{}, errors.New("plan generation failed")
}

func TestLLMPlanner_CreatePlan(t *testing.T) {
	// Create a mock model that returns a valid plan
	mockModel := models.NewMockModel("test-model", "test-provider")

	config := LLMPlannerConfig{
		Name:        "TestPlanner",
		Description: "Test description",
		Model:       mockModel,
	}

	planner, err := NewLLMPlanner(config)
	assert.NoError(t, err)

	// Set a custom plan generator for testing
	planner.SetPlanGenerator(MockPlanGenerator)

	// Test creating a plan
	ctx := context.Background()
	plan, err := planner.CreatePlan(ctx, "Test goal", nil)
	assert.NoError(t, err)
	assert.NotNil(t, plan)
	assert.Equal(t, "Test goal", plan.Goal())

	// Verify tasks were created correctly
	tasks := plan.Tasks()
	assert.Len(t, tasks, 2)

	task1, exists := plan.GetTask("task-1")
	assert.True(t, exists)
	assert.Equal(t, "First task", task1.Description())
	assert.Equal(t, 1, task1.Priority())
	assert.Empty(t, task1.Dependencies())

	task2, exists := plan.GetTask("task-2")
	assert.True(t, exists)
	assert.Equal(t, "Second task", task2.Description())
	assert.Equal(t, 2, task2.Priority())
	assert.Equal(t, []string{"task-1"}, task2.Dependencies())

	// Verify steps were created correctly
	steps1 := task1.Steps()
	assert.Len(t, steps1, 1)
	assert.Equal(t, "step-1", steps1[0].ID())
	assert.Equal(t, "First step", steps1[0].Description())
	assert.Equal(t, "test_action", steps1[0].Action())
	assert.Equal(t, "value1", steps1[0].Parameters()["param1"])

	steps2 := task2.Steps()
	assert.Len(t, steps2, 1)
	assert.Equal(t, "step-2", steps2[0].ID())
	assert.Equal(t, "Second step", steps2[0].Description())
	assert.Equal(t, "test_action", steps2[0].Action())
	assert.Equal(t, "value2", steps2[0].Parameters()["param2"])

	// Test with empty goal
	plan, err = planner.CreatePlan(ctx, "", nil)
	assert.Error(t, err)
	assert.Equal(t, ErrInvalidGoal, err)
	assert.Nil(t, plan)

	// Test with a generator that fails
	planner.SetPlanGenerator(ErrorPlanGenerator)
	plan, err = planner.CreatePlan(ctx, "Test goal", nil)
	assert.Error(t, err)
	assert.True(t, errors.Is(err, ErrPlanGenerationFailed))
	assert.Nil(t, plan)
}

func TestLLMPlanner_defaultPlanGenerator(t *testing.T) {
	// Create a mock model that returns a specific response
	mockModel := &MockLLMWithResponse{
		Response: &model.Response{
			Messages: []*message.Message{
				message.NewAssistantMessage(`Here's a plan:
{
  "goal": "Test goal",
  "tasks": [
    {
      "id": "task-1",
      "description": "First task",
      "priority": 1,
      "steps": [
        {
          "id": "step-1",
          "description": "First step",
          "action": "test_action",
          "parameters": {
            "param1": "value1"
          }
        }
      ]
    }
  ]
}`),
			},
		},
	}

	config := LLMPlannerConfig{
		Name:        "TestPlanner",
		Description: "Test description",
		Model:       mockModel,
	}

	planner, err := NewLLMPlanner(config)
	assert.NoError(t, err)

	// Test the default plan generator
	ctx := context.Background()
	planDesc, err := planner.defaultPlanGenerator(ctx, "Test goal", nil)
	assert.NoError(t, err)
	assert.Equal(t, "Test goal", planDesc.Goal)
	assert.Len(t, planDesc.Tasks, 1)
	assert.Equal(t, "task-1", planDesc.Tasks[0].ID)
	assert.Equal(t, "First task", planDesc.Tasks[0].Description)
	assert.Equal(t, 1, planDesc.Tasks[0].Priority)
	assert.Len(t, planDesc.Tasks[0].Steps, 1)
	assert.Equal(t, "step-1", planDesc.Tasks[0].Steps[0].ID)
	assert.Equal(t, "First step", planDesc.Tasks[0].Steps[0].Description)
	assert.Equal(t, "test_action", planDesc.Tasks[0].Steps[0].Action)
	assert.Equal(t, "value1", planDesc.Tasks[0].Steps[0].Parameters["param1"])

	// Test with invalid JSON response
	mockModel.Response = &model.Response{
		Messages: []*message.Message{
			message.NewAssistantMessage(`Not a valid JSON`),
		},
	}
	planDesc, err = planner.defaultPlanGenerator(ctx, "Test goal", nil)
	assert.Error(t, err)
	assert.True(t, errors.Is(err, ErrInvalidPlanFormat))

	// Test with response that fails to parse as JSON
	mockModel.Response = &model.Response{
		Messages: []*message.Message{
			message.NewAssistantMessage(`{
  "goal": "Test goal",
  "tasks": [
    {
      "invalid-json
    }
  ]
}`),
		},
	}
	planDesc, err = planner.defaultPlanGenerator(ctx, "Test goal", nil)
	assert.Error(t, err)
	assert.True(t, errors.Is(err, ErrInvalidPlanFormat))

	// Test with model error
	mockModel.Error = errors.New("model error")
	planDesc, err = planner.defaultPlanGenerator(ctx, "Test goal", nil)
	assert.Error(t, err)
	assert.Equal(t, "model error", err.Error())
}

// MockLLMWithResponse is a test implementation of model.Model.
type MockLLMWithResponse struct {
	Response *model.Response
	Error    error
}

func (m *MockLLMWithResponse) Name() string {
	return "mock-model"
}

func (m *MockLLMWithResponse) Provider() string {
	return "mock-provider"
}

func (m *MockLLMWithResponse) Generate(ctx context.Context, prompt string, options model.GenerationOptions) (*model.Response, error) {
	if m.Error != nil {
		return nil, m.Error
	}
	return m.Response, nil
}

func (m *MockLLMWithResponse) GenerateWithMessages(ctx context.Context, messages []*message.Message, options model.GenerationOptions) (*model.Response, error) {
	if m.Error != nil {
		return nil, m.Error
	}
	return m.Response, nil
}
