package graph

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"

	"trpc.group/trpc-go/trpc-agent-go/core/message"
)

func TestSequentialGraph(t *testing.T) {
	// Create nodes
	node1 := NodeFunc(func(ctx context.Context, input *message.Message) (*message.Message, error) {
		output := message.NewMessage(input.Role, input.Content+" - Node1")
		return output, nil
	}).WithInfo("node1", "First node")

	node2 := NodeFunc(func(ctx context.Context, input *message.Message) (*message.Message, error) {
		output := message.NewMessage(input.Role, input.Content+" - Node2")
		return output, nil
	}).WithInfo("node2", "Second node")

	node3 := NodeFunc(func(ctx context.Context, input *message.Message) (*message.Message, error) {
		output := message.NewMessage(input.Role, input.Content+" - Node3")
		return output, nil
	}).WithInfo("node3", "Third node")

	// Create sequential graph
	graph := Sequential("TestSequential", "A test sequential graph", node1, node2, node3)

	// Create runner
	runner, err := NewGraphRunner(graph)
	assert.NoError(t, err)

	// Execute graph
	input := message.NewUserMessage("Input")
	result, err := runner.Execute(context.Background(), input)
	assert.NoError(t, err)
	assert.Equal(t, "Input - Node1 - Node2 - Node3", result.Content)
}

func TestConditionalGraph(t *testing.T) {
	// Create nodes
	ifNode := NodeFunc(func(ctx context.Context, input *message.Message) (*message.Message, error) {
		output := message.NewMessage(input.Role, input.Content+" - If")
		return output, nil
	}).WithInfo("if", "If branch")

	elseNode := NodeFunc(func(ctx context.Context, input *message.Message) (*message.Message, error) {
		output := message.NewMessage(input.Role, input.Content+" - Else")
		return output, nil
	}).WithInfo("else", "Else branch")

	// Create condition that checks if the input content contains "if"
	condition := func(ctx context.Context, msg *message.Message) bool {
		return msg.Content == "if"
	}

	// Create conditional graph
	graph := Conditional("TestConditional", "A test conditional graph", condition, ifNode, elseNode)

	// Create runner
	runner, err := NewGraphRunner(graph)
	assert.NoError(t, err)

	// Test if branch
	input1 := message.NewUserMessage("if")
	result1, err := runner.Execute(context.Background(), input1)
	assert.NoError(t, err)
	assert.Equal(t, "if - If", result1.Content)

	// Test else branch
	input2 := message.NewUserMessage("else")
	result2, err := runner.Execute(context.Background(), input2)
	assert.NoError(t, err)
	assert.Equal(t, "else - Else", result2.Content)
}

func TestLoopGraph(t *testing.T) {
	// Create body node that increments a counter
	bodyNode := NodeFunc(func(ctx context.Context, input *message.Message) (*message.Message, error) {
		// Get counter
		counterVal, _ := input.GetMetadata("counter")
		counter, _ := counterVal.(int)

		// Increment counter
		counter++
		input.SetMetadata("counter", counter)

		output := message.NewMessage(input.Role, input.Content)
		output.SetMetadata("counter", counter)
		return output, nil
	}).WithInfo("body", "Body of the loop")

	// Create condition that loops until counter reaches 5
	condition := func(ctx context.Context, msg *message.Message) bool {
		counterVal, _ := msg.GetMetadata("counter")
		counter, _ := counterVal.(int)
		return counter < 5
	}

	// Create loop graph
	graph := Loop("TestLoop", "A test loop graph", bodyNode, condition, 10)

	// Create runner
	runner, err := NewGraphRunner(graph)
	assert.NoError(t, err)

	// Initialize input with counter at 0
	input := message.NewUserMessage("Loop test")
	input.SetMetadata("counter", 0)

	// Execute graph
	result, err := runner.Execute(context.Background(), input)
	assert.NoError(t, err)

	// Check that the counter reached 5
	counterVal, _ := result.GetMetadata("counter")
	counter, _ := counterVal.(int)
	assert.Equal(t, 5, counter)
}

