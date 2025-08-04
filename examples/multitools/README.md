# Multi-Tool Intelligent Assistant Example

This example demonstrates how to create an intelligent assistant that integrates multiple tools, including calculator, time tool, text processing tool, file operation tool, and web search tool.

## Features

### 🧮 Calculator Tool (calculator)
- **Basic Operations**: Addition, subtraction, multiplication, division
- **Scientific Functions**: sqrt(square root), sin(sine), cos(cosine), abs(absolute value)
- **Mathematical Constants**: pi(pi), e(natural logarithm base)
- **Examples**: 
  - `Calculate 123 + 456 * 789`
  - `Calculate sqrt(16)`
  - `Calculate sin(30*pi/180)`

### ⏰ Time Tool (time_tool)
- **Current Time**: Get current date and time
- **Date Information**: Get current date
- **Day Information**: Get current day of the week
- **Timestamp**: Get Unix timestamp
- **Examples**:
  - `What time is it now?`
  - `What day of the week is today?`
  - `Get current timestamp`

### 📝 Text Processing Tool (text_tool)
- **Case Conversion**: Convert to uppercase or lowercase
- **Character Statistics**: Count text length and word count
- **Text Reversal**: Reverse text content
- **Examples**:
  - `Convert 'Hello World' to uppercase`
  - `Count characters in 'Hello World'`
  - `Reverse text 'Hello World'`

### 📁 File Operation Tool (file_tool)
- **Read Files**: Read file contents
- **Write Files**: Create or write files
- **List Directory**: View directory contents
- **Check Existence**: Check if file exists
- **Examples**:
  - `Read README.md file`
  - `Create a test file in current directory`
  - `List all files in current directory`

### 🔍 Web Search Tool (duckduckgo_search)
- **Entity Search**: Search for information about people, companies, locations, etc.
- **Definition Queries**: Find definitions of concepts and terms
- **Historical Information**: Find historical facts and data
- **Examples**:
  - `Search for information about Steve Jobs`
  - `Find information about Tesla company`
  - `What is photosynthesis?`

## Usage

### 1. Environment Setup
Make sure you have set up the relevant environment variables and dependencies:
```bash
# Set environment variables like API keys
export OPENAI_API_KEY="your-api-key"
```

### 2. Run the Example
```bash
cd trpc.group/trpc-go/trpc-agent-go/examples/multi_tools
go run main.go
```

### 3. Specify Model
```bash
go run main.go -model="gpt-4"
```

### 4. Interactive Usage
After the program starts, you can:
- Enter various questions and requests
- Observe how the assistant selects and uses different tools
- Enter `exit` to quit the program

## Example Conversation

```
🚀 Multi-Tool Intelligent Assistant Demo
Model: deepseek-chat
Enter 'exit' to end the conversation
Available tools: calculator, time_tool, text_tool, file_tool, duckduckgo_search
============================================================
✅ Multi-tool intelligent assistant is ready! Session ID: multi-tool-session-1703123456

💡 Try asking these questions:
   [Calculator] Calculate 123 + 456 * 789
   [Calculator] Calculate the square root of pi
   [Time] What time is it now?
   [Time] What day of the week is today?
   [Text] Convert 'Hello World' to uppercase
   [Text] Count characters in 'Hello World'
   [File] Read the README.md file
   [File] Create a test file in the current directory
   [Search] Search for information about Steve Jobs
   [Search] Find information about Tesla company

👤 User: Calculate 100 + 200 * 3
🔧 Tool calls:
   🧮 calculator (ID: call_abc123)
     Arguments: {"expression":"100 + 200 * 3"}

⚡ Executing...
✅ Tool result (ID: call_abc123): {"expression":"100 + 200 * 3","result":700,"message":"Calculation result: 700"}

🤖 Assistant: According to mathematical operation rules, multiplication is performed first, then addition:
100 + 200 * 3 = 100 + 600 = 700

The calculation result is 700.
```

## Tool Design Principles

### 1. Security
- File operation tools are restricted to current directory and subdirectories
- Prevent path traversal attacks
- Limit file reading content length

### 2. User Experience
- English interface and prompts
- Clear tool call visualization
- Rich usage examples and help information

### 3. Extensibility
- Modular tool design
- Unified tool interface
- Easy to add new tools

## Extending New Tools

To add new tools, follow these steps:

1. **Define Request and Response Structures**
```go
type myToolRequest struct {
    Input string `json:"input" jsonschema:"description=Input description"`
}

type myToolResponse struct {
    Output string `json:"output"`
    Status string `json:"status"`
}
```

2. **Implement Tool Function**
```go
func myToolHandler(req myToolRequest) myToolResponse {
    // Implement tool logic
    return myToolResponse{
        Output: "Processing result",
        Status: "Success",
    }
}
```

3. **Create Tool Instance**
```go
func createMyTool() tool.CallableTool {
    return function.NewFunctionTool(
        myToolHandler,
        function.WithName("my_tool"),
        function.WithDescription("Tool description"),
    )
}
```

4. **Register Tool**
```go
tools := []tool.Tool{
    createMyTool(),
    // Other tools...
}
```

## Notes

1. **API Limitations**: Some tools may require network access or API keys
2. **Performance Considerations**: Large file operations may affect performance
3. **Error Handling**: Tool call failures will return error messages
4. **Security**: File operations are protected by path restrictions

## Technical Architecture

- **Framework**: trpc-agent-go
- **Models**: Supports various OpenAI-compatible models
- **Tool System**: Function calling-based tool invocation
- **Streaming**: Supports streaming responses and real-time interaction

## License

This example follows the project's license terms. 