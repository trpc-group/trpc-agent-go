# Travel Assistant DSL Example

This example demonstrates a **pure builtin-only** workflow that uses a classifier agent to route user queries to specialized agents.

## 🎯 Overview

This workflow showcases:
- ✅ **No custom code required** - Uses only builtin components
- ✅ **Classifier-based routing** - Intelligent intent classification
- ✅ **Structured output** - JSON schema for classification results
- ✅ **Conditional routing** - Routes based on classification using `builtin` condition
- ✅ **Multi-agent composition** - Three specialized agents working together
- ✅ **Tool integration** - Each agent equipped with domain-specific tools
- ✅ **Mock data tools** - Realistic mock tools for demonstration
- ✅ **DuckDuckGo search** - Real web search integration

## 📊 Workflow Architecture

```
┌─────────┐
│  Start  │
└────┬────┘
     │
     ▼
┌──────────────┐
│  Classifier  │ (builtin.llmagent with structured_output)
│    Agent     │
└──────┬───────┘
       │
       │ Structured Output:
       │ {
       │   "classification": "flight_info" | "itinerary",
       │   "confidence": 0.95,
       │   "reasoning": "..."
       │ }
       │
       ▼
  ┌────────────┐
  │  If/Else   │ (builtin condition)
  │  Routing   │
  └─────┬──────┘
        │
        ├─── classification == "flight_info" ───┐
        │                                       │
        │                                       ▼
        │                              ┌────────────────┐
        │                              │ Flight Agent   │
        │                              │(builtin.llmagent)
        │                              └────────┬───────┘
        │                                       │
        └─── else (itinerary) ─────┐           │
                                   │           │
                                   ▼           │
                          ┌────────────────┐   │
                          │Itinerary Agent │   │
                          │(builtin.llmagent)  │
                          └────────┬───────┘   │
                                   │           │
                                   └─────┬─────┘
                                         │
                                         ▼
                                    ┌────────┐
                                    │  End   │
                                    └────────┘
```

## 🔧 Components Used

### 1. Classifier Agent (`builtin.llmagent`)
- **Purpose**: Classify user intent into `flight_info` or `itinerary`
- **Features**:
  - Structured output with JSON schema
  - Low temperature (0.1) for consistent classification
  - Returns classification, confidence, and reasoning

### 2. Flight Agent (`builtin.llmagent`)
- **Purpose**: Handle flight-related queries
- **Tools**:
  - `search_flights`: Search for available flights between cities (mock data)
  - `check_flight_status`: Check real-time flight status (mock data)
  - `duckduckgo_search`: Search for airline information and policies (real search)
- **Capabilities**:
  - Flight bookings and reservations
  - Flight status and schedules
  - Airline information
  - Airport information

### 3. Itinerary Agent (`builtin.llmagent`)
- **Purpose**: Handle travel planning queries
- **Tools**:
  - `get_destination_info`: Get destination details (mock data)
  - `suggest_activities`: Generate day-by-day itineraries (mock data)
  - `duckduckgo_search`: Search for attractions and travel tips (real search)
- **Capabilities**:
  - Create detailed itineraries
  - Suggest destinations and activities
  - Plan multi-day trips
  - Provide travel recommendations

## 🎨 Key Features

### ✅ Pure Builtin Implementation

This example uses **ONLY** builtin components:
- `builtin.llmagent` for all three agents
- `builtin` condition for routing
- No custom components or functions needed!

### 🔀 Conditional Routing with Direct Field Access

The routing logic uses a `builtin` condition with **ordered cases** and direct field access. 在 DSL 层你只需要写人类友好的表达式，底层会自动映射到 per‑node 的结构化输出缓存：

```json
{
  "type": "builtin",
  "cases": [
    {
      "name": "flight_info",
      "condition": {
        "conditions": [
          {
            "variable": "input.output_parsed.classification",
            "operator": "==",
            "value": "flight_info"
          }
        ]
      },
      "target": "flight_agent"
    }
  ],
  "default": "itinerary_agent"
}
```