func TestParallelGraph(t *testing.T) {
	// Create parallel nodes
	node1 := NodeFunc(func(ctx context.Context, input *message.Message) (*message.Message, error) {
		output := message.NewMessage(input.Role, "Node1")
		output.SetMetadata("node", "node1")
		return output, nil
	}).WithInfo("node1", "First parallel node")

	node2 := NodeFunc(func(ctx context.Context, input *message.Message) (*message.Message, error) {
		output := message.NewMessage(input.Role, "Node2")
		output.SetMetadata("node", "node2")
		return output, nil
	}).WithInfo("node2", "Second parallel node")

	// Create combiner node
	combiner := NodeFunc(func(ctx context.Context, input *message.Message) (*message.Message, error) {
		// In a real implementation, this would collect results from all parallel nodes
		output := message.NewMessage(input.Role, "Combined")
		output.SetMetadata("combined", true)
		return output, nil
	}).WithInfo("combiner", "Combines results from parallel nodes")

	// Create parallel graph
	graph := Parallel("TestParallel", "A test parallel graph", combiner, node1, node2)

	// Create runner
	runner, err := NewGraphRunner(graph)
	assert.NoError(t, err)

	// Execute graph
	input := message.NewUserMessage("Parallel test")
	result, err := runner.Execute(context.Background(), input)
	assert.NoError(t, err)

	// Check result
	assert.Equal(t, "Combined", result.Content)
	combinedVal, _ := result.GetMetadata("combined")
	combined, _ := combinedVal.(bool)
	assert.True(t, combined)
}

func TestCustomGraph(t *testing.T) {
	// Create a custom graph
	graph := NewGraph("CustomGraph", "A custom graph")

	// Add nodes
	graph.AddNode("start", NodeFunc(func(ctx context.Context, input *message.Message) (*message.Message, error) {
		output := message.NewMessage(input.Role, input.Content+" - Start")
		return output, nil
	}).WithInfo("start", "Start node"))

	graph.AddNode("middle", NodeFunc(func(ctx context.Context, input *message.Message) (*message.Message, error) {
		output := message.NewMessage(input.Role, input.Content+" - Middle")
		return output, nil
	}).WithInfo("middle", "Middle node"))

	graph.AddNode("end", NodeFunc(func(ctx context.Context, input *message.Message) (*message.Message, error) {
		output := message.NewMessage(input.Role, input.Content+" - End")
		return output, nil
	}).WithInfo("end", "End node"))

	// Connect nodes
	graph.AddEdge("start", "middle")
	graph.AddEdge("middle", "end")

	// Set start and end nodes
	graph.SetStartNode("start")
	graph.AddEndNode("end")

	// Create runner
	runner, err := NewGraphRunner(graph)
	assert.NoError(t, err)

	// Execute graph
	input := message.NewUserMessage("Custom")
	result, err := runner.Execute(context.Background(), input)
	assert.NoError(t, err)
	assert.Equal(t, "Custom - Start - Middle - End", result.Content)
}

func TestBranchGraph(t *testing.T) {
	// Create branch nodes
	branchA := NodeFunc(func(ctx context.Context, input *message.Message) (*message.Message, error) {
		output := message.NewMessage(input.Role, input.Content+" - BranchA")
		return output, nil
	}).WithInfo("branchA", "Branch A")

	branchB := NodeFunc(func(ctx context.Context, input *message.Message) (*message.Message, error) {
		output := message.NewMessage(input.Role, input.Content+" - BranchB")
		return output, nil
	}).WithInfo("branchB", "Branch B")

	// Create branch selector
	branchSelector := func(ctx context.Context, msg *message.Message) string {
		if msg.Content == "A" {
			return "A"
		}
		return "B"
	}

	// Create branch graph
	branches := map[string]Node{
		"A": branchA,
		"B": branchB,
	}
	graph := Branch("TestBranch", "A test branch graph", branchSelector, branches)

	// Create runner
	runner, err := NewGraphRunner(graph)
	assert.NoError(t, err)

	// Test branch A
	inputA := message.NewUserMessage("A")
	resultA, err := runner.Execute(context.Background(), inputA)
	assert.NoError(t, err)
	assert.Equal(t, "A - BranchA", resultA.Content)

	// Test branch B
	inputB := message.NewUserMessage("B")
	resultB, err := runner.Execute(context.Background(), inputB)
	assert.NoError(t, err)
	assert.Equal(t, "B - BranchB", resultB.Content)
}
