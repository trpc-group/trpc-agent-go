package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"math"
	"net/http"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/model/openai"
	"trpc.group/trpc-go/trpc-agent-go/runner"
	"trpc.group/trpc-go/trpc-agent-go/server/agui"
	"trpc.group/trpc-go/trpc-agent-go/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool/function"
)

var (
	modelName  = flag.String("model", "deepseek-chat", "Model to use")
	isStream   = flag.Bool("stream", true, "Whether to stream the response")
	listenAddr = flag.String("listen", "127.0.0.1:8080", "Address to listen on")
	path       = flag.String("path", "/agui", "HTTP path exposed for AG-UI")
)

func main() {
	flag.Parse()
	r := runner.NewRunner("agui-external-tool", newAgent())
	server, err := agui.New(r, agui.WithPath(*path))
	if err != nil {
		log.Fatalf("create AG-UI server: %v", err)
	}
	log.Printf("AG-UI external tool demo listening on http://%s%s", *listenAddr, *path)
	log.Printf("Ensure OPENAI_API_KEY is exported before running if the selected model requires authentication.")
	if err := http.ListenAndServe(*listenAddr, server.Handler()); err != nil {
		log.Fatalf("listen and serve: %v", err)
	}
}

func newAgent() agent.Agent {
	modelInstance := openai.New(*modelName)
	generation := model.GenerationConfig{Stream: *isStream}
	changeBackgroundTool := function.NewFunctionTool(
		changeBackground,
		function.WithName("change_background"),
		function.WithDescription("Change the background color of the client UI. "+
			"The color should be in the format of #RRGGBB."),
	)
	calculatorTool := function.NewFunctionTool(
		calculator,
		function.WithName("calculator"),
		function.WithDescription("A calculator tool, you can use it to calculate the result of the operation. "+
			"a is the first number, b is the second number, "+
			"the operation can be add, subtract, multiply, divide, power."),
	)
	return llmagent.New(
		"external-tool-agent",
		llmagent.WithModel(modelInstance),
		llmagent.WithGenerationConfig(generation),
		llmagent.WithInstruction("You are a helpful assistant."+
			"You can use the calculator tool to calculate the result of the operation."+
			"You can also use the change_background tool to change the background color of the client UI."+
			"If the color is not accurately specified, use the color you think is closest to the user's wants."),
		llmagent.WithTools([]tool.Tool{changeBackgroundTool, calculatorTool}),
	)
}

type changeBackgroundArgs struct {
	Color string `json:"color"`
}

func changeBackground(ctx context.Context, args changeBackgroundArgs) (*int, error) {
	log.Printf("[tool] request to change background to %s", strings.ToUpper(args.Color))
	return nil, agent.NewStopError(
		"client must apply the background color and report back to the agent",
		agent.WithStopReason(agent.StopReasonExternalTool),
	)
}

func calculator(ctx context.Context, args calculatorArgs) (calculatorResult, error) {
	var result float64
	switch args.Operation {
	case "add", "+":
		result = args.A + args.B
	case "subtract", "-":
		result = args.A - args.B
	case "multiply", "*":
		result = args.A * args.B
	case "divide", "/":
		result = args.A / args.B
	case "power", "^":
		result = math.Pow(args.A, args.B)
	default:
		return calculatorResult{Result: 0}, fmt.Errorf("invalid operation: %s", args.Operation)
	}
	return calculatorResult{Result: result}, nil
}

type calculatorArgs struct {
	Operation string  `json:"operation" description:"add, subtract, multiply, divide, power"`
	A         float64 `json:"a" description:"First number"`
	B         float64 `json:"b" description:"Second number"`
}

type calculatorResult struct {
	Result float64 `json:"result"`
}
