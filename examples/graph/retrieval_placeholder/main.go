//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package main demonstrates a simple two‑node graph: Retrieve → LLM.
// The retrieval node writes ephemeral data into the session's temp namespace,
// and the LLM node uses placeholders to inject that data into its instruction.
package main

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/agent/graphagent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/graph"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/model/openai"
	"trpc.group/trpc-go/trpc-agent-go/runner"
	"trpc.group/trpc-go/trpc-agent-go/session"
	"trpc.group/trpc-go/trpc-agent-go/session/inmemory"
)

const (
	appName          = "graph-retrieval-placeholder"
	defaultModelName = "deepseek-chat"
)

func main() {
	modelName := flag.String("model", defaultModelName, "Model name to use")
	flag.Parse()

	fmt.Println("🔎 Graph Retrieval → LLM (Placeholder Injection)")
	fmt.Printf("Model: %s\n", *modelName)
	fmt.Println("Type 'exit' to quit")
	fmt.Println(strings.Repeat("=", 60))

	if err := run(context.Background(), *modelName); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, modelName string) error {
	// Session service
	svc := inmemory.NewSessionService()

	// Create a session with no prefilled state (we'll write temp: keys per turn).
	userID := "user"
	sessionID := fmt.Sprintf("sess-%d", time.Now().Unix())
	if _, err := svc.CreateSession(ctx, session.Key{AppName: appName, UserID: userID, SessionID: sessionID}, session.StateMap{}); err != nil {
		return fmt.Errorf("create session failed: %w", err)
	}

	// Build graph: retrieve → llm.
	mdl := openai.New(modelName)
	sg := graph.NewStateGraph(graph.MessagesStateSchema())

	// 1) Retrieval node: simulate recall and write into session.State as temp keys.
	sg.AddNode("retrieve", func(ctx context.Context, st graph.State) (any, error) {
		// Get the current user input from graph state.
		var input string
		if v, ok := st[graph.StateKeyUserInput]; ok {
			if s, ok := v.(string); ok {
				input = strings.TrimSpace(s)
			}
		}

		// Simulate a small retrieval based on input.
		// In real code, call your vector store or search tool here.
		snippets := fakeRetrieve(input)
		contextText := strings.Join(snippets, "\n• ")
		if contextText != "" {
			contextText = "• " + contextText
		}

		// Write ephemeral keys into the session temp namespace so LLM placeholders can read them.
		if sessVal, ok := st[graph.StateKeySession]; ok {
			if sess, ok := sessVal.(*session.Session); ok && sess != nil {
				if sess.State == nil {
					sess.State = make(session.StateMap)
				}
				sess.State[session.StateTempPrefix+"retrieved_context"] = []byte(contextText)
				sess.State[session.StateTempPrefix+"user_input"] = []byte(input)
			}
		}

		// No graph state changes are strictly necessary for placeholder injection.
		// Return an empty update to keep semantics clear.
		return graph.State{}, nil
	})

	// 2) LLM node: instruction uses placeholders from session state (temp namespace).
	instruction := strings.Join([]string{
		"You are a helpful assistant using RAG (retrieval‑augmented generation).",
		"Use the retrieved context to answer the user's question.",
		"Context:\n{temp:retrieved_context}",
		"Question: {temp:user_input}",
		"Provide a clear, factual answer. If context is missing, say so briefly.",
	}, "\n\n")

	sg.AddLLMNode("answer", mdl, instruction, nil)

	// Wire: retrieve → answer
	sg.SetEntryPoint("retrieve").SetFinishPoint("answer")
	compiled, err := sg.Compile()
	if err != nil {
		return fmt.Errorf("compile graph failed: %w", err)
	}

	// Agent + Runner
	ga, err := graphagent.New("retrieval-placeholder", compiled)
	if err != nil {
		return fmt.Errorf("create graph agent failed: %w", err)
	}
	r := runner.NewRunner(appName, ga, runner.WithSessionService(svc))

	// Ensure runner resources are cleaned up (trpc-agent-go >= v0.5.0)
	defer r.Close()

	fmt.Printf("✅ Session ready: %s\n", sessionID)

	// Interactive loop
	scanner := bufio.NewScanner(os.Stdin)
	for {
		fmt.Print("👤 You: ")
		if !scanner.Scan() {
			break
		}
		text := strings.TrimSpace(scanner.Text())
		if text == "" {
			continue
		}
		if strings.EqualFold(text, "exit") {
			fmt.Println("👋 Goodbye!")
			return nil
		}

		msg := model.NewUserMessage(text)
		ch, err := r.Run(ctx, userID, sessionID, msg)
		if err != nil {
			fmt.Printf("❌ run failed: %v\n", err)
			continue
		}
		if err := stream(ch); err != nil {
			fmt.Printf("❌ stream error: %v\n", err)
		}
		fmt.Println()
	}
	return scanner.Err()
}

// fakeRetrieve simulates retrieval results for demonstration purposes.
func fakeRetrieve(query string) []string {
	q := strings.ToLower(query)
	switch {
	case strings.Contains(q, "quantum"):
		return []string{
			"Quantum error correction protects information from decoherence.",
			"Surface codes are leading candidates for scalable architectures.",
		}
	case strings.Contains(q, "ai") || strings.Contains(q, "machine learning"):
		return []string{
			"Transformer architectures dominate state‑of‑the‑art NLP.",
			"Retrieval‑augmented generation combines parametric and non‑parametric memory.",
		}
	default:
		return []string{"No matching documents were found in the toy corpus."}
	}
}

// stream prints streaming events nicely.
func stream(ch <-chan *event.Event) error {
	var started bool
	for ev := range ch {
		if ev == nil {
			continue
		}
		if ev.Error != nil {
			fmt.Printf("\n❌ Error: %s\n", ev.Error.Message)
			continue
		}
		if len(ev.Choices) > 0 {
			delta := ev.Choices[0].Delta.Content
			if delta != "" {
				if !started {
					fmt.Print("🤖 Answer: ")
					started = true
				}
				fmt.Print(delta)
			}
		}
		if ev.Done && ev.Response != nil && ev.Response.Object == model.ObjectTypeRunnerCompletion && started {
			fmt.Println()
			started = false
		}
	}
	return nil
}
