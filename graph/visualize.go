//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package graph

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os/exec"
	"sort"
	"strings"
)

// Public constants for common string literals to avoid magic strings.
// Use these with visualization helpers and rendering.
const (
	// RankDirLR sets a left-to-right layout in Graphviz.
	RankDirLR = "LR"
	// RankDirTB sets a top-to-bottom layout in Graphviz.
	RankDirTB = "TB"

	// ImageFormatPNG is the PNG output format for Graphviz.
	ImageFormatPNG = "png"
	// ImageFormatSVG is the SVG output format for Graphviz.
	ImageFormatSVG = "svg"
)

// Style constants for visualization to avoid magic literals.
const (
	shapeBox     = "box"
	shapeDiamond = "diamond"
	shapeOval    = "oval"

	colorLLMFill       = "#e3f2fd"
	colorLLMBorder     = "#2196f3"
	colorToolFill      = "#fff3e0"
	colorToolBorder    = "#ff9800"
	colorAgentFill     = "#e8f5e9"
	colorAgentBorder   = "#4caf50"
	colorJoinFill      = "#f3e5f5"
	colorJoinBorder    = "#9c27b0"
	colorRouterFill    = "#eeeeee"
	colorRouterBorder  = "#757575"
	colorDefaultFill   = colorJoinFill
	colorDefaultBorder = colorJoinBorder

	colorStartFill   = "#e1f5e1"
	colorStartBorder = colorAgentBorder
	colorEndFill     = "#ffe1e1"
	colorEndBorder   = "#f44336"

	colorConditionalEdge = "#999999"
	colorDestinationEdge = "#aaaaaa"
)

// VizOptions configures DOT export and rendering.
// Use helpers like WithRankDir to construct options.
type VizOptions struct {
	// RankDir sets DOT graph direction: "LR" (left-to-right) or "TB" (top-to-bottom).
	RankDir string
	// IncludeDestinations toggles visualization of declared dynamic destinations
	// (set via WithDestinations). Rendered as dotted gray edges.
	IncludeDestinations bool
	// IncludeStartEnd toggles visualization of virtual Start/End nodes.
	IncludeStartEnd bool
	// GraphLabel optionally labels the whole graph (shows in DOT as label=...).
	GraphLabel string
}

// VizOption mutates VizOptions.
type VizOption func(*VizOptions)

// WithRankDir sets DOT graph direction. Valid values: "LR", "TB".
func WithRankDir(dir string) VizOption {
	return func(o *VizOptions) {
		if dir == RankDirLR || dir == RankDirTB {
			o.RankDir = dir
		}
	}
}

// WithIncludeDestinations toggles rendering of declared dynamic destinations.
func WithIncludeDestinations(include bool) VizOption {
	return func(o *VizOptions) { o.IncludeDestinations = include }
}

// WithIncludeStartEnd toggles rendering of Start/End virtual nodes.
func WithIncludeStartEnd(include bool) VizOption {
	return func(o *VizOptions) { o.IncludeStartEnd = include }
}

// WithGraphLabel sets an optional label for the graph.
func WithGraphLabel(label string) VizOption {
	return func(o *VizOptions) { o.GraphLabel = label }
}

// defaultVizOptions returns sensible defaults for visualization.
func defaultVizOptions() *VizOptions {
	return &VizOptions{
		RankDir:             RankDirLR,
		IncludeDestinations: true,
		IncludeStartEnd:     true,
	}
}

// DOT returns a Graphviz DOT representation of the graph.
// It includes:
//   - Nodes styled by NodeType
//   - Runtime edges (solid)
//   - Conditional edges (dashed, labeled by branch)
//   - Declared destinations from WithDestinations (dotted, gray)
func (g *Graph) DOT(opts ...VizOption) string {
	o := defaultVizOptions()
	for _, fn := range opts {
		fn(o)
	}

	// Snapshot data under read lock.
	g.mu.RLock()
	nodeIDs := make([]string, 0, len(g.nodes))
	for id := range g.nodes {
		nodeIDs = append(nodeIDs, id)
	}
	sort.Strings(nodeIDs)
	edgesCopy := copyEdges(g.edges)
	condCopy := copyConditionalEdges(g.conditionalEdges)
	entry := g.entryPoint
	g.mu.RUnlock()

	var b strings.Builder
	b.WriteString("digraph G {\n")
	writeGraphHeader(&b, o)
	writeVirtualStartEnd(&b, o)
	writeNodes(&b, g, nodeIDs)
	writeRuntimeEdges(&b, edgesCopy, o)
	writeConditionalEdges(&b, g, condCopy, o)
	writeDestinations(&b, g, nodeIDs, o)
	highlightEntry(&b, entry, o)
	b.WriteString("}\n")
	return b.String()
}