**How it works**:
1. Classifier 输出结构化 JSON：`{"classification": "flight_info", "confidence": 0.95, ...}`
2. 引擎会把解析后的结果缓存到 per‑node 状态：
   - `state["node_structured"]["classifier"].output_parsed = {...}`
3. 条件里的变量 `input.output_parsed.classification` 在编译阶段会被重写为：
   - `node_structured.classifier.output_parsed.classification`
4. Type-safe comparison: `classification == "flight_info"`
5. Cases are evaluated in order; if the first case matches → route to Flight Agent; otherwise → route to Itinerary Agent via `default`

**Key Innovation**:
- ✅ **No string matching!** Direct field access like `output_parsed.classification`
- ✅ **Type-safe** comparison using `==` operator
- ✅ **Clean syntax** matching modern workflow UI tools

### 📝 Structured Output with Automatic Parsing (Per‑Node Cache)

The classifier uses JSON schema to ensure consistent output, and the framework **automatically parses and stores** the result into a per‑node cache rather than a single global field.

```json
{
  "type": "object",
  "properties": {
    "classification": {
      "type": "string",
      "enum": ["flight_info", "itinerary"]
    },
    "confidence": {
      "type": "number",
      "minimum": 0,
      "maximum": 1
    },
    "reasoning": {
      "type": "string"
    }
  },
  "required": ["classification", "confidence", "reasoning"]
}
```

**Automatic State Storage**:

When `structured_output` is configured, the framework automatically:
1. Parses the JSON response from the classifier
2. Stores the parsed object under the classifier node in `node_structured`

**Example State After Classifier** (simplified):
```json
{
  "node_structured": {
    "classifier": {
      "output_parsed": {
        "classification": "flight_info",
        "confidence": 0.95,
        "reasoning": "User is asking about booking a flight"
      }
    }
  },
  "last_response": "{\"classification\":\"flight_info\",...}",
  "messages": [...]
}
```

在 DSL 里你使用 `input.output_parsed.classification` 表达“直接上游节点的结构化输出”，编译器会根据条件边的 `from` 节点自动补上对应的 node id，做到“内部按节点缓存，外部保持简单语法”。如需引用非直接上游的节点，则使用 `nodes.<id>.output_parsed.xxx` 形式。

## 🛠️ Tools and ToolSets Overview

This example demonstrates how to equip agents with domain-specific tools and toolsets. Each agent has access to tools that help it perform its specialized tasks.

### Built-in Tools

The DSL framework provides built-in tools that are automatically registered when you import the builtin package:

```go
import (
    _ "trpc.group/trpc-go/trpc-agent-go/dsl/registry/builtin"
)
```

#### `duckduckgo_search` (Built-in Tool)
Real web search using DuckDuckGo. Both agents have access to this tool for searching information online.

**Input**:
```json
{
  "query": "best time to visit Tokyo"
}
```

**Output**: Search results from DuckDuckGo with titles, snippets, and URLs.

**Note**: This tool is automatically available in `registry.DefaultToolRegistry` - no manual registration needed!

### Built-in ToolSets

ToolSets are collections of related tools. The DSL framework provides built-in toolsets:

#### `file` (Built-in ToolSet)
A collection of file operation tools for reading, writing, and searching files.

**Available Tools**:
- `file_read_file` - Read file contents
- `file_save_file` - Save content to a file
- `file_list_file` - List files in a directory
- `file_search_file` - Search for files by pattern
- `file_search_content` - Search content within files
- `file_replace_content` - Replace content in files
- `file_read_multiple_files` - Read multiple files at once

**Usage in DSL**:
```json
{
  "id": "code_agent",
  "type": "builtin.llmagent",
  "config": {
    "tool_sets": ["file"],
    "instruction": "You are a code assistant with file access..."
  }
}
```

