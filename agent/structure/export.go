//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package structure

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"reflect"
	"sort"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

var errNilAgent = errors.New("agent is nil")

type exportState struct {
	stack []agent.Agent
}

// Export exports a normalized static structure snapshot for the given agent.
func Export(ctx context.Context, a agent.Agent) (*Snapshot, error) {
	if isNilAgent(a) {
		return nil, errNilAgent
	}
	state := &exportState{}
	return exportWithState(ctx, a, state)
}

func exportWithState(
	ctx context.Context,
	a agent.Agent,
	state *exportState,
) (*Snapshot, error) {
	if isNilAgent(a) {
		return nil, errNilAgent
	}
	if state.containsRecursiveAgentInstance(a) {
		return normalizeSnapshot(opaqueLeafSnapshot(a))
	}
	exporter, ok := a.(Exporter)
	if !ok {
		return normalizeSnapshot(opaqueLeafSnapshot(a))
	}
	state.push(a)
	raw, err := exporter.Export(ctx, state.exportChild)
	state.pop()
	if err != nil {
		return nil, err
	}
	if raw == nil {
		return nil, errors.New("structure exporter returned nil snapshot")
	}
	return normalizeSnapshot(raw)
}

func (s *exportState) exportChild(
	ctx context.Context,
	a agent.Agent,
) (*Snapshot, error) {
	return exportWithState(ctx, a, s)
}

func normalizeSnapshot(raw *Snapshot) (*Snapshot, error) {
	snapshot := cloneSnapshot(raw)
	if snapshot.EntryNodeID == "" {
		return nil, errors.New("entry node id is empty")
	}
	nodeByID := make(map[string]Node, len(snapshot.Nodes))
	for _, node := range snapshot.Nodes {
		if node.NodeID == "" {
			return nil, errors.New("node id is empty")
		}
		if _, exists := nodeByID[node.NodeID]; exists {
			return nil, fmt.Errorf("duplicate node id %q", node.NodeID)
		}
		nodeByID[node.NodeID] = node
	}
	if _, exists := nodeByID[snapshot.EntryNodeID]; !exists {
		return nil, fmt.Errorf("entry node %q does not exist", snapshot.EntryNodeID)
	}
	edges := make([]Edge, 0, len(snapshot.Edges))
	seenEdges := make(map[edgeKey]struct{}, len(snapshot.Edges))
	for _, edge := range snapshot.Edges {
		if _, exists := nodeByID[edge.FromNodeID]; !exists {
			return nil, fmt.Errorf("edge from node %q does not exist", edge.FromNodeID)
		}
		if _, exists := nodeByID[edge.ToNodeID]; !exists {
			return nil, fmt.Errorf("edge to node %q does not exist", edge.ToNodeID)
		}
		key := edgeKey{from: edge.FromNodeID, to: edge.ToNodeID}
		if _, exists := seenEdges[key]; exists {
			continue
		}
		seenEdges[key] = struct{}{}
		edges = append(edges, edge)
	}
	surfaces := make([]Surface, 0, len(snapshot.Surfaces))
	seenSurfaceTypes := make(map[string]struct{}, len(snapshot.Surfaces))
	for _, surface := range snapshot.Surfaces {
		if _, exists := nodeByID[surface.NodeID]; !exists {
			return nil, fmt.Errorf("surface node %q does not exist", surface.NodeID)
		}
		key := SurfaceID(surface.NodeID, surface.Type)
		if _, exists := seenSurfaceTypes[key]; exists {
			return nil, fmt.Errorf(
				"duplicate surface type %q on node %q",
				surface.Type,
				surface.NodeID,
			)
		}
		seenSurfaceTypes[key] = struct{}{}
		surface.SurfaceID = key
		if err := validateSurfaceValue(surface.Type, surface.Value); err != nil {
			return nil, fmt.Errorf("invalid surface %q: %w", key, err)
		}
		surface.Value = normalizeSurfaceValue(surface.Value)
		surfaces = append(surfaces, surface)
	}
	nodes := make([]Node, 0, len(nodeByID))
	for _, node := range nodeByID {
		nodes = append(nodes, node)
	}
	sort.Slice(nodes, func(i, j int) bool {
		return nodes[i].NodeID < nodes[j].NodeID
	})
	sort.Slice(edges, func(i, j int) bool {
		if edges[i].FromNodeID != edges[j].FromNodeID {
			return edges[i].FromNodeID < edges[j].FromNodeID
		}
		return edges[i].ToNodeID < edges[j].ToNodeID
	})
	sort.Slice(surfaces, func(i, j int) bool {
		return surfaces[i].SurfaceID < surfaces[j].SurfaceID
	})
	snapshot.Nodes = nodes
	snapshot.Edges = edges
	snapshot.Surfaces = surfaces
	snapshot.StructureID = ""
	hashInput, err := json.Marshal(snapshot)
	if err != nil {
		return nil, fmt.Errorf("marshal snapshot: %w", err)
	}
	sum := sha256.Sum256(hashInput)
	snapshot.StructureID = "struct_" + hex.EncodeToString(sum[:])
	return snapshot, nil
}

