package compiler

import "trpc.group/trpc-go/trpc-agent-go/graph"

// buildNodeInputView constructs the "input" object exposed to CEL expressions
// for conditional routing and while conditions. It mirrors the structured
// per-node cache stored under state["node_structured"][nodeID], allowing
// expressions such as input.output_parsed.classification.
func buildNodeInputView(state graph.State, nodeID string) map[string]any {
	input := map[string]any{}
	if nodeID == "" {
		return input
	}

	raw, ok := state["node_structured"]
	if !ok {
		return input
	}

	ns, ok := raw.(map[string]any)
	if !ok {
		return input
	}

	nodeRaw, ok := ns[nodeID]
	if !ok {
		return input
	}

	nodeMap, ok := nodeRaw.(map[string]any)
	if !ok {
		return input
	}

	for k, v := range nodeMap {
		input[k] = v
	}

	return input
}

