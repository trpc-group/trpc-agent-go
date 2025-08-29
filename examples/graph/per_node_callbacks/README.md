# Per-Node Callbacks Example

This example demonstrates the **per-node callbacks** functionality in the graph package, showing how to use both global and per-node callbacks for fine-grained control over node execution behavior.

## 🎯 Overview

The per-node callbacks feature allows you to:

- **Global Callbacks**: Set callbacks that apply to all nodes in the graph
- **Per-Node Callbacks**: Set specific callbacks for individual nodes
- **Callback Types**: Use `BeforeNode`, `AfterNode`, and `OnNodeError` callbacks
- **Callback Merging**: Global callbacks execute first, then per-node callbacks

## 🏗️ Architecture

### Callback Execution Order

```
Global BeforeNode Callbacks → Per-Node BeforeNode Callbacks → Node Execution → Per-Node AfterNode Callbacks → Global AfterNode Callbacks
```

### Callback Types

1. **BeforeNode Callbacks**: Execute before node execution
   - Can modify state
   - Can return custom results to skip node execution
   - Can return errors to stop execution

2. **AfterNode Callbacks**: Execute after node execution
   - Can modify results
   - Can perform validation
   - Can add metadata

3. **OnNodeError Callbacks**: Execute when node fails
   - Cannot change the error
   - Useful for logging, monitoring, cleanup

## 🚀 Usage

### Basic Per-Node Callback

```go
graph.NewStateGraph(schema).
    AddNode("my_node", myFunction,
        graph.WithPreNodeCallback(func(ctx context.Context, callbackCtx *graph.NodeCallbackContext, state graph.State) (any, error) {
            fmt.Printf("Before executing: %s\n", callbackCtx.NodeID)
            return nil, nil
        }),
        graph.WithPostNodeCallback(func(ctx context.Context, callbackCtx *graph.NodeCallbackContext, state graph.State, result any, nodeErr error) (any, error) {
            fmt.Printf("After executing: %s\n", callbackCtx.NodeID)
            return nil, nil
        }),
    )
```

### Global Callbacks

```go
globalCallbacks := graph.NewNodeCallbacks().
    RegisterBeforeNode(func(ctx context.Context, callbackCtx *graph.NodeCallbackContext, state graph.State) (any, error) {
        fmt.Printf("Global before: %s\n", callbackCtx.NodeID)
        return nil, nil
    })

graph.NewStateGraph(schema).
    WithNodeCallbacks(globalCallbacks).
    AddNode("my_node", myFunction)
```

### Error Handling Callbacks

```go
graph.NewStateGraph(schema).
    AddNode("risky_node", riskyFunction,
        graph.WithNodeErrorCallback(func(ctx context.Context, callbackCtx *graph.NodeCallbackContext, state graph.State, err error) {
            fmt.Printf("Node %s failed: %v\n", callbackCtx.NodeID, err)
            // Set fallback state
            state["fallback_result"] = "default_value"
        }),
    )
```

## 📋 Example Features

This example demonstrates:

### 1. **Input Enhancement** (Step 1)
- **Pre-callback**: Enhances input by adding a prefix
- **Post-callback**: Validates the processing result

### 2. **Error Handling** (Step 2)
- **Pre-callback**: Checks for required input and sets defaults
- **Error callback**: Handles errors gracefully with fallback results

### 3. **Conditional Processing** (Step 3)
- **Pre-callback**: Checks input length and can skip processing
- **Post-callback**: Adds timestamps to results

### 4. **Global Monitoring**
- **Global callbacks**: Log all node executions for monitoring

## 🎮 Running the Example

### Default Mode
```bash
go run . --model deepseek-chat
```

### Interactive Mode
```bash
go run . --model deepseek-chat --interactive
```

### Example Inputs

1. **Normal Processing**: `"Hello World"`
2. **Length Check**: `"This is a very long input that will trigger the length check callback in step 3"`
3. **Error Handling**: `"ERROR test input"`
4. **Standard Workflow**: `"Normal processing test"`

