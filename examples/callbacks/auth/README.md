# Authentication and Authorization Example

This example demonstrates how to use **Invocation State** to pass user context through the agent execution flow and implement authentication and authorization checks in tool callbacks.

## Overview

This example shows a practical use case of `Invocation.State`: passing user information (ID, role, permissions) from the initial request through the entire agent execution, including tool calls, to implement:

- **User Context Propagation**: Store user information in invocation state
- **Tool Authorization**: Check user permissions before executing tools
- **Audit Logging**: Track which user called which tools
- **Custom Middleware**: Inject user context from request headers/metadata

## Scenario

A multi-tenant system where:

- Different users have different roles (admin, user, guest)
- Some tools require specific permissions (e.g., only admins can delete data)
- All tool calls should be logged with user information for audit purposes

## Key Features

- **User Context Storage**: Store user info in `Invocation.State` at the start
- **Permission Checks**: Validate user permissions in `BeforeToolCallback`
- **Audit Trail**: Log all tool calls with user context in callbacks
- **Graceful Degradation**: Handle missing user context appropriately

## File Structure

- `main.go`: Entry point, CLI interface, user context injection
- `callbacks.go`: Callback implementations for authorization and audit
- `tools.go`: Tool implementations with different permission requirements
- `user.go`: User context data structures and permission definitions

## Implementation Details

### User Context Structure

```go
type UserContext struct {
    UserID      string
    Role        string
    Permissions []string
}
```

### State Key Convention

- User context: `"custom:user_context"` - stores the UserContext object
- Audit log: `"custom:audit_log"` - accumulates audit entries

### Authorization Flow

1. **Inject User Context** (in main.go):

   ```go
   // Before running agent, inject user context
   userCtx := &UserContext{
       UserID:      userID,
       Role:        role,
       Permissions: getPermissions(role),
   }
   // Store in invocation state via BeforeAgentCallback
   ```

2. **Check Permissions** (in BeforeToolCallback):

   ```go
   // Get user context from invocation state
   userCtx := getUserContext(inv)

   // Check if user has required permission
   if !hasPermission(userCtx, toolName) {
       return nil, fmt.Errorf("permission denied: user %s cannot use %s",
           userCtx.UserID, toolName)
   }
   ```

3. **Audit Logging** (in AfterToolCallback):
   ```go
   // Log tool execution with user context
   logAudit(userCtx, toolName, args, result, err)
   ```

## Available Tools

- **read_file**: Read file contents (requires `read` permission)
- **write_file**: Write file contents (requires `write` permission)
- **delete_file**: Delete file (requires `admin` role)
- **list_files**: List files (no permission required)

## User Roles

- **admin**: Full access to all tools
- **user**: Can read and write files, but cannot delete
- **guest**: Can only list and read files

## Running the Example

### Prerequisites

```bash
export OPENAI_API_KEY="your-api-key"
```

### Run with Different Users

```bash
# Run as admin (full access)
go run . --user-id alice --role admin

# Run as regular user (limited access)
go run . --user-id bob --role user

# Run as guest (read-only)
go run . --user-id charlie --role guest
```

### Example Interactions

**As admin:**

```
👤 You (alice, admin): delete the old_data.txt file
✅ Permission granted: alice can use delete_file
🔧 Tool: delete_file
📝 Audit: User alice (admin) called delete_file
🤖 Assistant: The file has been deleted successfully.
```

**As regular user:**

```
👤 You (bob, user): delete the old_data.txt file
❌ Permission denied: user bob cannot use delete_file
🤖 Assistant: I don't have permission to delete files. Only administrators can perform this action.
```

**As guest:**

```
👤 You (charlie, guest): read the config.txt file
✅ Permission granted: charlie can use read_file
🔧 Tool: read_file
📝 Audit: User charlie (guest) called read_file
🤖 Assistant: Here are the contents: [file contents]
```

## Key Concepts

### 1. User Context Injection

User context is injected at the beginning of agent execution:

```go
// BeforeAgentCallback
func(ctx context.Context, args *agent.BeforeAgentArgs) (*agent.BeforeAgentResult, error) {
    // Get user info from somewhere (e.g., request metadata)
    userCtx := &UserContext{
        UserID:      getUserIDFromRequest(),
        Role:        getRoleFromRequest(),
        Permissions: getPermissionsFromRequest(),
    }

    // Store in invocation state
    args.Invocation.SetState("custom:user_context", userCtx)
    return nil, nil
}
```

### 2. Permission Checking

Tool callbacks check permissions before execution:

```go
// BeforeToolCallback
func(ctx context.Context, args *tool.BeforeToolArgs) (*tool.BeforeToolResult, error) {
    inv, _ := agent.InvocationFromContext(ctx)

    // Get user context from state
    userCtxVal, ok := inv.GetState("custom:user_context")
    if !ok {
        return nil, errors.New("user context not found")
    }
    userCtx := userCtxVal.(*UserContext)

    // Check permission
    if !hasPermission(userCtx, args.ToolName) {
        return nil, errors.New("permission denied")
    }

    return nil, nil
}
```

### 3. Audit Logging

All tool calls are logged with user context:

```go
// AfterToolCallback
func(ctx context.Context, args *tool.AfterToolArgs) (*tool.AfterToolResult, error) {
    inv, _ := agent.InvocationFromContext(ctx)
    userCtx := getUserContext(inv)

    // Log audit entry
    auditEntry := fmt.Sprintf("[%s] User %s (%s) called %s",
        time.Now().Format(time.RFC3339),
        userCtx.UserID,
        userCtx.Role,
        args.ToolName,
    )

    // Append to audit log in state
    appendAuditLog(inv, auditEntry)

    return nil, nil
}
```

## Benefits of Using Invocation State

1. **Clean Architecture**: No need for global variables or thread-local storage
2. **Automatic Scoping**: State is automatically scoped to the invocation
3. **Thread-Safe**: Built-in concurrency protection
4. **Type-Safe**: Can store any Go type
5. **Lifecycle Management**: State is cleaned up after invocation completes

## Comparison with Other Approaches

### ❌ Global Variables

```go
// Bad: Not thread-safe, not scoped to invocation
var currentUser *UserContext
```

### ❌ Context Values

```go
// Bad: Context is immutable, can't modify in callbacks
ctx = context.WithValue(ctx, "user", userCtx)
```

### ✅ Invocation State

```go
// Good: Thread-safe, invocation-scoped, mutable
inv.SetState("custom:user_context", userCtx)
```

## Related Examples

- [Timer Example](../timer/) - Timing and telemetry with Invocation State
- [Multi-turn Chat with Callbacks](../main.go) - Comprehensive callback examples

## Customization

To adapt this example to your use case:

1. Define your own user context structure
2. Implement your permission checking logic
3. Integrate with your authentication/authorization system
4. Add custom audit logging or metrics
5. Extend with additional middleware (rate limiting, quota management, etc.)
