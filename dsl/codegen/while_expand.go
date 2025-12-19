package codegen

import (
	"encoding/json"
	"fmt"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/dsl"
)

type whileBodyConfig struct {
	Nodes            []dsl.Node            `json:"nodes"`
	Edges            []dsl.Edge            `json:"edges"`
	ConditionalEdges []dsl.ConditionalEdge `json:"conditional_edges"`
	StartNodeID      string                `json:"start_node_id"`
	ExitNodeID       string                `json:"exit_node_id"`
}

type whileConfig struct {
	Body      whileBodyConfig `json:"body"`
	Condition dsl.Expression  `json:"condition"`
}

type whileExpansion struct {
	WhileID string

	BodyEntry string
	BodyExit  string
	AfterNode string

	Condition dsl.Expression

	BodyNodes            []dsl.Node
	BodyEdges            []dsl.Edge
	BodyConditionalEdges []dsl.ConditionalEdge
}

// expandWhileNodes flattens builtin.while nodes into their body nodes/edges and
// returns an expanded graph that contains no builtin.while nodes. It also
// appends a synthetic ConditionalEdge per while loop that implements the loop
// back-edge (continue) / exit (break) behavior.
func expandWhileNodes(graphDef *dsl.Graph) (*dsl.Graph, []whileExpansion, error) {
	if graphDef == nil {
		return nil, nil, fmt.Errorf("graph is nil")
	}

	// Index existing node IDs so we can detect conflicts with while bodies.
	nodeIDs := make(map[string]bool, len(graphDef.Nodes))
	whileByID := make(map[string]whileExpansion)
	for _, n := range graphDef.Nodes {
		if strings.TrimSpace(n.ID) != "" {
			nodeIDs[n.ID] = true
		}
	}

	var expansions []whileExpansion

	for _, n := range graphDef.Nodes {
		if n.EngineNode.NodeType != "builtin.while" {
			continue
		}

		exp, err := buildWhileExpansion(n, graphDef.Edges, nodeIDs)
		if err != nil {
			return nil, nil, err
		}
		expansions = append(expansions, exp)
		whileByID[n.ID] = exp

		// Reserve body node IDs globally so subsequent while nodes cannot reuse them.
		for _, bodyNode := range exp.BodyNodes {
			nodeIDs[bodyNode.ID] = true
		}
	}

	if len(expansions) == 0 {
		return graphDef, nil, nil
	}

	expanded := *graphDef
	expanded.Nodes = nil
	expanded.Edges = nil
	expanded.ConditionalEdges = nil

	// Copy all non-while nodes.
	for _, n := range graphDef.Nodes {
		if n.EngineNode.NodeType == "builtin.while" {
			continue
		}
		expanded.Nodes = append(expanded.Nodes, n)
	}
	// Append all while body nodes.
	for _, exp := range expansions {
		expanded.Nodes = append(expanded.Nodes, exp.BodyNodes...)
	}

	// Rewire edges:
	//   - edge targeting while node -> edge to body entry
	//   - edge originating from while node -> omitted (replaced by cond edge)
	//   - all other edges are kept as-is
	for _, e := range graphDef.Edges {
		if exp, ok := whileByID[e.Target]; ok {
			newEdge := e
			newEdge.Target = exp.BodyEntry
			expanded.Edges = append(expanded.Edges, newEdge)
			continue
		}
		if _, ok := whileByID[e.Source]; ok {
			continue
		}
		expanded.Edges = append(expanded.Edges, e)
	}

	// Add internal edges from while bodies.
	for _, exp := range expansions {
		expanded.Edges = append(expanded.Edges, exp.BodyEdges...)
	}

	// Rewire conditional edges:
	//   - conditional edge originating from while node -> omitted (while has its own exit logic)
	//   - case targets pointing to while node -> rewrite to body entry
	//   - default pointing to while node -> rewrite to body entry
	for _, ce := range graphDef.ConditionalEdges {
		if _, ok := whileByID[ce.From]; ok {
			// Skip conditional edges originating from while nodes; the while loop's
			// exit logic is handled by the synthetic conditional edge generated below.
			continue
		}
		newCE := ce
		newCE.Condition.Cases = nil
		for _, c := range ce.Condition.Cases {
			newCase := c
			if exp, ok := whileByID[c.Target]; ok {
				newCase.Target = exp.BodyEntry
			}
			newCE.Condition.Cases = append(newCE.Condition.Cases, newCase)
		}
		if exp, ok := whileByID[ce.Condition.Default]; ok {
			newCE.Condition.Default = exp.BodyEntry
		}
		expanded.ConditionalEdges = append(expanded.ConditionalEdges, newCE)
	}

	// Append conditional edges from while bodies.
	for _, exp := range expansions {
		if len(exp.BodyConditionalEdges) == 0 {
			continue
		}
		expanded.ConditionalEdges = append(expanded.ConditionalEdges, exp.BodyConditionalEdges...)
	}

	// Append a synthetic conditional edge for each while loop. It is appended
	// last so that it matches the compiler's behavior (it overrides any
	// existing conditional edge from body_exit).
	for _, exp := range expansions {
		cond := dsl.Condition{
			Cases: []dsl.Case{
				{
					Name:      "while_continue",
					Predicate: exp.Condition,
					Target:    exp.BodyEntry,
				},
			},
			Default: exp.AfterNode,
		}
		expanded.ConditionalEdges = append(expanded.ConditionalEdges, dsl.ConditionalEdge{
			ID:        "while_" + exp.WhileID,
			From:      exp.BodyExit,
			Condition: cond,
			Label:     "while",
		})
	}

	return &expanded, expansions, nil
}

