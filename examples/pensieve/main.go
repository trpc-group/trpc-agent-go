//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package main demonstrates Pensieve-style context management in a realistic
// research-assistant scenario.
//
// The agent is given a simulated web search tool that returns large result
// payloads. As the conversation grows, the model uses the four Pensieve
// context-management tools (check_budget, note, delete_context, read_notes)
// to distil key findings into persistent notes and prune the raw search
// results from its visible context — keeping the context window lean without
// losing critical information.
//
// This mirrors a real production pattern where an LLM-based agent processes
// many tool outputs over a long session and must actively manage its own
// context to avoid hitting token limits.
//
// Reference: "The Pensieve Paradigm: Stateful Language Models Mastering Their
// Own Context" (arXiv:2602.12108).
package main

import (
	"bufio"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/model/openai"
	"trpc.group/trpc-go/trpc-agent-go/runner"
	"trpc.group/trpc-go/trpc-agent-go/tool"
	ctxtools "trpc.group/trpc-go/trpc-agent-go/tool/context"
	"trpc.group/trpc-go/trpc-agent-go/tool/function"
)

var (
	modelName = flag.String("model", "gpt-4o", "Name of the model to use")
	streaming = flag.Bool("streaming", true, "Enable streaming mode for responses")
	autoMode  = flag.Bool("auto", false, "Run predefined queries automatically (no human input)")
)

// autoQueries is the predefined sequence for -auto mode.
var autoQueries = []string{
	"Research transformer architectures and attention mechanisms",
	"Now research climate change and carbon emissions",
	"Also look into quantum computing progress",
	"Summarise all your findings so far",
}

// ---------------------------------------------------------------------------
// System instruction
// ---------------------------------------------------------------------------

const pensieveInstruction = `You are a research assistant that helps users investigate topics by searching the web and synthesising findings.

You have a web_search tool to find information. Search results can be large,
so you also have four context-management tools to keep your context lean:

  • check_budget  — See how many events are visible vs masked.
  • note          — Save a persistent note (key + content). Notes survive pruning.
  • delete_context — Mask specific events by ID so they are no longer in your visible context.
  • read_notes    — Retrieve all saved notes.

## Workflow

1. When the user asks to research a topic, use web_search.
2. After you receive a large search result, IMMEDIATELY:
   a. Summarise the key findings in a note (call the "note" tool).
   b. Delete the raw search result event from context (call "delete_context").
3. Call check_budget periodically to monitor context pressure.
4. If the user asks you to recall earlier findings, use read_notes.

This workflow lets you handle many research queries in a single session
without running out of context space.
`

// ---------------------------------------------------------------------------
// Simulated web search tool
// ---------------------------------------------------------------------------

// WebSearchInput is the input for the simulated web search tool.
type WebSearchInput struct {
	Query string `json:"query" jsonschema:"description=Search query string,required"`
}

// WebSearchResult represents a single search result entry.
type WebSearchResult struct {
	Title   string `json:"title"`
	URL     string `json:"url"`
	Snippet string `json:"snippet"`
}

// WebSearchOutput is the output of the simulated web search tool.
type WebSearchOutput struct {
	Query   string            `json:"query"`
	Results []WebSearchResult `json:"results"`
	Total   int               `json:"total"`
}

// createWebSearchTool creates a simulated web search tool that returns
// realistic-looking (but fake) results. The large payload size is intentional —
// it demonstrates why Pensieve pruning is useful.
func createWebSearchTool() tool.CallableTool {
	return function.NewFunctionTool(
		func(_ context.Context, input WebSearchInput) (WebSearchOutput, error) {
			results := simulateSearch(input.Query)
			return WebSearchOutput{
				Query:   input.Query,
				Results: results,
				Total:   len(results),
			}, nil
		},
		function.WithName("web_search"),
		function.WithDescription(
			"Search the web for information on a topic. Returns a list of "+
				"results with title, URL, and snippet. Results can be large, "+
				"so consider using the note tool to save key findings and "+
				"delete_context to prune the raw results afterwards.",
		),
	)
}