## 📊 Expected Output

```
🚀 Per-Node Callbacks Example
Model: deepseek-chat
==================================================
📋 Running 4 examples...

--- Example 1 ---
Input: Hello World
🌍 [GLOBAL] Before node: step1 (function)
🎯 [STEP1] Pre-callback: Enhancing input before processing
🎯 [STEP1] Input enhanced: Enhanced: Hello World
📝 [STEP1] Processing input: Enhanced: Hello World
🎯 [STEP1] Post-callback: Validating step 1 result
🎯 [STEP1] Result validated successfully
🌍 [GLOBAL] After node: step1 (function)
...
```

## 🔧 Key Features Demonstrated

### 1. **State Modification**
- Pre-callbacks can modify state before node execution
- Post-callbacks can modify results after node execution

### 2. **Conditional Execution**
- Pre-callbacks can return custom results to skip node execution
- Useful for implementing conditional logic

### 3. **Error Recovery**
- Error callbacks can set fallback values
- Graceful handling of node failures

### 4. **Monitoring and Logging**
- Global callbacks for application-wide monitoring
- Per-node callbacks for specific node behavior

### 5. **Callback Composition**
- Global and per-node callbacks work together
- Clear execution order: global → per-node

## 🎯 Real-World Use Cases

### 1. **Input Validation**
```go
graph.WithPreNodeCallback(func(ctx context.Context, callbackCtx *graph.NodeCallbackContext, state graph.State) (any, error) {
    if input, exists := state["input"]; exists {
        if inputStr, ok := input.(string); ok && len(inputStr) == 0 {
            return nil, fmt.Errorf("input cannot be empty")
        }
    }
    return nil, nil
})
```

### 2. **Performance Monitoring**
```go
graph.WithPreNodeCallback(func(ctx context.Context, callbackCtx *graph.NodeCallbackContext, state graph.State) (any, error) {
    callbackCtx.ExecutionStartTime = time.Now()
    return nil, nil
}).
WithPostNodeCallback(func(ctx context.Context, callbackCtx *graph.NodeCallbackContext, state graph.State, result any, nodeErr error) (any, error) {
    duration := time.Since(callbackCtx.ExecutionStartTime)
    fmt.Printf("Node %s took %v to execute\n", callbackCtx.NodeID, duration)
    return nil, nil
})
```

### 3. **Result Transformation**
```go
graph.WithPostNodeCallback(func(ctx context.Context, callbackCtx *graph.NodeCallbackContext, state graph.State, result any, nodeErr error) (any, error) {
    if result != nil {
        // Add metadata to result
        return fmt.Sprintf("%s [processed by %s]", result, callbackCtx.NodeID), nil
    }
    return nil, nil
})
```

### 4. **Error Recovery**
```go
graph.WithNodeErrorCallback(func(ctx context.Context, callbackCtx *graph.NodeCallbackContext, state graph.State, err error) {
    // Log error for debugging
    fmt.Printf("Node %s failed: %v\n", callbackCtx.NodeID, err)
    
    // Set fallback state
    state["error_recovery"] = true
    state["fallback_result"] = "default_value"
})
```

## 🔄 Backward Compatibility

The per-node callbacks feature is **fully backward compatible**:

- Existing global callbacks continue to work unchanged
- New per-node callbacks are additive
- No breaking changes to existing APIs

## 🎨 Design Principles

### 1. **Composability**
- Global and per-node callbacks work together seamlessly
- Clear execution order ensures predictable behavior

### 2. **Flexibility**
- Multiple callback types for different use cases
- Callbacks can modify state, results, or just observe

### 3. **Performance**
- Callbacks are only executed when needed
- Minimal overhead when no callbacks are set

### 4. **Error Handling**
- Proper error propagation through callback chain
- Error callbacks for graceful failure handling

This example showcases the power and flexibility of the per-node callbacks system, enabling fine-grained control over graph execution while maintaining clean, readable code.
