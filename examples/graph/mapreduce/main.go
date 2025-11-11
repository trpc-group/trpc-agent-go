//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package main demonstrates a Map-Reduce workflow over a document:
// 1) chunk the document, 2) retrieve top-K relevant chunks for a question,
// 3) summarize selected chunks in parallel, and 4) join partial summaries.
package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/agent/graphagent"
	"trpc.group/trpc-go/trpc-agent-go/graph"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/model/openai"
	"trpc.group/trpc-go/trpc-agent-go/runner"
	"trpc.group/trpc-go/trpc-agent-go/session/inmemory"
)

const (
	defaultModelName = "deepseek-chat"

	// Schema keys
	keyDocText          = "doc_text"
	keyChunks           = "chunks"
	keySelected         = "selected_chunks"
	keySelectedCount    = "selected_count"
	keyPartialSummaries = "partial_summaries"
	keyFinalAnswer      = "final_answer"
)

var (
	flagModel     = flag.String("model", defaultModelName, "Model name to use for LLM nodes")
	flagFile      = flag.String("file", "./sample.txt", "Path to the input document (text)")
	flagChunkSize = flag.Int("chunk-size", 800, "Chunk size (characters)")
	flagOverlap   = flag.Int("overlap", 100, "Chunk overlap (characters)")
	flagTopK      = flag.Int("top-k", 4, "Top K chunks to summarize in parallel")
	flagVerbose   = flag.Bool("verbose", false, "Verbose node/model events")
)

func main() {
	flag.Parse()
	fmt.Println("🗺️  Map-Reduce Document QA Example")
	fmt.Printf("Model: %s\n", *flagModel)
	fmt.Printf("File : %s\n", absOrSelf(*flagFile))
	fmt.Println(strings.Repeat("=", 56))
	demo := &mapReduceDemo{
		modelName:   *flagModel,
		chunkSize:   *flagChunkSize,
		overlap:     *flagOverlap,
		topK:        *flagTopK,
		verboseLogs: *flagVerbose,
	}
	if err := demo.setup(context.Background()); err != nil {
		fmt.Fprintf(os.Stderr, "setup failed: %v\n", err)
		os.Exit(1)
	}

	// Ensure runner resources are cleaned up (trpc-agent-go >= v0.5.0)
	defer demo.runner.Close()

	if err := demo.runInteractive(context.Background()); err != nil {
		fmt.Fprintf(os.Stderr, "run failed: %v\n", err)
		os.Exit(1)
	}
}

// mapReduceDemo holds runtime config and shared components.
type mapReduceDemo struct {
	modelName   string
	chunkSize   int
	overlap     int
	topK        int
	verboseLogs bool

	docText string

	runner    runner.Runner
	userID    string
	sessionID string
}

func (d *mapReduceDemo) setup(_ context.Context) error {
	// Load document text eagerly so node functions can reference it.
	text, err := os.ReadFile(*flagFile)
	if err != nil {
		return fmt.Errorf("failed to read file %s: %w", *flagFile, err)
	}
	d.docText = string(text)

	// Build the graph.
	g, err := d.buildGraph()
	if err != nil {
		return err
	}

	// Create GraphAgent.
	ga, err := graphagent.New(
		"map-reduce-agent",
		g,
		graphagent.WithDescription("Document chunk → retrieve → parallel summarize → reduce join"),
	)
	if err != nil {
		return fmt.Errorf("create graph agent: %w", err)
	}

	// Session service + runner
	sessSvc := inmemory.NewSessionService()
	d.runner = runner.NewRunner(
		"map-reduce-demo",
		ga,
		runner.WithSessionService(sessSvc),
	)
	d.userID = "user"
	d.sessionID = fmt.Sprintf("mr-%d", time.Now().Unix())
	fmt.Printf("✅ Ready. Session: %s\n\n", d.sessionID)
	return nil
}