// simulateSearch generates realistic-looking search results for common
// research topics. Each set is intentionally verbose to show context pressure.
func simulateSearch(query string) []WebSearchResult {
	q := strings.ToLower(query)

	switch {
	case strings.Contains(q, "transformer") || strings.Contains(q, "attention"):
		return []WebSearchResult{
			{
				Title:   "Attention Is All You Need - Original Paper",
				URL:     "https://arxiv.org/abs/1706.03762",
				Snippet: "The Transformer model architecture was introduced by Vaswani et al. in 2017. It relies entirely on self-attention mechanisms, dispensing with recurrence and convolution. The model achieves state-of-the-art results on machine translation benchmarks. The key innovation is the multi-head self-attention mechanism that allows the model to attend to information from different representation subspaces at different positions. The architecture consists of an encoder-decoder structure where both components use stacked self-attention and point-wise fully connected layers.",
			},
			{
				Title:   "Understanding Transformers: A Deep Dive",
				URL:     "https://jalammar.github.io/illustrated-transformer/",
				Snippet: "Transformers process input sequences in parallel rather than sequentially, which makes them much faster to train than RNNs or LSTMs. The self-attention mechanism computes attention scores between all pairs of positions in a sequence, creating a weighted representation. Key components include: positional encoding (since the model has no inherent notion of order), multi-head attention (allowing the model to jointly attend to different subspaces), layer normalization, and residual connections. Training typically uses the Adam optimizer with a custom learning rate schedule that includes a warm-up period.",
			},
			{
				Title:   "Transformer Architecture Variants",
				URL:     "https://lilianweng.github.io/posts/2023-01-27-transformers/",
				Snippet: "Since the original Transformer, many variants have been proposed: GPT (decoder-only, autoregressive), BERT (encoder-only, masked language modeling), T5 (encoder-decoder, text-to-text), Vision Transformer (ViT, applies transformers to image patches), and Sparse Transformers (using sparse attention patterns for efficiency). Recent advances include Flash Attention for memory-efficient attention computation, Rotary Position Embeddings (RoPE) for better length generalization, and Mixture of Experts (MoE) architectures that activate only a subset of parameters per input.",
			},
			{
				Title:   "Scaling Laws for Neural Language Models",
				URL:     "https://arxiv.org/abs/2001.08361",
				Snippet: "Research by Kaplan et al. at OpenAI showed that language model performance follows predictable power-law scaling with respect to model size, dataset size, and compute budget. This has been instrumental in guiding the development of large language models. The study found that larger models are more sample-efficient and that the optimal model size grows smoothly with compute budget. These scaling laws have been validated across multiple orders of magnitude and have influenced the development of GPT-3, GPT-4, and other frontier models.",
			},
			{
				Title:   "Efficient Transformers: A Survey",
				URL:     "https://arxiv.org/abs/2009.06732",
				Snippet: "The quadratic complexity of self-attention with respect to sequence length has motivated numerous efficient variants: Linformer (linear complexity via low-rank projections), Performer (kernel-based attention approximation), Longformer (combination of local and global attention), BigBird (sparse attention with random, window, and global tokens). These methods trade off some model quality for significant computational savings, enabling transformers to handle much longer sequences than the original architecture.",
			},
		}

	case strings.Contains(q, "climate") || strings.Contains(q, "carbon") || strings.Contains(q, "emission"):
		return []WebSearchResult{
			{
				Title:   "IPCC Sixth Assessment Report - Key Findings",
				URL:     "https://www.ipcc.ch/assessment-report/ar6/",
				Snippet: "The IPCC AR6 Synthesis Report (2023) confirms that human activities have unequivocally caused global warming of approximately 1.1°C above pre-industrial levels. Global greenhouse gas emissions continued to rise, reaching 59 ± 6.6 GtCO2-eq in 2019. The report emphasizes that deep, rapid, and sustained reductions in greenhouse gas emissions are necessary to limit warming to 1.5°C or 2°C. Key sectors for emission reduction include energy (switching to renewables), transport (electrification), industry (efficiency and carbon capture), and agriculture/land use changes.",
			},
			{
				Title:   "Global Carbon Budget 2024",
				URL:     "https://globalcarbonproject.org/carbonbudget/",
				Snippet: "Global CO2 emissions from fossil fuels reached 37.4 billion tonnes in 2024, a 0.8% increase from 2023. The top emitters are China (31%), USA (13%), India (8%), and EU (7%). Emissions from coal decreased by 1.2% globally but increased in India. Natural gas emissions grew by 2.4%. The remaining carbon budget for a 50% chance of limiting warming to 1.5°C is approximately 275 GtCO2, which at current rates would be exhausted in about 7 years. Cement production contributes an additional 1.7 billion tonnes of CO2.",
			},
			{
				Title:   "Renewable Energy Growth Trends",
				URL:     "https://www.irena.org/publications/2024/renewable-capacity",
				Snippet: "Global renewable energy capacity additions reached a record 473 GW in 2024, driven primarily by solar PV (346 GW) and wind (127 GW). Solar PV costs have fallen 90% since 2010, making it the cheapest source of electricity in most regions. China accounted for 63% of new solar installations. Battery storage capacity doubled year-over-year to reach 120 GWh. Despite rapid growth, renewables still need to triple by 2030 to align with the 1.5°C pathway. Investment in clean energy reached $1.8 trillion in 2024.",
			},
			{
				Title:   "Carbon Capture and Storage: Current State",
				URL:     "https://www.iea.org/reports/carbon-capture-utilisation-and-storage",
				Snippet: "As of 2024, there are 44 commercial CCS facilities operating globally, with a total capture capacity of 49 million tonnes CO2/year. This represents less than 0.2% of global emissions. The largest projects include Gorgon (Australia, 4 Mt/yr), Quest (Canada, 1.2 Mt/yr), and Sleipner (Norway, 1 Mt/yr). Direct Air Capture (DAC) technology is emerging but currently captures only 0.01 Mt/yr. The cost of DAC ranges from $250-600 per tonne of CO2, compared to $40-120 for point-source capture. Significant scale-up is needed for CCS to play a meaningful role in climate mitigation.",
			},
		}

	case strings.Contains(q, "quantum") || strings.Contains(q, "qubit"):
		return []WebSearchResult{
			{
				Title:   "Quantum Computing Progress in 2024-2025",
				URL:     "https://research.google/pubs/quantum-error-correction-milestone/",
				Snippet: "Google's Willow quantum processor demonstrated below-threshold quantum error correction for the first time in December 2024, using a 105-superconducting-qubit device. The logical error rate decreased exponentially as more physical qubits were added, a critical milestone for practical quantum computing. IBM reached 1,121 qubits with its Condor processor. Microsoft announced a topological qubit breakthrough. The current industry consensus is that millions of physical qubits will be needed for commercially useful fault-tolerant quantum computing, which is estimated to be 10-15 years away.",
			},
			{
				Title:   "Quantum Algorithms and Applications",
				URL:     "https://quantum-journal.org/views/qv-2024-algorithms/",
				Snippet: "Key quantum algorithms include Shor's algorithm (integer factoring, threatening RSA encryption), Grover's algorithm (quadratic speedup for unstructured search), VQE (variational quantum eigensolver for chemistry simulations), and QAOA (quantum approximate optimization). Near-term applications focus on quantum chemistry (drug discovery, materials science), optimization problems (logistics, finance), and quantum machine learning. However, most practical applications require error-corrected quantum computers that are not yet available.",
			},
			{
				Title:   "Post-Quantum Cryptography Standards",
				URL:     "https://csrc.nist.gov/pubs/fips/203/final",
				Snippet: "NIST finalized three post-quantum cryptography standards in August 2024: ML-KEM (FIPS 203, based on CRYSTALS-Kyber) for key encapsulation, ML-DSA (FIPS 204, based on CRYSTALS-Dilithium) for digital signatures, and SLH-DSA (FIPS 205, based on SPHINCS+) for hash-based signatures. Organizations are urged to begin migration planning immediately, as quantum computers capable of breaking RSA-2048 could emerge within 15-20 years. The transition is expected to take many years due to the pervasiveness of current cryptographic standards.",
			},
		}

	default:
		return []WebSearchResult{
			{
				Title:   fmt.Sprintf("Search results for: %s", query),
				URL:     fmt.Sprintf("https://example.com/search?q=%s", strings.ReplaceAll(query, " ", "+")),
				Snippet: fmt.Sprintf("This is a simulated search result for the query '%s'. In a production system, this tool would call a real search API (e.g., Google, Bing, DuckDuckGo) and return actual web results. The Pensieve context management tools help the agent manage the potentially large search result payloads by allowing it to distil key findings into notes and prune the raw results from context.", query),
			},
			{
				Title:   fmt.Sprintf("Understanding %s - Overview", query),
				URL:     fmt.Sprintf("https://example.com/wiki/%s", strings.ReplaceAll(query, " ", "_")),
				Snippet: fmt.Sprintf("A comprehensive overview of '%s' covering history, key concepts, current state of the art, and future directions. This topic has seen significant developments in recent years with implications across multiple domains including technology, science, and industry.", query),
			},
			{
				Title:   fmt.Sprintf("Latest Research on %s", query),
				URL:     fmt.Sprintf("https://example.com/research/%s", strings.ReplaceAll(query, " ", "-")),
				Snippet: fmt.Sprintf("Recent peer-reviewed research on '%s' published in 2024-2025. The field is rapidly evolving with new breakthroughs being reported regularly. Key challenges remain around scalability, cost-effectiveness, and real-world deployment. Several major institutions and companies have announced significant investments in advancing this area.", query),
			},
		}
	}
}

