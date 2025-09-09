# Human-in-the-Loop Agent Example

This example demonstrates how to implement a **Human-in-the-Loop (HIL)** pattern using a long-running tool. The agent handles employee reimbursement requests with automatic approval for small amounts and manager approval for larger amounts.

## Overview

Human-in-the-Loop is a critical pattern in AI agent systems where human intervention is required for certain decisions or validations. This example shows how to:

- **Pause agent execution** for human approval.
- **Resume execution** after receiving human input (simulated programmatically in this example).
- **Handle long-running operations** that require external validation.
- **Maintain state** during the approval process.

## Architecture

The workflow aligns with a typical HIL pattern and the original example’s semantics:

```
User Request → Agent Analysis → Decision Point
                                    ↓
                              Amount < $100?
                                ↙        ↘
                        Auto Approve    Request Approval (long-running)
                               ↓              ↓
                         Reimburse      Wait for Human (pending)
                                               ↓
                                   Approval Callback (approved/rejected)
                                               ↓
                                        Resume Agent Execution
                                               ↓
                           Approved → Reimburse     Rejected → Notify User
```

In this demo, the “Approval Callback” is simulated programmatically to provide a complete end-to-end flow without external services. In production, this would be triggered by an external approver UI/service.

## Key Features

### Long-Running Function Tool

The example uses `LongRunningFunctionTool` to implement the approval process:

```go
function.NewFunctionTool(
    askForApproval,
    function.WithLongRunning(true),
    function.WithName("ask_for_approval"),
    function.WithDescription("Ask for approval for the reimbursement."),
)
```

### Programmatic Approval (for demo)

- When the agent calls `ask_for_approval`, the tool returns a pending status and a `ticket_id`.
- The example code automatically simulates manager approval by sending an updated tool result back to the agent.
- This mirrors a real external approval callback, but without requiring user input.

## Implementation Details

### 1. Agent Configuration

The reimbursement agent is configured with:

- **Model**: DeepSeek Chat for intelligent decision-making.
- **Tools**: `reimburse` and `ask_for_approval` functions.
- **Instructions**: Clear guidelines for handling amount thresholds.

### 2. Tool Functions

#### `askForApproval`

- **Type**: Long-running function tool.
- **Purpose**: Initiates approval workflow for amounts ≥ $100.
- **Returns**: Pending status with ticket ID.

```go
func askForApproval(i askForApprovalInput) askForApprovalOutput {
    return askForApprovalOutput{
        Status:   "pending",
        Amount:   i.Amount,
        TicketID: "reimbursement-ticket-001",
    }
}
```

#### `reimburse`

- **Type**: Standard function tool.
- **Purpose**: Processes the reimbursement.
- **Returns**: Success status.

### 3. Workflow States

The system handles multiple states:

1. **Initial Request**: Agent receives reimbursement request.
2. **Analysis**: Agent determines if approval is needed.
3. **Pending**: Waiting for manager approval (simulated in this example).
4. **Approved/Rejected**: Manager decision applied.
5. **Final Action**: Reimbursement processed or user notified.

## Usage

### Running the Example

```bash
cd examples/humaninloop
# Basic usage (in-memory session service)
go run .

# With custom model
go run . -model gpt-4o-mini

# Disable streaming
go run . -streaming=false
```

### Example Interactions

#### Small Amount (Auto-approved)

```
User Query: Please reimburse $50 for meals
🤖 Assistant: I'll process your reimbursement request for $50...
🔧 Tool calls initiated:
   • reimburse (ID: call_001)
✅ Tool response: {"status": "ok"}
🤖 Assistant: Your $50 meal reimbursement has been approved and processed automatically.
```

#### Large Amount (Requires Approval, simulated)

```
User Query: Please reimburse $200 for conference travel
🔧 Tool calls initiated:
   • ask_for_approval (ID: call_002)
✅ Tool response: {"status": "pending", "ticket_id": "reimbursement-ticket-001"}
🤖 Assistant: Your request for $200 conference travel reimbursement requires manager approval...

--- Simulating external approval ---
--- Sending updated tool result: {"status": "approved", "approver_feedback": "Approved by manager"} ---
🔧 Tool calls initiated:
   • reimburse (ID: call_003)
✅ Tool response: {"status": "ok"}
🤖 Assistant: Great! Your reimbursement has been approved and processed.
```

### Command Line Options

- `-model`: Model name to use (default: "deepseek-chat").
- `-streaming`: Enable streaming mode for responses (default: true).

## Notes

- In a real system, the approval would be performed by an external service or a human UI that sends the decision back to the agent. This demo simulates that behavior programmatically for a complete end-to-end flow.

## References

- [Google ADK Long Running Function Tool](https://google.github.io/adk-docs/tools/function-tools/#2-long-running-function-tool)
- [LangGraph Human-in-the-Loop Concepts](https://langchain-ai.github.io/langgraph/concepts/human_in_the_loop/)
