//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package todo

// DefaultToolDescription is the short one-line description surfaced in
// the tool declaration. It is what most LLM providers feed directly into
// the function-calling prompt.
const DefaultToolDescription = "Maintain a structured todo list for the current session. " +
	"Call this proactively to plan, track and report progress on multi-step tasks. " +
	"Each call replaces the entire list; at most one task may be 'in_progress' at any time."

// DefaultToolPrompt is a long-form usage guide for the todo tool,
// intended to be concatenated into an agent's system instruction.
// It tells the model *when* and *how* to use the tool - the short
// Description alone is often not enough to produce disciplined usage.
const DefaultToolPrompt = `## Todo list tool

You have access to a todo_write tool to create and manage a structured
task list for the current session. Use it proactively to plan and track
complex work and to show progress to the user.

### When to use

Use the tool when:
1. A task requires 3 or more distinct steps.
2. The user provides multiple tasks (numbered or comma-separated).
3. The user explicitly asks for a checklist.
4. You receive new instructions that must be captured as follow-up work.
5. You start working on a task - mark it as in_progress BEFORE beginning.
6. You finish a task - mark it completed and add any follow-ups discovered.

Skip the tool when:
- There is only a single, straightforward step.
- The task is trivial or purely conversational.

### Task states

Each item has three fields:

- content:    imperative form, e.g. "Run tests".
- activeForm: present-continuous form, e.g. "Running tests".
- status:     one of pending | in_progress | completed.

Rules:
- At most ONE task may be in_progress at any time. Keep exactly one
  in_progress while you are actively working the plan; zero is only
  acceptable when the list has just been drafted, everything is
  completed, or you are between batches.
- Mark a task completed IMMEDIATELY after finishing; do not batch completions.
- Only mark completed when the task is FULLY done. If blocked, keep it
  in_progress and add a new task describing what must be resolved.
- Each call replaces the ENTIRE list. Send the full updated list every time.
- Content must be unique across the list; do not duplicate tasks.
- Remove tasks that are no longer relevant instead of leaving them stale.

### Example

User: "Add a dark-mode toggle and make sure tests pass."

First call:
  todo_write({todos: [
    {content: "Add dark-mode toggle component",    activeForm: "Adding dark-mode toggle component",    status: "in_progress"},
    {content: "Wire theme state into app context", activeForm: "Wiring theme state into app context", status: "pending"},
    {content: "Update styles for dark theme",      activeForm: "Updating styles for dark theme",      status: "pending"},
    {content: "Run test suite and fix failures",   activeForm: "Running test suite and fixing failures", status: "pending"},
  ]})

After completing the first item, call again with the first task marked
completed and the next one flipped to in_progress.`
