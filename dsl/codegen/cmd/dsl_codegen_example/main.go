package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"trpc.group/trpc-go/trpc-agent-go/dsl"
	"trpc.group/trpc-go/trpc-agent-go/dsl/codegen"
)

// Simple helper binary to try the codegen pipeline against a single DSL
// workflow. By default it uses the customer_service example and writes the
// generated Go code to dsl/generated/customer_service_demo so it can be run
// directly with go run.
//
// You can also point it at a different workflow/out directory, e.g.:
//
//	cd dsl && go run ./codegen/cmd/dsl_codegen_example \
//	  -workflow ../examples/dsl/travel_assistant/workflow.json \
//	  -out ./generated/travel_assistant_demo \
//	  -mode agui
func main() {
	workflowPathFlag := flag.String(
		"workflow",
		"../examples/dsl/customer_service/workflow.json",
		"Path to DSL workflow.json (relative to dsl module root)",
	)
	outDirFlag := flag.String(
		"out",
		"./generated/customer_service_demo",
		"Output directory for generated Go package",
	)
	modeFlag := flag.String(
		"mode",
		"interactive",
		"Run mode: 'interactive' (terminal CLI) or 'agui' (AG-UI HTTP server)",
	)
	flag.Parse()

	workflowPath := filepath.Clean(*workflowPathFlag)

	data, err := os.ReadFile(workflowPath)
	if err != nil {
		panic(fmt.Errorf("read workflow.json %s: %w", workflowPath, err))
	}

	var g dsl.Graph
	if err := json.Unmarshal(data, &g); err != nil {
		panic(fmt.Errorf("unmarshal workflow.json %s: %w", workflowPath, err))
	}

	runMode := codegen.RunModeInteractive
	if *modeFlag == "agui" {
		runMode = codegen.RunModeAGUI
	}

	out, err := codegen.GenerateNativeGo(&g, codegen.Options{
		PackageName: "main",
		AppName:     g.Name,
		RunMode:     runMode,
	})
	if err != nil {
		panic(fmt.Errorf("GenerateNativeGo: %w", err))
	}

	targetDir := filepath.Clean(*outDirFlag)
	if err := os.MkdirAll(targetDir, 0o755); err != nil {
		panic(fmt.Errorf("mkdir %s: %w", targetDir, err))
	}

	// Cleanup known legacy outputs when switching codegen modes.
	// Older versions emitted graph.go + main.go; current codegen emits only main.go.
	if _, ok := out.Files["graph.go"]; !ok {
		_ = os.Remove(filepath.Join(targetDir, "graph.go"))
	}

	for name, src := range out.Files {
		target := filepath.Join(targetDir, name)
		if err := os.WriteFile(target, src, 0o644); err != nil {
			panic(fmt.Errorf("write %s: %w", target, err))
		}
	}

	fmt.Printf("✅ Codegen complete. Workflow=%s, mode=%s, files written to %s\n", workflowPath, runMode, targetDir)
}