type edgeKey struct {
	from string
	to   string
}

func cloneSnapshot(raw *Snapshot) *Snapshot {
	if raw == nil {
		return &Snapshot{}
	}
	snapshot := &Snapshot{
		StructureID: raw.StructureID,
		EntryNodeID: raw.EntryNodeID,
		Nodes:       append([]Node(nil), raw.Nodes...),
		Edges:       append([]Edge(nil), raw.Edges...),
		Surfaces:    make([]Surface, 0, len(raw.Surfaces)),
	}
	for _, surface := range raw.Surfaces {
		snapshot.Surfaces = append(snapshot.Surfaces, Surface{
			SurfaceID: surface.SurfaceID,
			NodeID:    surface.NodeID,
			Type:      surface.Type,
			Value:     cloneSurfaceValue(surface.Value),
		})
	}
	return snapshot
}

func cloneSurfaceValue(value SurfaceValue) SurfaceValue {
	cloned := SurfaceValue{
		FewShot: cloneFewShot(value.FewShot),
		Tools:   cloneToolRefs(value.Tools),
		Skills:  append([]SkillRef(nil), value.Skills...),
	}
	if value.Text != nil {
		text := *value.Text
		cloned.Text = &text
	}
	if value.PromptSyntax != nil {
		promptSyntax := *value.PromptSyntax
		cloned.PromptSyntax = &promptSyntax
	}
	if value.Model != nil {
		modelRef := *value.Model
		modelRef.Headers = maps.Clone(value.Model.Headers)
		cloned.Model = &modelRef
	}
	return cloned
}

func cloneToolRefs(refs []ToolRef) []ToolRef {
	if len(refs) == 0 {
		return nil
	}
	out := make([]ToolRef, len(refs))
	for i, ref := range refs {
		out[i] = ToolRef{
			ID:           ref.ID,
			Description:  ref.Description,
			InputSchema:  cloneToolSchema(ref.InputSchema),
			OutputSchema: cloneToolSchema(ref.OutputSchema),
		}
	}
	return out
}

func cloneToolSchema(schema *tool.Schema) *tool.Schema {
	if schema == nil {
		return nil
	}
	return &tool.Schema{
		Type:                 schema.Type,
		Description:          schema.Description,
		Required:             append([]string(nil), schema.Required...),
		Properties:           cloneSchemaMap(schema.Properties),
		Items:                cloneToolSchema(schema.Items),
		AdditionalProperties: cloneSchemaValue(schema.AdditionalProperties),
		Default:              cloneSchemaValue(schema.Default),
		Enum:                 cloneSchemaValues(schema.Enum),
		Ref:                  schema.Ref,
		Defs:                 cloneSchemaMap(schema.Defs),
	}
}

func cloneSchemaMap(in map[string]*tool.Schema) map[string]*tool.Schema {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]*tool.Schema, len(in))
	for key, value := range in {
		out[key] = cloneToolSchema(value)
	}
	return out
}

func cloneSchemaValues(in []any) []any {
	if len(in) == 0 {
		return nil
	}
	out := make([]any, len(in))
	for i, value := range in {
		out[i] = cloneSchemaValue(value)
	}
	return out
}

func cloneSchemaValue(value any) any {
	switch current := value.(type) {
	case nil:
		return nil
	case *tool.Schema:
		return cloneToolSchema(current)
	case map[string]any:
		out := make(map[string]any, len(current))
		for key, item := range current {
			out[key] = cloneSchemaValue(item)
		}
		return out
	case []any:
		return cloneSchemaValues(current)
	case []byte:
		return append([]byte(nil), current...)
	default:
		return current
	}
}

func cloneFewShot(value []FewShotExample) []FewShotExample {
	if len(value) == 0 {
		return nil
	}
	out := make([]FewShotExample, len(value))
	for i, example := range value {
		out[i].Messages = append([]FewShotMessage(nil), example.Messages...)
	}
	return out
}

func normalizeSurfaceValue(value SurfaceValue) SurfaceValue {
	value = cloneSurfaceValue(value)
	if len(value.Tools) > 0 {
		sort.Slice(value.Tools, func(i, j int) bool {
			return value.Tools[i].ID < value.Tools[j].ID
		})
		value.Tools = uniqueToolRefs(value.Tools)
	}
	if len(value.Skills) > 0 {
		sort.Slice(value.Skills, func(i, j int) bool {
			return value.Skills[i].ID < value.Skills[j].ID
		})
		value.Skills = uniqueSkillRefs(value.Skills)
	}
	return value
}

