# Customer Support Workflow with A2A Sub-Agent Example

This example demonstrates how to build and execute complex workflows using the trpc-agent-go graph package with GraphAgent, Runner, and A2A sub-agents. It showcases:

- Building graphs with StateGraph and conditional routing
- Creating function nodes and agent nodes
- **Integrating A2A agents as sub-agents in GraphAgent workflows**
- **Remote agent communication through A2A protocol**
- Type-safe state management with constants and helpers
- Using state management with schemas
- **Creating GraphAgent from compiled graphs with sub-agents**
- **Using Runner for session management and execution**
- Executing workflows with streaming responses

## Features

The example implements a customer support workflow that:

1. **Analyzes** incoming customer queries to determine type and priority
2. **Routes** queries based on type (technical, billing, general)
3. **Delegates** technical issues to a specialized A2A remote agent
4. **Handles** billing and general queries locally
5. **Formats** final responses with workflow metadata

## A2A Integration Architecture

### **Remote Agent Setup**

The example demonstrates a complete A2A integration:

1. **A2A Server**: Runs a specialized technical support agent with custom tools
2. **A2A Client**: Connects to the remote agent as a sub-agent in the GraphAgent
3. **Graph Coordination**: Routes technical queries to the remote agent automatically

### **Key Components**

#### **A2A Server (Remote Agent)**

```go
// Specialized technical support agent with tools
remoteAgent := llmagent.New(
    "technical-support-agent",
    llmagent.WithModel(modelInstance),
    llmagent.WithTools(tools), // Custom system monitoring tools
)

// A2A server exposes the agent
server, err := a2a.New(
    a2a.WithHost("0.0.0.0:8888"),
    a2a.WithAgent(remoteAgent),
)
```

#### **A2A Client (Sub-Agent)**

```go
// Create A2A client agent
a2aAgent, err := a2aagent.New(a2aagent.WithAgentCardURL("http://0.0.0.0:8888"))

// Add as sub-agent to GraphAgent
graphAgent, err := graphagent.New("coordinator", workflowGraph,
    graphagent.WithSubAgents([]agent.Agent{a2aAgent}),
)
```

#### **Graph Node Integration**

```go
// Graph node references sub-agent by name
AddAgentNode(nodeTechnicalSupport, agentTechnicalSupport,
    graph.WithName("Technical Support"),
    graph.WithDescription("Routes to A2A technical support agent"),
)
```

## State Management

### **Type-Safe State Keys**

This example demonstrates improved state management with constants:

#### **State Constants**

```go
const (
    stateKeyCustomerQuery    = "customer_query"
    stateKeyQueryType        = "query_type"
    stateKeyPriority         = "priority"
    stateKeyResponse         = "response"
    stateKeyFinalAnswer      = "final_answer"
)

const (
    queryTypeTechnical = "technical"
    queryTypeBilling   = "billing"
    queryTypeGeneral   = "general"
)
```

#### **StateBuilder Pattern**

```go
// ✅ Type-safe state construction
return NewStateBuilder().
    SetQueryType(queryTypeTechnical).
    SetPriority(priorityHigh).
    SetResponse("Technical response from A2A agent").
    Build()
```

### **Benefits**

1. **IDE Support**: Autocomplete, refactoring, go-to-definition
2. **Compile-time Safety**: Wrong key names caught before runtime
3. **Type Validation**: Clear error messages for wrong types
4. **Self-Documenting**: Constants show available state fields
5. **Maintainability**: Easy to rename fields across the codebase

## Workflow Architecture

The workflow uses a **GraphAgent and Runner architecture with A2A sub-agents**:

- **Graph**: Defines the workflow structure with nodes and edges
- **GraphAgent**: Wraps the graph with agent capabilities and sub-agent management
- **A2A Sub-Agent**: Remote specialized agent for technical support
- **Runner**: Manages execution, session handling, and streaming responses
- **Function Nodes**: Pure Go functions for query analysis and response formatting
- **Agent Nodes**: Routes to A2A sub-agent for specialized tasks
- **Conditional Routing**: Dynamic path selection based on query type

## Usage

### Run with default examples:

```bash
go run .
```

### Run in interactive mode:

```bash
go run . -interactive
```

### Use a different model:

```bash
go run . -model "gpt-4"
```

### Use a different A2A host:

```bash
go run . -a2a-host "localhost:9999"
```

## State Schema

The example uses a custom state schema with these fields:

- `customer_query`: Original customer query (provided by Runner)
- `query_type`: Type of query (technical, billing, general)
- `priority`: Priority level (high, medium, low)
- `technical_details`: Technical details from A2A agent
- `response`: Response from the appropriate handler
- `final_answer`: Formatted final response for the customer

## Example Workflow

```
Customer Query (via Runner)
      ↓
   Analyze Query
      ↓
 Route by Query Type
     ↙     ↓     ↘
Technical Billing General
    ↓       ↓       ↓
A2A Agent  Local   Local
    ↓       ↓       ↓
   Format Response
      ↓
   Final Result
```

## Interactive Mode

In interactive mode, you can:

- Process custom customer queries by typing them
- See real-time workflow execution
- View query classification and routing
- Type `help` for available commands
- Type `exit` to quit

### Example Interactive Session

```
🎯 Interactive Mode - Type your customer support queries
Commands: 'help', 'exit', or just type your query

Customer: My application is showing error 500, can you help me troubleshoot?
Response: 🎯 Customer Support Response
Query Type: technical
Priority: medium
Response: [Technical response from A2A agent with system diagnostics]
Thank you for contacting our support team!

Customer: I need help with my billing statement
Response: 🎯 Customer Support Response
Query Type: billing
Priority: low
Response: I understand you have a billing inquiry...
```

## Real-World Use Cases

This example demonstrates practical applications:

### **1. Customer Support Automation**

- **Query Classification**: Automatically categorize customer issues
- **Specialized Routing**: Route technical issues to specialized agents
- **Scalable Architecture**: Add more specialized A2A agents as needed

### **2. Microservices Integration**

- **Service Discovery**: A2A protocol enables dynamic agent discovery
- **Load Distribution**: Distribute workload across multiple specialized agents
- **Fault Tolerance**: Isolate failures to specific agent types

### **3. Multi-Domain Expertise**

- **Domain Specialization**: Each A2A agent can specialize in different domains
- **Tool Integration**: Remote agents can have access to domain-specific tools
- **Knowledge Isolation**: Sensitive tools/data isolated to specific agents

## Customization

To customize the workflow:

1. **Add new A2A agents**: Create specialized agents for different domains
2. **Modify routing logic**: Update conditional functions for new query types
3. **Extend state schema**: Add custom fields with reducers
4. **Add new tools**: Create function tools for A2A agents
5. **Change A2A endpoints**: Configure different A2A server hosts

## Requirements

- Go 1.21 or later
- Valid OpenAI API key (set via environment or configuration)
- Network connectivity for LLM calls and A2A communication
- A2A server running on the specified host (automatically started by the example)
