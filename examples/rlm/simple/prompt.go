//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package main

import "fmt"

// MaxFanOut is the hard ceiling on sub-agents per rlm_query_batched call.
// Enforced both in the prompt (guidance) and in code (batchCallRLM rejects excess).
const MaxFanOut = 10

// BuildSystemPrompt generates the system prompt for an RLM agent.
// All agents (root or child, any depth) share the same prompt structure.
// The LLM decides its own decomposition strategy based on context size and content.
func BuildSystemPrompt(contextLen, contextLines, depth, maxDepth int) string {
	canRecurse := depth < maxDepth

	toolSection := buildToolSection(canRecurse)
	strategySection := buildStrategySection(contextLen, depth, canRecurse)

	return fmt.Sprintf(`You are a Recursive Language Model (RLM) agent.
Your task is to answer a query about a large external context that you access via code.

RUNTIME STATUS:
- Context: %d characters, %d lines (loaded as 'context' in the REPL)
- Recursion depth: %d / %d (max)
- Remaining recursion levels: %d
%s%s
STARLARK NOTES (the REPL language):
- Python subset: no while (use for+range), no try/except, no import, no f-strings
- Variables persist across execute_code calls — use them to accumulate results
- Top-level for-loops are not allowed; wrap them in a function: def f(): ...; f()
- String slicing: context[0:5000], context.split("\n"), len(context), etc.
`, contextLen, contextLines, depth, maxDepth, maxDepth-depth,
		toolSection, strategySection)
}

func buildToolSection(canRecurse bool) string {
	base := `
YOUR TOOLS:
1. execute_code — Run Starlark code in the REPL. The REPL has these builtins:
   • context                         — the full context string (your data)
   • llm_query(prompt)               — call LLM once, returns string
   • llm_query_batched(prompt_list)   — call LLM in PARALLEL, returns list of strings
   • print(...)                      — output to stdout (visible in tool result)`

	if canRecurse {
		base += fmt.Sprintf(`
   • rlm_query(query, ctx)           — spawn a child RLM agent on a sub-context
   • rlm_query_batched(queries, ctxs) — spawn PARALLEL child agents (max %d per call)
   IMPORTANT: To delegate work to child agents, you MUST use execute_code and call
   rlm_query() or rlm_query_batched() inside Starlark. Pass context slices as variables,
   e.g.: chunk = context[0:50000]; result = rlm_query(query="...", context=chunk)

2. final_answer — Submit your answer and stop.`, MaxFanOut)
	} else {
		base += `

2. final_answer — Submit your answer and stop.

NOTE: You are at max recursion depth. You cannot spawn child agents.
Use llm_query / llm_query_batched for analysis instead.`
	}
	return base
}

func buildStrategySection(contextLen, depth int, canRecurse bool) string {
	s := fmt.Sprintf(`

STRATEGY GUIDANCE:
Your context is %d chars. You have full autonomy to decide how to process it.

`, contextLen)

	if canRecurse && contextLen > 50000 {
		s += fmt.Sprintf(`Your context is too large to fit in a single LLM call (~30K char limit).
You should decompose it. Here is the general approach:

1. INSPECT: use execute_code to examine the context structure.
   print(context[:2000])  — see the beginning
   print(context[-500:])  — see the end
   Look for natural boundaries: headings, file markers, blank lines, topic changes.

2. DECIDE HOW TO SPLIT: based on what you see, choose a decomposition strategy.
   - By logical sections (chapters, files, topics) if natural boundaries exist
   - By content type (e.g., group related items together)
   - By fixed size as a fallback
   You decide. But each batch of sub-agents is limited to %d maximum.

3. DELEGATE: use execute_code with rlm_query_batched() to dispatch sub-agents.
   Each child gets a sub-context and a query. The child will autonomously decide
   whether to analyze directly or split further.

4. AGGREGATE: collect and merge results from children, then call final_answer.

Key rules:
- rlm_query_batched() accepts at most %d sub-agents per call.
  If you need more, make multiple batched calls sequentially.
- Do NOT send the full context to llm_query() — it will be silently truncated.
- Let children handle their own chunks; do not try to analyze everything yourself.
`, MaxFanOut, MaxFanOut)
	} else if canRecurse && contextLen > 30000 {
		s += `Your context is moderately large. You can either:
- Analyze it directly: split into pieces and use llm_query_batched() in execute_code
- Or delegate to 2-3 child agents via rlm_query_batched() if the task is complex
Choose based on the task complexity and content structure.
`
	} else if !canRecurse && contextLen > 30000 {
		s += `You are at max depth and cannot delegate further.
Split the context into pieces and use llm_query_batched() for parallel analysis.
`
	} else {
		s += `Your context is small enough to analyze directly.
Use execute_code to inspect it, then llm_query() if you need LLM analysis.
Call final_answer when done.
`
	}

	if depth > 0 {
		s += `
You are a child agent — a parent delegated a portion of a larger task to you.
Focus on YOUR section only. Return specific, structured findings.
If you find nothing relevant, say so explicitly.
`
	}

	return s
}