// ---------------------------------------------------------------------------
// Chat application
// ---------------------------------------------------------------------------

func main() {
	flag.Parse()

	fmt.Println("🧠 Pensieve Research Assistant")
	fmt.Printf("Model: %s | Streaming: %t | Auto: %t\n", *modelName, *streaming, *autoMode)
	fmt.Println(strings.Repeat("=", 60))
	fmt.Println()
	fmt.Println("This example demonstrates the Pensieve paradigm: the LLM actively")
	fmt.Println("manages its own context window using note, delete_context, check_budget,")
	fmt.Println("and read_notes tools alongside a web_search tool.")
	fmt.Println()

	chat := &pensieveChat{
		modelName: *modelName,
		streaming: *streaming,
		autoMode:  *autoMode,
	}

	if err := chat.run(); err != nil {
		log.Fatalf("Chat failed: %v", err)
	}
}

// pensieveChat manages the research-assistant conversation.
type pensieveChat struct {
	modelName string
	runner    runner.Runner
	userID    string
	sessionID string
	streaming bool
	autoMode  bool
}

func (c *pensieveChat) run() error {
	ctx := context.Background()
	if err := c.setup(ctx); err != nil {
		return fmt.Errorf("setup failed: %w", err)
	}
	defer c.runner.Close()
	if c.autoMode {
		return c.runAutoMode(ctx)
	}
	return c.startChat(ctx)
}