// buildGraph constructs the Map-Reduce graph.
func (d *mapReduceDemo) buildGraph() (*graph.Graph, error) {
	// Base schema with message semantics + our fields.
	schema := graph.MessagesStateSchema().
		AddField(keyDocText, graph.StateField{Type: reflect.TypeOf(""), Reducer: graph.DefaultReducer, Default: func() any { return "" }}).
		AddField(keyChunks, graph.StateField{Type: reflect.TypeOf([]string{}), Reducer: graph.StringSliceReducer, Default: func() any { return []string{} }}).
		AddField(keySelected, graph.StateField{Type: reflect.TypeOf([]map[string]any{}), Reducer: appendMapSliceReducer, Default: func() any { return []map[string]any{} }}).
		AddField(keySelectedCount, graph.StateField{Type: reflect.TypeOf(0), Reducer: graph.DefaultReducer, Default: func() any { return 0 }}).
		AddField(keyPartialSummaries, graph.StateField{Type: reflect.TypeOf([]string{}), Reducer: graph.StringSliceReducer, Default: func() any { return []string{} }}).
		AddField(keyFinalAnswer, graph.StateField{Type: reflect.TypeOf(""), Reducer: graph.DefaultReducer, Default: func() any { return "" }})

	mdl := openai.New(d.modelName)

	sg := graph.NewStateGraph(schema)

	// 1) Load + chunk
	sg.AddNode("load_and_chunk", d.nodeLoadAndChunk)

	// 2) Retrieve top‑K for the query in user_input
	sg.AddNode("retrieve", d.nodeRetrieve)

	// 3) Fan‑out create commands to map_summarize with per‑chunk input
	sg.AddNode("create_map_tasks", d.nodeCreateMapTasks)

	// 3b) Map summarize each chunk with the question context
	sg.AddLLMNode("map_summarize", mdl, `You are a focused document chunk summarizer.
Your goal: Extract only information from the chunk that helps answer the question.
Rules:
- If the chunk is not relevant, respond with: N/A
- Be concise: return 3–6 bullet points or a short paragraph
- Do not invent information beyond the chunk
`, nil)

	// 3c) Collect each partial summary into keyPartialSummaries
	sg.AddNode("collect_partial", d.nodeCollectPartial)

	// 4) Prepare reduce prompt using collected partials and the question
	sg.AddNode("prepare_reduce", d.nodePrepareReduce)

	// 4b) LLM final join
	sg.AddLLMNode("reduce_join", mdl, `You are a synthesis expert.
You will receive:
- A user question
- Multiple partial summaries, each from a different document chunk

Task:
- Integrate the partial summaries into a single, coherent answer to the question
- Remove duplicates, resolve inconsistencies conservatively
- Include only facts supported by the partials
- Be concise and actionable
`, nil)

	// 5) Finish node formats the final output
	sg.AddNode("finish", d.nodeFinish)

	// Wiring
	sg.SetEntryPoint("load_and_chunk").SetFinishPoint("finish")
	sg.AddEdge("load_and_chunk", "retrieve")
	sg.AddEdge("retrieve", "create_map_tasks")
	sg.AddEdge("create_map_tasks", "map_summarize")
	sg.AddEdge("map_summarize", "collect_partial")

	// Barrier: proceed to prepare_reduce only when all K partials are collected
	cond := func(ctx context.Context, state graph.State) (string, error) {
		want, _ := state[keySelectedCount].(int)
		got := 0
		if arr, ok := state[keyPartialSummaries].([]string); ok {
			got = len(arr)
		}
		if got >= want && want > 0 {
			return "prepare_reduce", nil
		}
		return graph.End, nil
	}
	sg.AddConditionalEdges("collect_partial", cond, map[string]string{
		"prepare_reduce": "prepare_reduce",
		graph.End:        graph.End,
	})

	// After prepare_reduce, call reduce then finish.
	sg.AddEdge("prepare_reduce", "reduce_join")
	sg.AddEdge("reduce_join", "finish")

	return sg.Compile()
}

// nodeLoadAndChunk splits the preloaded document into overlapping chunks.
func (d *mapReduceDemo) nodeLoadAndChunk(ctx context.Context, state graph.State) (any, error) {
	text := strings.TrimSpace(d.docText)
	if text == "" {
		return nil, errors.New("document text is empty; provide a non-empty -file")
	}
	chunks := chunkText(text, d.chunkSize, d.overlap)
	return graph.State{
		keyDocText: text,
		keyChunks:  chunks,
	}, nil
}

