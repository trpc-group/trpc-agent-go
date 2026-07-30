// Package graphagent assembles the Code Review Agent GraphAgent from its 8 nodes.
package graphagent

import (
	"reflect"

	"github.com/dcdc4747/trpc-agent-go-cr-project/internal/dedup"
	"github.com/dcdc4747/trpc-agent-go-cr-project/internal/diffparser"
	"github.com/dcdc4747/trpc-agent-go-cr-project/internal/llmanalyzer"
	"github.com/dcdc4747/trpc-agent-go-cr-project/internal/permission"
	"github.com/dcdc4747/trpc-agent-go-cr-project/internal/report"
	"github.com/dcdc4747/trpc-agent-go-cr-project/internal/ruleengine"
	"github.com/dcdc4747/trpc-agent-go-cr-project/internal/sandbox"
	"github.com/dcdc4747/trpc-agent-go-cr-project/internal/state"
	storagewriter "github.com/dcdc4747/trpc-agent-go-cr-project/internal/storagewriter"
	"trpc.group/trpc-go/trpc-agent-go/graph"
)

// Build creates the 8-node code review GraphAgent.
// Nodes are wired in serial order per design v1.2:
//
//	DiffParser → PermissionFilter → SandboxRunner → RuleEngine → LLMAnalyzer → DedupEngine → ReportGenerator → StorageWriter
func Build() (*graph.StateGraph, error) {
	schema := graph.NewStateSchema()

	// Declare state fields with proper reflect types
	schema.AddField(state.StateKeyInputDiffFile, graph.StateField{Type: reflect.TypeOf("")})
	schema.AddField(state.StateKeyInputDiffText, graph.StateField{Type: reflect.TypeOf("")})
	schema.AddField(state.StateKeyOutputDir, graph.StateField{Type: reflect.TypeOf("")})
	schema.AddField(state.StateKeyTaskID, graph.StateField{Type: reflect.TypeOf("")})
	schema.AddField(state.StateKeyJSONReportPath, graph.StateField{Type: reflect.TypeOf("")})
	schema.AddField(state.StateKeyMDReportPath, graph.StateField{Type: reflect.TypeOf("")})
	schema.AddField(state.StateKeyStorageDone, graph.StateField{Type: reflect.TypeOf(false)})

	sg := graph.NewStateGraph(schema)

	// Wire 8 nodes in serial order
	sg.AddNode("DiffParser",       diffparser.Run)
	sg.AddNode("PermissionFilter", permission.Run)
	sg.AddNode("SandboxRunner",    sandbox.Run)
	sg.AddNode("RuleEngine",       ruleengine.Run)
	sg.AddNode("LLMAnalyzer",      llmanalyzer.Run)
	sg.AddNode("DedupEngine",      dedup.Run)
	sg.AddNode("ReportGenerator",  report.Run)
	sg.AddNode("StorageWriter",    storagewriter.Run)

	sg.SetEntryPoint("DiffParser")
	sg.AddEdge("DiffParser",       "PermissionFilter")
	sg.AddEdge("PermissionFilter", "SandboxRunner")
	sg.AddEdge("SandboxRunner",    "RuleEngine")
	sg.AddEdge("RuleEngine",       "LLMAnalyzer")
	sg.AddEdge("LLMAnalyzer",      "DedupEngine")
	sg.AddEdge("DedupEngine",      "ReportGenerator")
	sg.AddEdge("ReportGenerator",  "StorageWriter")
	sg.SetFinishPoint("StorageWriter")

	return sg, nil
}