func (c *pensieveChat) setup(_ context.Context) error {
	modelInstance := openai.New(c.modelName)

	genConfig := model.GenerationConfig{
		MaxTokens:   intPtr(2000),
		Temperature: floatPtr(0.7),
		Stream:      c.streaming,
	}

	// Combine the simulated web search tool with the four Pensieve tools.
	allTools := append([]tool.Tool{createWebSearchTool()}, ctxtools.Tools()...)

	llmAgent := llmagent.New(
		"pensieve-research-agent",
		llmagent.WithModel(modelInstance),
		llmagent.WithDescription("A research assistant that manages its own context window"),
		llmagent.WithInstruction(pensieveInstruction),
		llmagent.WithGenerationConfig(genConfig),
		llmagent.WithTools(allTools),
		llmagent.WithAnnotateEventIDs(true),
	)

	appName := "pensieve-research-demo"
	c.runner = runner.NewRunner(appName, llmAgent)
	c.userID = "researcher"
	c.sessionID = fmt.Sprintf("research-%d", time.Now().Unix())

	fmt.Printf("✅ Research assistant ready! Session: %s\n\n", c.sessionID)
	return nil
}

func (c *pensieveChat) startChat(ctx context.Context) error {
	scanner := bufio.NewScanner(os.Stdin)

	fmt.Println("💡 Commands:")
	fmt.Println("   /exit  — End the session")
	fmt.Println()
	fmt.Println("💡 Try these research queries:")
	fmt.Println("   1. \"Research transformer architectures and attention mechanisms\"")
	fmt.Println("   2. \"Now research climate change and carbon emissions\"")
	fmt.Println("   3. \"Also look into quantum computing progress\"")
	fmt.Println("   4. \"Summarise all your findings so far\"  (triggers read_notes)")
	fmt.Println()

	for {
		fmt.Print("👤 You: ")
		if !scanner.Scan() {
			break
		}

		userInput := strings.TrimSpace(scanner.Text())
		if userInput == "" {
			continue
		}

		if strings.ToLower(userInput) == "/exit" {
			fmt.Println("👋 Goodbye!")
			return nil
		}

		if err := c.processMessage(ctx, userInput); err != nil {
			fmt.Printf("❌ Error: %v\n", err)
		}
		fmt.Println()
	}

	if err := scanner.Err(); err != nil {
		return fmt.Errorf("input scanner error: %w", err)
	}
	return nil
}

