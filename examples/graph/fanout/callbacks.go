package main

import (
	"context"
	"fmt"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/graph"
)

// createNodeCallbacks creates comprehensive callbacks for monitoring and performance tracking.
func (w *fanoutWorkflow) createNodeCallbacks() *graph.NodeCallbacks {
	callbacks := graph.NewNodeCallbacks()
	callbacks.RegisterBeforeNode(w.onBeforeNode)
	callbacks.RegisterAfterNode(w.onAfterNode)
	callbacks.RegisterOnNodeError(w.onNodeError)
	return callbacks
}

// onBeforeNode records timing and execution metadata.
func (w *fanoutWorkflow) onBeforeNode(ctx context.Context, callbackCtx *graph.NodeCallbackContext, state graph.State) (any, error) {
	if state["node_timings"] == nil {
		state["node_timings"] = make(map[string]time.Time)
	}
	timings := state["node_timings"].(map[string]time.Time)
	timings[callbackCtx.NodeID] = time.Now()

	if state["node_execution_history"] == nil {
		state["node_execution_history"] = make([]map[string]any, 0)
	}
	history := state["node_execution_history"].([]map[string]any)
	history = append(history, map[string]any{
		"node_id":       callbackCtx.NodeID,
		"node_name":     callbackCtx.NodeName,
		"node_type":     callbackCtx.NodeType,
		"step_number":   callbackCtx.StepNumber,
		"start_time":    time.Now(),
		"invocation_id": callbackCtx.InvocationID,
	})
	state["node_execution_history"] = history
	return nil, nil
}

// onAfterNode updates history, adds metadata, and appends task results.
func (w *fanoutWorkflow) onAfterNode(
	ctx context.Context,
	callbackCtx *graph.NodeCallbackContext,
	state graph.State,
	result any,
	nodeErr error,
) (any, error) {
	executionTime := w.computeExecutionTime(state, callbackCtx.NodeID)
	w.updateLastHistory(state, executionTime, nodeErr)
	w.maybeWarnSlow(callbackCtx.NodeName, executionTime)

	// Append result for process_task when success and expected fields exist.
	if nodeErr == nil && callbackCtx.NodeID == "process_task" {
		if _, taskOK := state["task_id"].(string); taskOK {
			if _, priorityOK := state["priority"].(string); priorityOK {
				msg := w.buildTaskResultString(state)
				return graph.State{"results": []string{msg}}, nil
			}
		}
	}

	// Enrich state result with execution metadata if applicable.
	if result != nil && nodeErr == nil {
		if sr, ok := result.(graph.State); ok {
			sr["last_executed_node"] = callbackCtx.NodeID
			sr["last_execution_time"] = executionTime
			if hist, ok := state["node_execution_history"].([]map[string]any); ok {
				sr["total_nodes_executed"] = len(hist)
			}
			return sr, nil
		}
	}
	return result, nil
}

// onNodeError records error info and lightweight classification.
func (w *fanoutWorkflow) onNodeError(
	ctx context.Context,
	callbackCtx *graph.NodeCallbackContext,
	state graph.State,
	err error,
) {
	fmt.Printf("❌ [CALLBACK] Error in node: %s (%s) at step %d\n",
		callbackCtx.NodeName, callbackCtx.NodeType, callbackCtx.StepNumber)
	fmt.Printf("   Error details: %v\n", err)

	if state["error_count"] == nil {
		state["error_count"] = 0
	}
	errorCount := state["error_count"].(int)
	state["error_count"] = errorCount + 1

	if history, ok := state["node_execution_history"].([]map[string]any); ok && len(history) > 0 {
		lastEntry := history[len(history)-1]
		lastEntry["end_time"] = time.Now()
		lastEntry["success"] = false
		lastEntry["error"] = err.Error()
	}

	switch callbackCtx.NodeType {
	case graph.NodeTypeLLM:
		fmt.Printf("   🤖 LLM node error - this might be a model API issue\n")
	case graph.NodeTypeTool:
		fmt.Printf("   🔧 Tool execution error - check tool implementation\n")
	case graph.NodeTypeFunction:
		fmt.Printf("   ⚙️  Function node error - check business logic\n")
	}

	if state["error_context"] == nil {
		state["error_context"] = make([]map[string]any, 0)
	}
	ec := state["error_context"].([]map[string]any)
	ec = append(ec, map[string]any{
		"node_id":     callbackCtx.NodeID,
		"node_name":   callbackCtx.NodeName,
		"step_number": callbackCtx.StepNumber,
		"error":       err.Error(),
		"timestamp":   time.Now(),
	})
	state["error_context"] = ec
}

// helpers
func (w *fanoutWorkflow) computeExecutionTime(state graph.State, nodeID string) time.Duration {
	if timings, ok := state["node_timings"].(map[string]time.Time); ok {
		if startTime, exists := timings[nodeID]; exists {
			return time.Since(startTime)
		}
	}
	return 0
}

func (w *fanoutWorkflow) updateLastHistory(state graph.State, dur time.Duration, nodeErr error) {
	if history, ok := state["node_execution_history"].([]map[string]any); ok && len(history) > 0 {
		last := history[len(history)-1]
		last["end_time"] = time.Now()
		last["execution_time"] = dur
		last["success"] = nodeErr == nil
		if nodeErr != nil {
			last["error"] = nodeErr.Error()
		}
	}
}

func (w *fanoutWorkflow) maybeWarnSlow(nodeName string, dur time.Duration) {
	if dur > 25*time.Second {
		fmt.Printf("⚠️  [CALLBACK] Performance alert: Node %s took %v to execute\n", nodeName, dur)
	}
}

func (w *fanoutWorkflow) buildTaskResultString(state graph.State) string {
	var taskID, priority string
	if v, ok := state["task_id"].(string); ok {
		taskID = v
	}
	if v, ok := state["priority"].(string); ok {
		priority = v
	}
	return fmt.Sprintf("%s (priority: %s)", taskID, priority)
}