**Note**: The `file` toolset is automatically available in `registry.DefaultToolSetRegistry` - no manual registration needed!

### Flight Agent Tools

#### 1. `search_flights` (Mock Tool)
Search for available flights between two cities on a specific date.

**Input**:
```json
{
  "from": "Beijing",
  "to": "Shanghai",
  "date": "2024-12-25"
}
```

**Output**:
```json
{
  "from": "Beijing",
  "to": "Shanghai",
  "date": "2024-12-25",
  "flights": [
    {
      "flight_number": "CA1234",
      "airline": "Air China",
      "departure": "08:00",
      "arrival": "10:30",
      "duration": "2h 30m",
      "price": 580.00,
      "available": true
    }
  ],
  "count": 3
}
```

**Mock Data**: Returns 3 flights from Air China, China Eastern, and China Southern with varying prices and availability.

#### 2. `check_flight_status` (Mock Tool)
Check the real-time status of a flight by flight number.

**Input**:
```json
{
  "flight_number": "CA1234"
}
```

**Output**:
```json
{
  "flight_number": "CA1234",
  "airline": "Air China",
  "status": "On Time",
  "scheduled_departure": "14:30",
  "actual_departure": "",
  "scheduled_arrival": "17:00",
  "estimated_arrival": "17:00",
  "departure_gate": "A12",
  "arrival_gate": "B8",
  "departure_terminal": "T3",
  "arrival_terminal": "T2"
}
```

**Mock Data**: Returns different statuses (On Time, Delayed, Scheduled) based on flight number pattern.

#### 3. `duckduckgo_search` (Real Tool)
Search using DuckDuckGo's Instant Answer API for airline information, airport details, and travel policies.

**Input**:
```json
{
  "query": "Air China baggage policy"
}
```

**Output**: Real search results from DuckDuckGo with titles, URLs, and descriptions.

### Itinerary Agent Tools

#### 1. `get_destination_info` (Mock Tool)
Get comprehensive information about a travel destination.

**Input**:
```json
{
  "destination": "Japan"
}
```

**Output**:
```json
{
  "name": "Japan",
  "country": "Japan",
  "description": "A fascinating blend of ancient traditions and cutting-edge technology",
  "best_time_to_visit": "March-May (Spring) and September-November (Autumn)",
  "climate": "Temperate with four distinct seasons",
  "languages": ["Japanese"],
  "currency": "Japanese Yen (JPY)",
  "timezone": "JST (UTC+9)",
  "highlights": [
    "Mount Fuji",
    "Tokyo Skytree",
    "Kyoto Temples",
    "Cherry Blossoms",
    "Traditional Onsen",
    "Sushi and Ramen"
  ]
}
```

**Mock Data**: Includes detailed information for Japan, Europe, China, and generic destinations.

#### 2. `suggest_activities` (Mock Tool)
Generate day-by-day activity suggestions with timing and costs.

**Input**:
```json
{
  "destination": "Japan",
  "days": 5,
  "interests": "culture, food"
}
```

**Output**:
```json
{
  "destination": "Japan",
  "days": 5,
  "activities": [
    {
      "day": 1,
      "time": "09:00",
      "activity": "Visit Senso-ji Temple",
      "location": "Asakusa",
      "duration": "2 hours",
      "description": "Tokyo's oldest temple with traditional atmosphere",
      "estimated_cost": "Free"
    }
  ],
  "total_estimated_cost": "Varies by choices"
}
```

**Mock Data**: Provides detailed itineraries for Japan, Europe, and generic destinations with specific activities, timings, and costs.

#### 3. `duckduckgo_search` (Real Tool)
Search for specific attractions, restaurants, or travel tips.

**Input**:
```json
{
  "query": "best sushi restaurants in Tokyo"
}
```

**Output**: Real search results from DuckDuckGo.

### Tool Registration

Tools are registered in `main.go` using the Tool Registry:

