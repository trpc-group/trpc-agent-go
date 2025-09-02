package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/graph"
	"trpc.group/trpc-go/trpc-agent-go/graph/checkpoint/inmemory"
)

// Define a simple approval workflow.
func main() {
	// Create an in-memory checkpoint saver.
	saver := inmemory.NewSaver()
	defer saver.Close()

	// Create the workflow state graph.
	sg := graph.NewStateGraph(nil)

	// Add nodes.
	sg.AddNode("start", startNode)
	sg.AddNode("process", processNode)
	sg.AddNode("approval", approvalNode)
	sg.AddNode("complete", completeNode)

	// Add edges.
	sg.AddEdge("start", "process")
	sg.AddEdge("process", "approval")
	sg.AddEdge("approval", "complete")

	// Set the entry point.
	sg.SetEntryPoint("start")

	// Compile the graph.
	g := sg.MustCompile()

	// Create the executor.
	exec, err := graph.NewExecutor(g, graph.WithCheckpointSaver(saver))
	if err != nil {
		log.Fatalf("Failed to create executor: %v", err)
	}

	fmt.Println("🚀 Starting Suspend/Resume demo...")

	// First run - will suspend.
	runWithSuspend(exec, saver)

	// Wait a bit.
	time.Sleep(2 * time.Second)

	// Resume execution.
	resumeFromSuspend(exec, saver)
}

// startNode is the entry node.
func startNode(ctx context.Context, state graph.State) (any, error) {
	fmt.Println("📍 Start node: initializing workflow")
	state["message"] = "Hello from start node"
	state["step"] = 1
	return state, nil
}

// processNode handles business logic.
func processNode(ctx context.Context, state graph.State) (any, error) {
	fmt.Println("⚙️ Process node: handling business logic")
	state["processed"] = true
	state["step"] = 2
	state["timestamp"] = time.Now().Unix()
	return state, nil
}

// approvalNode suspends for manual approval.
func approvalNode(ctx context.Context, state graph.State) (any, error) {
	fmt.Println("⏸️ Approval node: checking if manual approval is needed")

	// Check if there is a resume value.
	if approved, exists := graph.ResumeValue[bool](ctx, state, "approval"); exists {
		fmt.Printf("✅ Detected resume value: %v\n", approved)
		if approved {
			state["approved"] = true
			state["step"] = 3
			state["approval_time"] = time.Now().Unix()
			return state, nil
		} else {
			state["approved"] = false
			state["step"] = 3
			state["rejection_reason"] = "User rejected"
			return state, nil
		}
	}

	// No resume value, need to suspend for approval.
	fmt.Println("⏳ Waiting for manual approval...")
	prompt := "Please approve this workflow (enter 'yes' or 'no'):"

	// Use Suspend helper to interrupt execution.
	_, err := graph.Suspend(ctx, state, "approval", prompt)
	if err != nil {
		return nil, err
	}

	// This will not be reached, as Suspend will interrupt.
	return state, nil
}

// completeNode marks workflow as complete.
func completeNode(ctx context.Context, state graph.State) (any, error) {
	fmt.Println("🎉 Complete node: workflow finished")
	state["completed"] = true
	state["step"] = 4
	state["completion_time"] = time.Now().Unix()

	// Print final state.
	fmt.Println("\n📊 Final state:")
	for key, value := range state {
		fmt.Printf("  %s: %v\n", key, value)
	}

	return state, nil
}

// runWithSuspend runs the workflow until it suspends.
func runWithSuspend(exec *graph.Executor, saver graph.CheckpointSaver) {
	fmt.Println("\n🔄 First run - will suspend for approval...")

	// Create initial state.
	initialState := graph.State{
		"workflow_id": "demo_001",
		"start_time":  time.Now().Unix(),
	}

	// Create invocation.
	inv := &agent.Invocation{
		InvocationID: "inv_001",
	}

	// Execute the workflow.
	events, err := exec.Execute(context.Background(), initialState, inv)
	if err != nil {
		log.Printf("Execution failed: %v", err)
		return
	}

	// Listen for events.
	for event := range events {
		if event.Response.Done {
			fmt.Println("✅ Workflow completed")
			break
		}

		// Check for interrupt event.
		if event.StateDelta != nil {
			if metadata, exists := event.StateDelta["_pregel_metadata"]; exists {
				var pregelMeta map[string]any
				if err := json.Unmarshal(metadata, &pregelMeta); err == nil {
					if source, ok := pregelMeta["source"].(string); ok && source == "interrupt" {
						fmt.Printf("🛑 Detected interrupt event: %s\n", source)
						if nodeID, ok := pregelMeta["node_id"].(string); ok {
							fmt.Printf("📍 Interrupted at node: %s\n", nodeID)
						}
					}
				}
			}
		}
	}
}

// resumeFromSuspend resumes the workflow from the suspend point.
func resumeFromSuspend(exec *graph.Executor, saver graph.CheckpointSaver) {
	fmt.Println("\n🔄 Resuming - simulating manual approval...")

	// Create resume command.
	cmd := &graph.Command{
		ResumeMap: map[string]any{
			"approval": true, // Simulate user approval.
		},
	}

	// Create initial state with resume command.
	initialState := graph.State{
		"workflow_id": "demo_001",
		"start_time":  time.Now().Unix(),
		"__command__": cmd,
	}

	// Create invocation.
	inv := &agent.Invocation{
		InvocationID: "inv_001",
	}

	// Execute the workflow.
	events, err := exec.Execute(context.Background(), initialState, inv)
	if err != nil {
		log.Printf("Resume execution failed: %v", err)
		return
	}

	// Listen for events.
	for event := range events {
		if event.Response.Done {
			fmt.Println("✅ Resume completed")
			break
		}
	}
}