// nodeRetrieve selects top‑K chunks by simple lexical scoring against the question in user_input.
func (d *mapReduceDemo) nodeRetrieve(ctx context.Context, state graph.State) (any, error) {
	question, _ := state[graph.StateKeyUserInput].(string)
	if question == "" {
		return nil, errors.New("no query in user_input; enter your question in interactive prompt")
	}
	chunks, _ := state[keyChunks].([]string)
	if len(chunks) == 0 {
		return nil, errors.New("no chunks to retrieve from")
	}
	type scored struct {
		Idx   int
		Score float64
	}
	scores := make([]scored, len(chunks))
	for i, c := range chunks {
		scores[i] = scored{Idx: i, Score: scoreChunk(question, c)}
	}
	sort.Slice(scores, func(i, j int) bool { return scores[i].Score > scores[j].Score })
	k := d.topK
	if k > len(scores) {
		k = len(scores)
	}
	selected := make([]map[string]any, 0, k)
	for i := 0; i < k; i++ {
		idx := scores[i].Idx
		selected = append(selected, map[string]any{
			"index": idx,
			"score": scores[i].Score,
			"text":  chunks[idx],
		})
	}
	return graph.State{
		keySelected:      selected,
		keySelectedCount: len(selected),
	}, nil
}

// nodeCreateMapTasks fans out commands to map_summarize with per-chunk prompts.
func (d *mapReduceDemo) nodeCreateMapTasks(ctx context.Context, state graph.State) (any, error) {
	question, _ := state[graph.StateKeyUserInput].(string)
	sel, _ := state[keySelected].([]map[string]any)
	if len(sel) == 0 {
		return nil, errors.New("no selected chunks; ensure retrieval produced results")
	}
	cmds := make([]*graph.Command, 0, len(sel))
	for i, s := range sel {
		idx, _ := s["index"].(int)
		text, _ := s["text"].(string)
		// Build per-task user input for the LLM summarizer node.
		userInput := fmt.Sprintf("Question: %s\n\nChunk #%d:\n%s", question, idx, text)
		cmds = append(cmds, &graph.Command{
			Update: graph.State{
				graph.StateKeyUserInput: userInput,
				"map_task_index":        i,
				"chunk_index":           idx,
			},
			GoTo: "map_summarize",
		})
	}
	return cmds, nil
}

// nodeCollectPartial appends the last_response from map_summarize into partial_summaries.
func (d *mapReduceDemo) nodeCollectPartial(ctx context.Context, state graph.State) (any, error) {
	resp, _ := state[graph.StateKeyLastResponse].(string)
	if strings.TrimSpace(resp) == "" {
		resp = "N/A"
	}
	return graph.State{
		keyPartialSummaries: []string{resp},
	}, nil
}

// nodePrepareReduce creates a single user_input for the reduce LLM that contains
// the original question and each partial summary.
func (d *mapReduceDemo) nodePrepareReduce(ctx context.Context, state graph.State) (any, error) {
	question, _ := state[graph.StateKeyUserInput].(string)
	partials, _ := state[keyPartialSummaries].([]string)
	if len(partials) == 0 {
		return nil, errors.New("no partial summaries collected")
	}
	var b strings.Builder
	b.WriteString("Question: ")
	b.WriteString(strings.TrimSpace(question))
	b.WriteString("\n\nPartial Summaries:\n")
	for i, p := range partials {
		b.WriteString(fmt.Sprintf("[%d] %s\n", i+1, strings.TrimSpace(p)))
	}
	b.WriteString("\nSynthesize a final answer.")
	return graph.State{graph.StateKeyUserInput: b.String()}, nil
}

// nodeFinish exposes a consistent final message for the CLI.
func (d *mapReduceDemo) nodeFinish(ctx context.Context, state graph.State) (any, error) {
	// Prefer reduce_join output; fallback to last_response.
	if v, ok := state[graph.StateKeyNodeResponses].(map[string]any); ok {
		if ans, ok2 := v["reduce_join"].(string); ok2 && strings.TrimSpace(ans) != "" {
			return graph.State{graph.StateKeyLastResponse: ans, keyFinalAnswer: ans}, nil
		}
	}
	if last, ok := state[graph.StateKeyLastResponse].(string); ok && last != "" {
		return graph.State{keyFinalAnswer: last}, nil
	}
	return nil, errors.New("no final answer found")
}

// Interactive shell: read question and run once per line.
func (d *mapReduceDemo) runInteractive(ctx context.Context) error {
	fmt.Println("💡 Enter your question about the loaded document (or 'exit')")
	in := bufio.NewScanner(os.Stdin)
	for {
		fmt.Print("❓ Question: ")
		if !in.Scan() {
			break
		}
		q := strings.TrimSpace(in.Text())
		if q == "" {
			continue
		}
		if strings.EqualFold(q, "exit") {
			fmt.Println("👋 Goodbye!")
			return nil
		}
		if err := d.runOnce(ctx, q); err != nil {
			fmt.Printf("❌ Error: %v\n\n", err)
		}
	}
	return in.Err()
}