```go
// Create Tool Registry
toolRegistry := registry.NewToolRegistry()

// Register DuckDuckGo search tool
ddgTool := duckduckgo.NewTool()
toolRegistry.MustRegister("duckduckgo_search", ddgTool)

// Register flight-related tools
searchFlightsTool := createSearchFlightsTool()
toolRegistry.MustRegister("search_flights", searchFlightsTool)

checkFlightStatusTool := createCheckFlightStatusTool()
toolRegistry.MustRegister("check_flight_status", checkFlightStatusTool)

// Register itinerary-related tools
getDestinationInfoTool := createGetDestinationInfoTool()
toolRegistry.MustRegister("get_destination_info", getDestinationInfoTool)

suggestActivitiesTool := createSuggestActivitiesTool()
toolRegistry.MustRegister("suggest_activities", suggestActivitiesTool)
```

### Tool Assignment in DSL

Tools are assigned to agents in `workflow.json`:

```json
{
  "id": "flight_agent",
  "config": {
    "tools": ["search_flights", "check_flight_status", "duckduckgo_search"]
  }
}
```

## 🚀 Running the Example

### Prerequisites

1. Set OpenAI API key (or DeepSeek API key):
```bash
export OPENAI_API_KEY="your-api-key-here"
export OPENAI_BASE_URL="https://api.deepseek.com/v1"  # For DeepSeek
export MODEL_NAME="deepseek-chat"
```

### Run

**Option 1: Using run.sh (recommended)**
```bash
cd examples/dsl/travel_assistant
chmod +x run.sh
./run.sh
```

**Option 2: Manual run**
```bash
cd examples/dsl/travel_assistant
go run main.go
```

### Architecture

The example uses the **runner pattern** for production-ready workflow execution:

```go
// 1. Create tool registry with built-in tools
toolRegistry := registry.NewToolRegistryWithBuiltins()
// This automatically registers: duckduckgo_search

// 2. Register custom mock tools
searchFlightsTool := createSearchFlightsTool()
toolRegistry.MustRegister("search_flights", searchFlightsTool)

checkFlightStatusTool := createCheckFlightStatusTool()
toolRegistry.MustRegister("check_flight_status", checkFlightStatusTool)

getDestinationInfoTool := createGetDestinationInfoTool()
toolRegistry.MustRegister("get_destination_info", getDestinationInfoTool)

suggestActivitiesTool := createSuggestActivitiesTool()
toolRegistry.MustRegister("suggest_activities", suggestActivitiesTool)

// 3. Compile DSL to Graph with tool registry
compiler := dsl.NewCompiler(registry.DefaultRegistry).
    WithModelRegistry(modelRegistry).
    WithToolRegistry(toolRegistry)
compiledGraph, _ := compiler.Compile(&workflow)

// 4. Wrap Graph in GraphAgent
graphAgent, _ := graphagent.New("travel-assistant", compiledGraph,
    graphagent.WithDescription("Travel assistant with classifier routing"),
)

// 5. Create Runner with Session Service
sessionService := inmemory.NewSessionService()
appRunner := runner.NewRunner(
    "travel-assistant-workflow",
    graphAgent,
    runner.WithSessionService(sessionService),
)
defer appRunner.Close()

// 6. Run workflow with user message
message := model.NewUserMessage(userInput)
eventChan, _ := appRunner.Run(ctx, userID, sessionID, message)
```

**Benefits of the runner pattern**:
- ✅ **Session management** - Maintains conversation state across multiple turns
- ✅ **State persistence** - Uses session service for state storage
- ✅ **Event streaming** - Real-time event updates
- ✅ **Production-ready** - Proper error handling and resource cleanup

**Built-in Tools**:
- ✅ **Automatic registration** - `NewToolRegistryWithBuiltins()` includes duckduckgo_search
- ✅ **No manual setup** - Built-in tools work out of the box
- ✅ **Extensible** - Add custom tools alongside built-in ones