// copyEdges returns a deep copy of edges map for stable iteration.
func copyEdges(src map[string][]*Edge) map[string][]*Edge {
	dst := make(map[string][]*Edge, len(src))
	for k, v := range src {
		vv := make([]*Edge, len(v))
		copy(vv, v)
		dst[k] = vv
	}
	return dst
}

// copyConditionalEdges returns a shallow copy of conditional edges map.
func copyConditionalEdges(src map[string]*ConditionalEdge) map[string]*ConditionalEdge {
	dst := make(map[string]*ConditionalEdge, len(src))
	for k, v := range src {
		dst[k] = v
	}
	return dst
}

// writeGraphHeader writes DOT-level attributes.
func writeGraphHeader(b *strings.Builder, o *VizOptions) {
	b.WriteString(fmt.Sprintf("  rankdir=%s;\n", escapeIdentifier(o.RankDir)))
	b.WriteString("  node [fontname=\"Helvetica\"];\n")
	b.WriteString("  edge [fontname=\"Helvetica\"];\n")
	if o.GraphLabel != "" {
		b.WriteString(fmt.Sprintf("  label=\"%s\";\n  labelloc=t;\n", escapeLabel(o.GraphLabel)))
	}
}

// writeVirtualStartEnd optionally emits Start/End visuals.
func writeVirtualStartEnd(b *strings.Builder, o *VizOptions) {
	if !o.IncludeStartEnd {
		return
	}
	b.WriteString(fmt.Sprintf("  \"%s\" [label=\"start\", shape=%s, style=filled, fillcolor=\"%s\", color=\"%s\"];\n",
		escapeIdentifier(Start), shapeOval, colorStartFill, colorStartBorder))
	b.WriteString(fmt.Sprintf("  \"%s\" [label=\"finish\", shape=%s, style=filled, fillcolor=\"%s\", color=\"%s\"];\n",
		escapeIdentifier(End), shapeOval, colorEndFill, colorEndBorder))
}

// writeNodes emits node declarations with simple styling per NodeType.
func writeNodes(b *strings.Builder, g *Graph, nodeIDs []string) {
	for _, id := range nodeIDs {
		n := g.nodes[id]
		label := n.Name
		if label == "" {
			label = n.ID
		}
		shape, fill, color := styleForNodeType(n.Type)
		b.WriteString(fmt.Sprintf("  \"%s\" [label=\"%s\", shape=%s, style=filled, fillcolor=\"%s\", color=\"%s\"];\n",
			escapeIdentifier(n.ID), escapeLabel(label), shape, fill, color))
	}
}

// writeRuntimeEdges emits solid edges (optionally skipping Start/End).
func writeRuntimeEdges(b *strings.Builder, edges map[string][]*Edge, o *VizOptions) {
	var fromIDs []string
	for from := range edges {
		fromIDs = append(fromIDs, from)
	}
	sort.Strings(fromIDs)
	for _, from := range fromIDs {
		es := edges[from]
		for _, e := range es {
			if !o.IncludeStartEnd && (e.From == Start || e.To == End) {
				continue
			}
			b.WriteString(fmt.Sprintf("  \"%s\" -> \"%s\";\n", escapeIdentifier(e.From), escapeIdentifier(e.To)))
		}
	}
}

// writeConditionalEdges emits dashed edges with branch labels.
func writeConditionalEdges(b *strings.Builder, g *Graph, cond map[string]*ConditionalEdge, o *VizOptions) {
	var fromIDs []string
	for from := range cond {
		fromIDs = append(fromIDs, from)
	}
	sort.Strings(fromIDs)
	for _, from := range fromIDs {
		ce := cond[from]
		keys := make([]string, 0, len(ce.PathMap))
		for k := range ce.PathMap {
			keys = append(keys, k)
		}
		// For MultiCondition with empty PathMap, fallback to node-level ends
		// to render potential branches for visualization.
		if len(keys) == 0 {
			if n, ok := g.nodes[from]; ok && n != nil && n.ends != nil {
				for k := range n.ends {
					keys = append(keys, k)
				}
			}
		}
		sort.Strings(keys)
		for _, k := range keys {
			to := ce.PathMap[k]
			if to == "" {
				// Resolve through ends map for display purposes.
				if n, ok := g.nodes[from]; ok && n != nil && n.ends != nil {
					if v, ok2 := n.ends[k]; ok2 {
						to = v
					}
				}
			}
			if !o.IncludeStartEnd && (from == Start || to == End) {
				continue
			}
			b.WriteString(fmt.Sprintf("  \"%s\" -> \"%s\" [style=dashed, color=\"%s\", label=\"%s\"];\n",
				escapeIdentifier(from), escapeIdentifier(to), colorConditionalEdge, escapeLabel(k)))
		}
	}
}