func uniqueToolRefs(refs []ToolRef) []ToolRef {
	if len(refs) == 0 {
		return nil
	}
	out := refs[:0]
	var last string
	for i, ref := range refs {
		if i == 0 || ref.ID != last {
			out = append(out, ref)
			last = ref.ID
		}
	}
	return out
}

func uniqueSkillRefs(refs []SkillRef) []SkillRef {
	if len(refs) == 0 {
		return nil
	}
	out := refs[:0]
	var last string
	for i, ref := range refs {
		if i == 0 || ref.ID != last {
			out = append(out, ref)
			last = ref.ID
		}
	}
	return out
}

func opaqueLeafSnapshot(a agent.Agent) *Snapshot {
	name := a.Info().Name
	nodeID := escapeNodeIDSegment(name)
	return &Snapshot{
		EntryNodeID: nodeID,
		Nodes: []Node{
			{
				NodeID: nodeID,
				Kind:   NodeKindAgent,
				Name:   name,
			},
		},
	}
}

func escapeNodeIDSegment(name string) string {
	if name == "" {
		return "_"
	}
	replacer := strings.NewReplacer("~", "~0", "/", "~1")
	escaped := replacer.Replace(name)
	if escaped == "" {
		return "_"
	}
	return escaped
}

func validateSurfaceValue(surfaceType SurfaceType, value SurfaceValue) error {
	switch surfaceType {
	case SurfaceTypeInstruction, SurfaceTypeGlobalInstruction:
		return validateTextSurfaceValue(value)
	case SurfaceTypeFewShot:
		return validateFewShotSurfaceValue(value)
	case SurfaceTypeModel:
		return validateModelSurfaceValue(value)
	case SurfaceTypeTool:
		return validateToolSurfaceValue(value)
	case SurfaceTypeSkill:
		return validateSkillSurfaceValue(value)
	default:
		return fmt.Errorf("unknown surface type %q", surfaceType)
	}
}

func validateTextSurfaceValue(value SurfaceValue) error {
	if len(value.FewShot) > 0 || value.Model != nil || len(value.Tools) > 0 || len(value.Skills) > 0 {
		return errors.New("text surface must not carry other value branches")
	}
	if value.PromptSyntax != nil {
		switch *value.PromptSyntax {
		case PromptSyntaxMixedBrace, PromptSyntaxSingleBrace, PromptSyntaxDoubleBrace:
		default:
			return fmt.Errorf("unknown prompt syntax %q", *value.PromptSyntax)
		}
	}
	return nil
}

func validateFewShotSurfaceValue(value SurfaceValue) error {
	if value.Text != nil || value.PromptSyntax != nil || value.Model != nil || len(value.Tools) > 0 || len(value.Skills) > 0 {
		return errors.New("few-shot surface must not carry other value branches")
	}
	return nil
}

func validateModelSurfaceValue(value SurfaceValue) error {
	if value.Text != nil || value.PromptSyntax != nil || len(value.FewShot) > 0 || len(value.Tools) > 0 || len(value.Skills) > 0 {
		return errors.New("model surface must not carry other value branches")
	}
	return nil
}

func validateToolSurfaceValue(value SurfaceValue) error {
	if value.Text != nil || value.PromptSyntax != nil || len(value.FewShot) > 0 || value.Model != nil || len(value.Skills) > 0 {
		return errors.New("tool surface must not carry other value branches")
	}
	return nil
}

func validateSkillSurfaceValue(value SurfaceValue) error {
	if value.Text != nil || value.PromptSyntax != nil || len(value.FewShot) > 0 || value.Model != nil || len(value.Tools) > 0 {
		return errors.New("skill surface must not carry other value branches")
	}
	return nil
}

func (s *exportState) push(a agent.Agent) {
	s.stack = append(s.stack, a)
}

func (s *exportState) pop() {
	if len(s.stack) == 0 {
		return
	}
	s.stack = s.stack[:len(s.stack)-1]
}

func (s *exportState) containsRecursiveAgentInstance(a agent.Agent) bool {
	for _, current := range s.stack {
		if samePointerAgentInstance(current, a) {
			return true
		}
	}
	return false
}

func samePointerAgentInstance(left agent.Agent, right agent.Agent) bool {
	leftValue := reflect.ValueOf(left)
	rightValue := reflect.ValueOf(right)
	if !leftValue.IsValid() || !rightValue.IsValid() {
		return false
	}
	if leftValue.Type() != rightValue.Type() {
		return false
	}
	if leftValue.Kind() != reflect.Pointer || rightValue.Kind() != reflect.Pointer {
		return false
	}
	if leftValue.IsNil() || rightValue.IsNil() {
		return false
	}
	return leftValue.Pointer() == rightValue.Pointer()
}

func isNilAgent(a agent.Agent) bool {
	if a == nil {
		return true
	}
	value := reflect.ValueOf(a)
	switch value.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return value.IsNil()
	default:
		return false
	}
}