## 📋 Test Cases

The example includes 4 test queries:

1. **Flight booking**: "I need to book a flight from Beijing to Shanghai next Monday"
   - Expected: Routes to Flight Agent

2. **Itinerary planning**: "Can you help me plan a 5-day trip to Japan?"
   - Expected: Routes to Itinerary Agent

3. **Flight status**: "What's the status of flight CA1234?"
   - Expected: Routes to Flight Agent

4. **Vacation planning**: "I want to create an itinerary for my vacation in Europe"
   - Expected: Routes to Itinerary Agent

## 🎯 Expected Output

```
✅ Model registered: deepseek-chat
✅ Workflow loaded: travel_assistant
✅ Workflow compiled successfully

━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
Test 1: I need to book a flight from Beijing to Shanghai next Monday
━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━

🤖 Response:
I'd be happy to help you book a flight from Beijing to Shanghai for next Monday...
[Flight Agent response]

━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
Test 2: Can you help me plan a 5-day trip to Japan?
━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━

🤖 Response:
Absolutely! Here's a suggested 5-day itinerary for Japan...
[Itinerary Agent response]

━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
Test 3: What's the status of flight CA1234?
━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━

🤖 Response:
I'd love to help you check the status of flight CA1234, but as an itinerary
planning assistant, I don't have real-time access to current flight status...
[Itinerary Agent response - Note: Classifier may route flight status queries
to either agent depending on context]
```

### ⚠️ Known Issues

1. **Timeout Errors**: Some queries may timeout due to DeepSeek API response latency. This is a known issue with the API and not the DSL framework.

2. **Classification Accuracy**: The classifier may occasionally misclassify queries (e.g., routing "flight status" to Itinerary Agent instead of Flight Agent). This can be improved by:
   - Refining the classifier's instruction
   - Adding more examples in the prompt
   - Using a more powerful model
   - Adjusting the temperature parameter

## 💡 Design Insights

### ✅ Advantages of Builtin-Only Approach

1. **Zero Code Required**
   - No custom components to write
   - No registration logic needed
   - Pure JSON configuration

2. **Easy to Modify**
   - Change agent instructions in DSL
   - Add more classification categories
   - Adjust routing logic

3. **Fully Serializable**
   - Entire workflow is JSON
   - Can be stored in database
   - Can be edited by front-end

### ⚠️ Current Limitations

1. **Binary Routing Only**
   - `builtin` condition only supports true/false
   - For 3+ categories, need cascading conditions
   - Would benefit from `builtin.switch` (future enhancement)

2. **String Matching for JSON**
   - Uses `contains` operator on JSON string
   - Not as elegant as direct field access
   - Would benefit from `json_extract` operator (future enhancement)

## 🔮 Future Enhancements

### Proposed: `builtin.switch` Condition

To make multi-way routing more elegant:

```json
{
  "type": "builtin.switch",
  "variable": "last_response",
  "json_path": "classification",
  "routes": {
    "flight_info": "flight_agent",
    "itinerary": "itinerary_agent",
    "hotel": "hotel_agent",
    "default": "general_agent"
  }
}
```

This would:
- ✅ Support 3+ routes natively
- ✅ Parse JSON automatically
- ✅ Provide cleaner DSL syntax
- ✅ Eliminate string matching hacks

## 📚 Related Examples

- **llmagent**: Demonstrates `builtin.llmagent` with custom routing component
- **code_execution**: Shows `builtin.code` for data processing
- **graph_subagent**: Uses `builtin.agent` for sub-agent composition

## 🎓 Learning Points

1. **Structured Output** enables reliable routing
2. **Builtin conditions** can handle simple classification routing
3. **String matching** works but is not ideal for JSON parsing
4. **Pure builtin workflows** are possible for many use cases
5. **Multi-way routing** would benefit from dedicated switch condition

---

**This example proves that you can build sophisticated multi-agent workflows using ONLY builtin components!** 🎉