// writeDestinations emits dotted gray edges for declared destinations.
func writeDestinations(b *strings.Builder, g *Graph, nodeIDs []string, o *VizOptions) {
	if !o.IncludeDestinations {
		return
	}
	for _, id := range nodeIDs {
		n := g.nodes[id]
		if n.destinations == nil {
			continue
		}
		var dstIDs []string
		for to := range n.destinations {
			dstIDs = append(dstIDs, to)
		}
		sort.Strings(dstIDs)
		for _, to := range dstIDs {
			if !o.IncludeStartEnd && to == End {
				continue
			}
			lbl := n.destinations[to]
			if lbl != "" {
				fmt.Fprintf(b, "  \"%s\" -> \"%s\" [style=dotted, color=\"%s\", label=\"%s\", constraint=false];\n",
					escapeIdentifier(n.ID), escapeIdentifier(to), colorDestinationEdge, escapeLabel(lbl))
			} else {
				fmt.Fprintf(b, "  \"%s\" -> \"%s\" [style=dotted, color=\"%s\", constraint=false];\n",
					escapeIdentifier(n.ID), escapeIdentifier(to), colorDestinationEdge)
			}
		}
	}
}

// highlightEntry emphasizes entry when Start node is hidden.
func highlightEntry(b *strings.Builder, entry string, o *VizOptions) {
	if o.IncludeStartEnd || entry == "" {
		return
	}
	// When Start is hidden, emphasize entry with a double border.
	b.WriteString(fmt.Sprintf("  \"%s\" [peripheries=2];\n", escapeIdentifier(entry)))
}

// WriteDOT writes the DOT representation to the provided writer.
func (g *Graph) WriteDOT(w io.Writer, opts ...VizOption) error {
	_, err := io.WriteString(w, g.DOT(opts...))
	return err
}

// RenderImage renders the graph to an image by invoking Graphviz's `dot` binary.
// The format should be a valid Graphviz output format (e.g., "png", "svg").
// It returns an error if `dot` is not found or the command fails.
func (g *Graph) RenderImage(ctx context.Context, format, outputPath string, opts ...VizOption) error {
	if format == "" {
		format = ImageFormatPNG
	}
	dotPath, err := exec.LookPath("dot")
	if err != nil {
		return fmt.Errorf("graphviz 'dot' binary not found in PATH: %w", err)
	}
	cmd := exec.CommandContext(ctx, dotPath, "-T"+format, "-o", outputPath)
	cmd.Stdin = bytes.NewBufferString(g.DOT(opts...))
	out, runErr := cmd.CombinedOutput()
	if runErr != nil {
		return fmt.Errorf("dot render failed: %w, output: %s", runErr, string(out))
	}
	return nil
}

// styleForNodeType returns shape/fill/border color for a node type.
func styleForNodeType(nt NodeType) (shape, fill, color string) {
	switch nt {
	case NodeTypeLLM:
		return shapeBox, colorLLMFill, colorLLMBorder
	case NodeTypeTool:
		return shapeBox, colorToolFill, colorToolBorder
	case NodeTypeAgent:
		return shapeBox, colorAgentFill, colorAgentBorder
	case NodeTypeJoin:
		return shapeDiamond, colorJoinFill, colorJoinBorder
	case NodeTypeRouter:
		return shapeDiamond, colorRouterFill, colorRouterBorder
	default:
		return shapeBox, colorDefaultFill, colorDefaultBorder
	}
}

// escapeLabel escapes label strings for DOT.
func escapeLabel(s string) string {
	s = strings.ReplaceAll(s, "\\", "\\\\")
	s = strings.ReplaceAll(s, "\"", "\\\"")
	s = strings.ReplaceAll(s, "\n", "\\n")
	return s
}

// escapeIdentifier escapes node/edge identifiers for DOT.
func escapeIdentifier(s string) string {
	// Identifiers are quoted in our output, so re-use label escaping.
	return escapeLabel(s)
}
