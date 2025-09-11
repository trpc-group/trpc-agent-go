# Context Continuity Demo: Chain and Parallel Agent Nesting

This example demonstrates **context continuity** in trpc-agent-go when Sequential (Chain) and Parallel agents are nested together. It shows how Session visibility works across different agent hierarchies and verifies that our context filtering improvements allow Sequential agents to properly see outputs from their Parallel sub-agents.

## Problem Statement

Previously, when a `ChainAgent` contained a `ParallelAgent`, the agents following the parallel execution would lose access to the parallel agents' outputs. This was due to branch filtering logic that prevented Sequential agents from seeing events from sub-branches.

## Solution

The context continuity fix improves the `isEventBelongsToBranch` function to allow Sequential agents to see outputs from their sub-agent hierarchies while maintaining proper isolation between parallel agents.

## Architecture Examples

### Simple Case: Sequential(Parallel(A1,A2), B)

```
Sequential Agent (Chain)
├── Parallel Agent
│   ├── A1 (NumberAnalyst)    - Branch: chain.parallel.A1
│   └── A2 (CultureAnalyst)   - Branch: chain.parallel.A2
└── B (ColorAnalyst)          - Branch: chain
```

**Expected Visibility:**
- **A1 sees**: System prompt + User message (2 messages)
- **A2 sees**: System prompt + User message (2 messages) 
- **B sees**: System prompt + User message + A1 output + A2 output (4 messages) ✅

### Complex Case: Sequential(Parallel(Sequential(Parallel(A1,A2), B), C), D)

```
ComplexNesting (Sequential)
├── MiddleParallel (Parallel)
│   ├── InnerSequential (Sequential)
│   │   ├── InnerParallel (Parallel)
│   │   │   ├── NumberAnalyst     - Branch: ComplexNesting.MiddleParallel.InnerSequential.InnerParallel.NumberAnalyst
│   │   │   └── CultureAnalyst    - Branch: ComplexNesting.MiddleParallel.InnerSequential.InnerParallel.CultureAnalyst
│   │   └── ColorAnalyst          - Branch: ComplexNesting.MiddleParallel.InnerSequential
│   └── Evaluator                 - Branch: ComplexNesting.MiddleParallel.Evaluator
└── Summarizer                    - Branch: ComplexNesting
```

**Expected Visibility:**

| Agent | Round 1 Messages | Round 2 Messages | Can See |
|-------|------------------|------------------|---------|
| NumberAnalyst | 2 | 6 | System + User + Previous rounds |
| CultureAnalyst | 2 | 6 | System + User + Previous rounds |
| ColorAnalyst | 4 | 9 | System + User + NumberAnalyst + CultureAnalyst + Previous rounds ✅ |
| Evaluator | 2 | 5 | System + User + Previous rounds |
| Summarizer | 6 | 12 | System + User + All agent outputs ✅ |

## Key Verification Points

### 1. Sequential Agents See Parallel Sub-Agents

**ColorAnalyst** (Sequential following Parallel) should see outputs from **NumberAnalyst** and **CultureAnalyst**:
- Round 1: 4 messages (includes parallel outputs)
- Round 2: 9 messages (includes parallel outputs + previous rounds)

### 2. Root Sequential Agent Sees All

**Summarizer** (root Sequential) should see outputs from all nested agents:
- Round 1: 6 messages (includes all agent outputs)
- Round 2: 12 messages (includes all agent outputs + previous rounds)

### 3. Context Continuity Across Rounds

All agents in Round 2 should explicitly reference Round 1 analysis results:
- "From previous agents..."
- "Building on Round 1 analysis..."
- "Compared to the number 8 analysis..."

### 4. Parallel Agent Isolation

Parallel agents remain isolated from each other:
- **NumberAnalyst** cannot see **CultureAnalyst** outputs (same round)
- **Evaluator** cannot see **ColorAnalyst** outputs (same parallel level)

## Running the Demo

### Prerequisites

Set up environment variables:
```bash
export OPENAI_BASE_URL="https://api.deepseek.com/v1"
export OPENAI_API_KEY="your-api-key"
export MODEL_NAME="deepseek-chat"
```

### Build and Run

```bash
# Build the demo
go build -o combined-agent main.go

# Run the demo
./combined-agent
```

### Expected Output Pattern

```
🚀 Round 1: Analyze number 8 and color red
📍 Key observation: Can ColorAnalyst see NumberAnalyst & CultureAnalyst outputs?

📋 [NumberAnalyst] Branch: ComplexNesting.MiddleParallel.InnerSequential.InnerParallel.NumberAnalyst
   Message count: 2
   📝 Current message only (2 messages)

📋 [CultureAnalyst] Branch: ComplexNesting.MiddleParallel.InnerSequential.InnerParallel.CultureAnalyst
   Message count: 2
   📝 Current message only (2 messages)

📋 [ColorAnalyst] Branch: ComplexNesting.MiddleParallel.InnerSequential
   Message count: 4
   🟡 Partial context (4 messages)  ✅ SUCCESS: Sees parallel outputs

📋 [Summarizer] Branch: ComplexNesting
   Message count: 6
   ✅ Rich context (6 messages)     ✅ SUCCESS: Sees all outputs
```

### What to Look For

1. **Message Count Progression**: 
   - ColorAnalyst: 2 → 4 → 9 messages
   - Summarizer: 2 → 6 → 12 messages

2. **Context References in Round 2**:
   - "From NumberAnalyst, I learned..."
   - "Building on previous analyses..."
   - "Compared to the number 8 analysis..."

3. **Branch Hierarchy**:
   - Clear hierarchical branch naming showing parent-child relationships
   - Sequential agents can see sub-branch events

## Technical Details

### Branch Filtering Logic

The improved `isEventBelongsToBranch` function allows:

```go
// Original logic: agent can see parent/ancestor events
if strings.HasPrefix(invocationBranch, evt.Branch) {
    return true
}

// NEW: Sequential agents can see sub-agent events
if len(eventParts) > len(invocationParts) {
    // Check if event is from a sub-branch
    for i, part := range invocationParts {
        if eventParts[i] != part {
            return false
        }
    }
    return true  // ✅ Allow Sequential to see Parallel sub-agents
}
```