func (c *pensieveChat) processMessage(ctx context.Context, userMessage string) error {
	message := model.NewUserMessage(userMessage)
	eventChan, err := c.runner.Run(ctx, c.userID, c.sessionID, message)
	if err != nil {
		return fmt.Errorf("failed to run agent: %w", err)
	}
	_, err = c.processResponse(eventChan)
	return err
}

// runAutoMode executes the predefined autoQueries twice:
//  1. BASELINE — only web_search tool, no context management.
//  2. PENSIEVE — web_search + all four Pensieve tools.
//
// After both runs, it prints a comparison showing how Pensieve reduces
// visible context by masking raw search results after distilling notes.
func (c *pensieveChat) runAutoMode(ctx context.Context) error {
	fmt.Println("🤖 Running in AUTO mode — no human input required.")
	fmt.Printf("📋 Will execute %d predefined queries in two modes:\n", len(autoQueries))
	for i, q := range autoQueries {
		fmt.Printf("   %d. %q\n", i+1, q)
	}
	fmt.Println()

	// ── Phase 1: BASELINE (no Pensieve tools) ──────────────────────
	fmt.Println("╔══════════════════════════════════════════════════════════════╗")
	fmt.Println("║  PHASE 1: BASELINE — web_search only, no context management║")
	fmt.Println("╚══════════════════════════════════════════════════════════════╝")
	fmt.Println()

	baselineRunner, baselineSessionID, err := c.createRunner(false)
	if err != nil {
		return fmt.Errorf("baseline setup failed: %w", err)
	}
	defer baselineRunner.Close()

	var baselineStats runStats
	// Run only the first 3 research queries for baseline (skip summarise).
	researchQueries := autoQueries[:3]
	for i, query := range researchQueries {
		fmt.Printf("━━━ Baseline Query %d/%d ━━━\n", i+1, len(researchQueries))
		fmt.Printf("👤 You: %s\n", query)
		msg := model.NewUserMessage(query)
		eventChan, runErr := baselineRunner.Run(ctx, c.userID, baselineSessionID, msg)
		if runErr != nil {
			fmt.Printf("❌ Error: %v\n", runErr)
			baselineStats.errors++
			continue
		}
		s, procErr := c.processResponse(eventChan)
		baselineStats.add(s)
		if procErr != nil {
			fmt.Printf("❌ Error: %v\n", procErr)
			baselineStats.errors++
		}
		fmt.Println()
	}

	// ── Phase 2: PENSIEVE (with context management) ────────────────
	fmt.Println()
	fmt.Println("╔══════════════════════════════════════════════════════════════╗")
	fmt.Println("║  PHASE 2: PENSIEVE — web_search + context management tools ║")
	fmt.Println("╚══════════════════════════════════════════════════════════════╝")
	fmt.Println()

	var pensieveStats runStats
	// The main runner (c.runner) was already set up with Pensieve tools.
	for i, query := range autoQueries {
		fmt.Printf("━━━ Pensieve Query %d/%d ━━━\n", i+1, len(autoQueries))
		fmt.Printf("👤 You: %s\n", query)
		msg := model.NewUserMessage(query)
		eventChan, runErr := c.runner.Run(ctx, c.userID, c.sessionID, msg)
		if runErr != nil {
			fmt.Printf("❌ Error: %v\n", runErr)
			pensieveStats.errors++
			continue
		}
		s, procErr := c.processResponse(eventChan)
		pensieveStats.add(s)
		if procErr != nil {
			fmt.Printf("❌ Error: %v\n", procErr)
			pensieveStats.errors++
		}
		fmt.Println()
	}

	// ── Comparison ─────────────────────────────────────────────────
	fmt.Println("╔══════════════════════════════════════════════════════════════╗")
	fmt.Println("║                    COMPARISON RESULTS                       ║")
	fmt.Println("╚══════════════════════════════════════════════════════════════╝")
	fmt.Println()
	fmt.Printf("  %-30s %12s %12s\n", "Metric", "Baseline", "Pensieve")
	fmt.Printf("  %-30s %12s %12s\n", strings.Repeat("─", 30), strings.Repeat("─", 12), strings.Repeat("─", 12))
	fmt.Printf("  %-30s %12d %12d\n", "Queries", len(researchQueries), len(autoQueries))
	fmt.Printf("  %-30s %12d %12d\n", "Total events streamed", baselineStats.events, pensieveStats.events)
	fmt.Printf("  %-30s %12d %12d\n", "Tool calls made", baselineStats.toolCalls, pensieveStats.toolCalls)
	fmt.Printf("  %-30s %12d %12d\n", "Tool responses received", baselineStats.toolResponses, pensieveStats.toolResponses)
	fmt.Printf("  %-30s %12d %12d\n", "Events masked (pruned)", 0, pensieveStats.eventsMasked)
	fmt.Printf("  %-30s %12d %12d\n", "Notes saved", 0, pensieveStats.notesSaved)
	fmt.Printf("  %-30s %12d %12d\n", "Tool result chars received", baselineStats.toolResultChars, pensieveStats.toolResultChars)
	fmt.Printf("  %-30s %12d %12d\n", "Errors", baselineStats.errors, pensieveStats.errors)
	fmt.Println()
	if pensieveStats.eventsMasked > 0 {
		fmt.Printf("  💡 Pensieve pruned %d events, keeping context lean while\n", pensieveStats.eventsMasked)
		fmt.Println("     saving key findings in persistent notes.")
	}
	fmt.Println()
	fmt.Println("━━━ AUTO MODE COMPLETE ━━━")
	return nil
}