func buildWhileExpansion(whileNode dsl.Node, edges []dsl.Edge, reservedIDs map[string]bool) (whileExpansion, error) {
	var exp whileExpansion
	exp.WhileID = whileNode.ID

	rawCfg := whileNode.EngineNode.Config
	if rawCfg == nil {
		return exp, fmt.Errorf("while node %s: config is required", whileNode.ID)
	}

	data, err := json.Marshal(rawCfg)
	if err != nil {
		return exp, fmt.Errorf("while node %s: marshal config: %w", whileNode.ID, err)
	}
	var cfg whileConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		return exp, fmt.Errorf("while node %s: unmarshal config: %w", whileNode.ID, err)
	}

	body := cfg.Body
	if len(body.Nodes) == 0 {
		return exp, fmt.Errorf("while node %s: body.nodes must contain at least one node", whileNode.ID)
	}
	if strings.TrimSpace(body.StartNodeID) == "" {
		return exp, fmt.Errorf("while node %s: body.start_node_id is required", whileNode.ID)
	}
	if strings.TrimSpace(body.ExitNodeID) == "" {
		return exp, fmt.Errorf("while node %s: body.exit_node_id is required", whileNode.ID)
	}

	// Ensure body node IDs are unique and do not conflict with existing nodes.
	bodyNodeIDs := make(map[string]bool, len(body.Nodes))
	for _, n := range body.Nodes {
		id := strings.TrimSpace(n.ID)
		if id == "" {
			return exp, fmt.Errorf("while node %s: body node has empty id", whileNode.ID)
		}
		if bodyNodeIDs[id] {
			return exp, fmt.Errorf("while node %s: duplicate node id %q in body", whileNode.ID, id)
		}
		if reservedIDs != nil && reservedIDs[id] {
			return exp, fmt.Errorf("while node %s: body node id %q conflicts with an existing node id", whileNode.ID, id)
		}
		bodyNodeIDs[id] = true
	}
	if !bodyNodeIDs[body.StartNodeID] {
		return exp, fmt.Errorf("while node %s: body.start_node_id %q not found in body.nodes", whileNode.ID, body.StartNodeID)
	}
	if !bodyNodeIDs[body.ExitNodeID] {
		return exp, fmt.Errorf("while node %s: body.exit_node_id %q not found in body.nodes", whileNode.ID, body.ExitNodeID)
	}

	// Validate body edges reference only body nodes.
	for _, e := range body.Edges {
		if strings.TrimSpace(e.Source) == "" || strings.TrimSpace(e.Target) == "" {
			return exp, fmt.Errorf("while node %s: body edge has empty source or target", whileNode.ID)
		}
		if !bodyNodeIDs[e.Source] {
			return exp, fmt.Errorf("while node %s: body edge source %q is not a body node", whileNode.ID, e.Source)
		}
		if !bodyNodeIDs[e.Target] {
			return exp, fmt.Errorf("while node %s: body edge target %q is not a body node", whileNode.ID, e.Target)
		}
	}

	// Validate body conditional edges originate from body nodes.
	for _, ce := range body.ConditionalEdges {
		if strings.TrimSpace(ce.From) == "" {
			return exp, fmt.Errorf("while node %s: body conditional edge has empty from", whileNode.ID)
		}
		if !bodyNodeIDs[ce.From] {
			return exp, fmt.Errorf("while node %s: body conditional edge from %q is not a body node", whileNode.ID, ce.From)
		}
	}

	// Resolve the single "after" node from outgoing edges of the while node.
	var afterNode string
	for _, e := range edges {
		if e.Source != whileNode.ID {
			continue
		}
		if afterNode == "" {
			afterNode = e.Target
		} else if afterNode != e.Target {
			return exp, fmt.Errorf("while node %s: multiple outgoing edges (%s, %s)", whileNode.ID, afterNode, e.Target)
		}
	}
	if strings.TrimSpace(afterNode) == "" {
		return exp, fmt.Errorf("while node %s: must have exactly one outgoing edge", whileNode.ID)
	}

	condExpr := strings.TrimSpace(cfg.Condition.Expression)
	if condExpr == "" {
		return exp, fmt.Errorf("while node %s: condition.expression is required", whileNode.ID)
	}
	if strings.TrimSpace(cfg.Condition.Format) != "" && strings.TrimSpace(cfg.Condition.Format) != "cel" {
		return exp, fmt.Errorf("while node %s: unsupported condition.format %q (expected \"cel\")", whileNode.ID, cfg.Condition.Format)
	}

	exp.BodyEntry = body.StartNodeID
	exp.BodyExit = body.ExitNodeID
	exp.AfterNode = afterNode
	exp.Condition = cfg.Condition
	exp.BodyNodes = body.Nodes
	exp.BodyEdges = body.Edges
	exp.BodyConditionalEdges = body.ConditionalEdges

	return exp, nil
}
