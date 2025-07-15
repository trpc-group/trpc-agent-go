# Human-in-the-Loop Agent Example

This example demonstrates how to implement a **Human-in-the-Loop (HIL)** pattern using long running tool. The agent handles employee reimbursement requests with automatic approval for small amounts and manager approval for larger amounts.

## Overview

Human-in-the-Loop is a critical pattern in AI agent systems where human intervention is required for certain decisions or validations. This example shows how to:

- **Pause agent execution** for human approval
- **Resume execution** after receiving human input
- **Handle long-running operations** that require external validation
- **Maintain state** during the approval process

## Architecture

The example implements a reimbursement workflow with two main components:

1. **Automatic Processing**: Amounts < $100 are automatically approved
2. **Human Approval**: Amounts ≥ $100 require manager approval

```
User Request → Agent Analysis → Decision Point
                                    ↓
                              Amount < $100? 
                                ↙        ↘
                        Auto Approve    Request Approval
                               ↓              ↓
                         Reimburse      Wait for Human
                                            ↓
                                    Manager Decision
                                        ↙      ↘
                                 Approve     Reject
                                   ↓          ↓
                               Reimburse   Notify User
```

## Key Features

### Long-Running Function Tools

The example uses `LongRunningFunctionTool` to implement the approval process:

```go
function.NewFunctionTool(
    askForApproval,
    function.WithLongRunning(true),
    function.WithName("ask_for_approval"),
    function.WithDescription("Ask for approval for the reimbursement."),
)
```

### Persistent Execution State

The agent can pause execution indefinitely until human input is received, maintaining all context and state.

### Flexible Integration Points

Human intervention can be introduced at any point in the workflow, allowing for:
- Approval/rejection decisions
- State modifications
- Tool call reviews
- Input validation

## Implementation Details

### 1. Agent Configuration

The reimbursement agent is configured with:
- **Model**: DeepSeek Chat for intelligent decision-making
- **Tools**: `reimburse` and `ask_for_approval` functions
- **Instructions**: Clear guidelines for handling different amount thresholds

### 2. Tool Functions

#### `askForApproval`
- **Type**: Long-running function tool
- **Purpose**: Initiates approval workflow for amounts ≥ $100
- **Returns**: Pending status with ticket ID

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
- **Type**: Standard function tool
- **Purpose**: Processes the actual reimbursement
- **Returns**: Success status

### 3. Workflow States

The system handles multiple states:

1. **Initial Request**: Agent receives reimbursement request
2. **Analysis**: Agent determines if approval is needed
3. **Pending**: Waiting for manager approval (if required)
4. **Approved/Rejected**: Manager decision received
5. **Final Action**: Reimbursement processed or user notified

## Usage

### Running the Example

```bash
cd examples/humaninloop
go run .
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

#### Large Amount (Requires Approval)
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

### Tool Configuration
```go
function.WithLongRunning(true)  // Enables long running to help implemententing man-in-the-loop
```

## References

- [Google ADK Long Running Function Tool](https://google.github.io/adk-docs/tools/function-tools/#2-long-running-function-tool)
- [LangGraph Human-in-the-Loop Concepts](https://langchain-ai.github.io/langgraph/concepts/human_in_the_loop/)