func (d *mapReduceDemo) runOnce(ctx context.Context, question string) error {
	msg := model.NewUserMessage(question)
	// Seed runtime state (doc_text is set by nodeLoadAndChunk from d.docText).
	evCh, err := d.runner.Run(ctx, d.userID, d.sessionID, msg, agent.WithRuntimeState(map[string]any{}))
	if err != nil {
		return err
	}
	// Minimal event loop: print final answer when done.
	var final string
	for e := range evCh {
		if e.Error != nil {
			return fmt.Errorf("graph error: %s", e.Error.Message)
		}
		if e.Done && e.StateDelta != nil {
			// Try reduce_join output first.
			if b, ok := e.StateDelta[graph.StateKeyNodeResponses]; ok && len(b) > 0 {
				var m map[string]any
				_ = jsonUnmarshal(b, &m)
				if v, ok2 := m["reduce_join"].(string); ok2 && v != "" {
					final = v
				}
			}
			if final == "" {
				if b, ok := e.StateDelta[graph.StateKeyLastResponse]; ok && len(b) > 0 {
					_ = jsonUnmarshal(b, &final)
				}
			}
		}
	}
	if final == "" {
		return errors.New("no final answer produced")
	}
	fmt.Println()
	fmt.Println("🧠 Final Answer:")
	fmt.Println(strings.Repeat("-", 56))
	fmt.Println(final)
	fmt.Println(strings.Repeat("-", 56))
	fmt.Println()
	return nil
}

// Helpers

// chunkText splits text into overlapping chunks of size chunkSize with given overlap.
func chunkText(text string, chunkSize, overlap int) []string {
	if chunkSize <= 0 {
		chunkSize = 800
	}
	if overlap < 0 {
		overlap = 0
	}
	runes := []rune(text)
	n := len(runes)
	if n == 0 {
		return nil
	}
	if chunkSize > n {
		return []string{string(runes)}
	}
	// Advance by step = chunkSize - overlap
	step := chunkSize - overlap
	if step <= 0 {
		step = chunkSize
	}
	var chunks []string
	for start := 0; start < n; start += step {
		end := start + chunkSize
		if end > n {
			end = n
		}
		chunks = append(chunks, string(runes[start:end]))
		if end == n {
			break
		}
	}
	return chunks
}

// scoreChunk computes a simple lexical score for how well chunk matches question.
func scoreChunk(question, chunk string) float64 {
	// Lower‑case, split into alphanumerics, compute overlap count.
	qTokens := tokenize(question)
	if len(qTokens) == 0 {
		return 0
	}
	cTokens := tokenize(chunk)
	if len(cTokens) == 0 {
		return 0
	}
	qset := map[string]struct{}{}
	for _, t := range qTokens {
		if t == "" {
			continue
		}
		qset[t] = struct{}{}
	}
	count := 0
	for _, t := range cTokens {
		if _, ok := qset[t]; ok {
			count++
		}
	}
	// Normalize by sqrt length to dampen long chunks effect.
	denom := 1 + len(cTokens)
	return float64(count) / float64(denom)
}

func tokenize(s string) []string {
	s = strings.ToLower(s)
	// Replace non‑letters/digits with space
	b := strings.Builder{}
	for _, r := range s {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == ' ' {
			b.WriteRune(r)
		} else {
			b.WriteByte(' ')
		}
	}
	// Collapse spaces
	f := strings.Fields(b.String())
	return f
}

func absOrSelf(p string) string {
	if a, err := filepath.Abs(p); err == nil {
		return a
	}
	return p
}

// Small helpers to avoid import cycles with examples
func appendMapSliceReducer(existing, update any) any {
	if existing == nil {
		existing = []map[string]any{}
	}
	ex, ok1 := existing.([]map[string]any)
	up, ok2 := update.([]map[string]any)
	if !ok1 || !ok2 {
		return update
	}
	return append(ex, up...)
}

// Minimal JSON unmarshal from []byte to T without adding a new dependency here.
func jsonUnmarshal(data []byte, out any) error { return json.Unmarshal(data, out) }