// runStats tracks numeric stats from processResponse for comparison.
type runStats struct {
	events          int // total events seen in the stream
	toolCalls       int // number of tool calls
	toolResponses   int // number of tool responses
	eventsMasked    int // number of events masked (from delete_context results)
	notesSaved      int // number of notes saved
	toolResultChars int // total characters in tool results
	errors          int
}

func (s *runStats) add(other runStats) {
	s.events += other.events
	s.toolCalls += other.toolCalls
	s.toolResponses += other.toolResponses
	s.eventsMasked += other.eventsMasked
	s.notesSaved += other.notesSaved
	s.toolResultChars += other.toolResultChars
	s.errors += other.errors
}

// createRunner creates a new agent+runner pair. When withPensieve is true,
// it includes the four context-management tools; otherwise only web_search.
func (c *pensieveChat) createRunner(withPensieve bool) (runner.Runner, string, error) {
	modelInstance := openai.New(c.modelName)
	genConfig := model.GenerationConfig{
		MaxTokens:   intPtr(2000),
		Temperature: floatPtr(0.7),
		Stream:      c.streaming,
	}

	tools := []tool.Tool{
		createWebSearchTool(),
	}
	var instruction string
	var agentName, appName string

	if withPensieve {
		tools = append(tools, ctxtools.Tools()...)
		instruction = pensieveInstruction
		agentName = "pensieve-research-agent"
		appName = "pensieve-research-demo"
	} else {
		instruction = `You are a research assistant. Use web_search to find information on topics the user asks about. Summarise your findings clearly.`
		agentName = "baseline-research-agent"
		appName = "baseline-research-demo"
	}

	llmAgent := llmagent.New(
		agentName,
		llmagent.WithModel(modelInstance),
		llmagent.WithDescription("A research assistant"),
		llmagent.WithInstruction(instruction),
		llmagent.WithGenerationConfig(genConfig),
		llmagent.WithTools(tools),
		llmagent.WithAnnotateEventIDs(withPensieve),
	)

	r := runner.NewRunner(appName, llmAgent)
	sessionID := fmt.Sprintf("%s-%d", agentName, time.Now().Unix())
	fmt.Printf("✅ %s ready! Session: %s\n\n", agentName, sessionID)
	return r, sessionID, nil
}

// ---------------------------------------------------------------------------
// Response / event processing
// ---------------------------------------------------------------------------

func (c *pensieveChat) processResponse(eventChan <-chan *event.Event) (runStats, error) {
	fmt.Print("🤖 Assistant: ")

	var (
		stats            runStats
		fullContent      string
		toolCallDetected bool
		assistantStarted bool
	)

	for evt := range eventChan {
		stats.events++

		if evt.Error != nil {
			if evt.Error.Type == agent.ErrorTypeStopAgentError {
				fmt.Printf("\n🛑 Agent stopped: %s\n", evt.Error.Message)
				return stats, agent.NewStopError(evt.Error.Message)
			}
			fmt.Printf("\n❌ Error: %s\n", evt.Error.Message)
			stats.errors++
			continue
		}

		if c.handleToolCalls(evt, &toolCallDetected, &assistantStarted, &stats) {
			continue
		}
		if c.handleToolResponses(evt, &stats) {
			continue
		}
		c.processStreamingContent(evt, &toolCallDetected, &assistantStarted, &fullContent)

		if evt.IsFinalResponse() {
			fmt.Println()
			break
		}
	}
	return stats, nil
}

func (c *pensieveChat) handleToolCalls(
	evt *event.Event,
	toolCallDetected *bool,
	assistantStarted *bool,
	stats *runStats,
) bool {
	if len(evt.Response.Choices) == 0 || len(evt.Response.Choices[0].Message.ToolCalls) == 0 {
		return false
	}
	*toolCallDetected = true
	if *assistantStarted {
		fmt.Println()
	}
	fmt.Println("🔧 Tool calls:")
	for _, tc := range evt.Response.Choices[0].Message.ToolCalls {
		stats.toolCalls++
		icon := toolIcon(tc.Function.Name)
		fmt.Printf("   %s %s (ID: %s)\n", icon, tc.Function.Name, tc.ID)
		if len(tc.Function.Arguments) > 0 {
			fmt.Printf("     Args: %s\n", string(tc.Function.Arguments))
		}
	}
	fmt.Println("⚡ Executing...")
	return true
}

func (c *pensieveChat) handleToolResponses(evt *event.Event, stats *runStats) bool {
	if evt.Response == nil || len(evt.Response.Choices) == 0 {
		return false
	}
	handled := false
	for _, choice := range evt.Response.Choices {
		if choice.Message.Role == model.RoleTool && choice.Message.ToolID != "" {
			result := choice.Message.Content
			stats.toolResponses++
			stats.toolResultChars += len(result)

			// Parse stats from known tool outputs.
			if strings.Contains(result, `"masked":`) {
				var v struct {
					Masked int `json:"masked"`
				}
				if json.Unmarshal([]byte(result), &v) == nil {
					stats.eventsMasked += v.Masked
				}
			}
			if strings.Contains(result, `saved`) && strings.Contains(result, `note`) {
				stats.notesSaved++
			}

			if len(result) > 300 {
				result = result[:300] + "…(truncated)"
			}
			fmt.Printf("✅ Tool result (ID: %s): %s\n",
				choice.Message.ToolID, strings.TrimSpace(result))
			handled = true
		}
	}
	return handled
}

func (c *pensieveChat) processStreamingContent(
	evt *event.Event,
	toolCallDetected *bool,
	assistantStarted *bool,
	fullContent *string,
) {
	if len(evt.Response.Choices) == 0 {
		return
	}
	delta := evt.Response.Choices[0].Delta.Content
	if delta == "" {
		return
	}
	if !*assistantStarted {
		if *toolCallDetected {
			fmt.Print("\n🤖 Assistant: ")
		}
		*assistantStarted = true
	}
	fmt.Print(delta)
	*fullContent += delta
}

func toolIcon(name string) string {
	switch name {
	case "web_search":
		return "🔍"
	case "delete_context":
		return "🗑️"
	case "check_budget":
		return "📊"
	case "note":
		return "📝"
	case "read_notes":
		return "📖"
	default:
		return "🔧"
	}
}

func intPtr(i int) *int           { return &i }
func floatPtr(f float64) *float64 { return &f }
