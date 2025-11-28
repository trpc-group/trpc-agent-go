# TDesign Chat Front-End for the AG-UI Server

This example demonstrates how to integrate the Go-based AG-UI server with a React front-end built on [TDesign Chat](https://tdesign.woa.com/react-chat). The UI streams Server-Sent Events from the AG-UI endpoint using TDesign Chat's native AG-UI protocol support.

## Features

✨ **Native AG-UI Protocol Support** - TDesign Chat natively supports AG-UI protocol without complex adapters

🧮 **Tool Call Rendering** - Automatically renders tool calls (e.g., calculator) with beautiful UI components

📊 **Streaming Support** - Real-time streaming of AI responses with smooth UX

🎨 **Modern UI** - Clean, professional interface built with TDesign component library

🔧 **Type-Safe** - Full TypeScript support with comprehensive type definitions

## Architecture Comparison

### CopilotKit vs TDesign Chat

| Aspect | CopilotKit | TDesign Chat |
|--------|-----------|--------------|
| AG-UI Support | Requires custom adapter (~150 lines) | Native support (0 lines adapter) |
| Tool Rendering | Manual event parsing and rendering | `useAgentToolcall` hook + component registration |
| Code Complexity | ~700 lines | ~300 lines |
| Bundle Size | Larger (includes CopilotKit runtime) | Smaller (direct AG-UI client) |
| Customization | Limited by framework | Fully customizable components |
| Chinese Support | Requires customization | Built-in |

## Quick Start

### 1. Start the AG-UI Server

First, ensure the Go AG-UI server is running:

```bash
cd ../../server/default
go run main.go
```

The server will start on `http://127.0.0.1:8080/agui`.

### 2. Install Dependencies

```bash
pnpm install   # or npm install
```

### 3. Start the Development Server

```bash
pnpm dev       # or npm run dev
```

### 4. Open in Browser

Open `http://localhost:3000` and start chatting!

Try the default prompt:
```
Calculate 2*(10+11), first explain the idea, then calculate, and finally give the conclusion.
```

## Environment Variables

You can customize the AG-UI endpoint before running `pnpm dev`:

```bash
# Override the default AG-UI endpoint
export NEXT_PUBLIC_AG_UI_ENDPOINT=http://your-server:port/agui
pnpm dev
```

Default: `http://127.0.0.1:8080/agui`

## Project Structure

```
tdesign-chat/
├── app/
│   ├── page.tsx           # Main chat page with tool registration
│   ├── layout.tsx         # Root layout with TDesign styles
│   └── globals.css        # Custom styles for tool cards
├── package.json           # Dependencies
├── tsconfig.json          # TypeScript configuration
├── next.config.mjs        # Next.js configuration
└── README.md             # This file
```

## Key Implementation Details

### Tool Registration

TDesign Chat uses `useAgentToolcall` hook to register tool components:

```tsx
useAgentToolcall([
  {
    name: 'calculator',
    description: 'Calculator tool',
    parameters: [
      { name: 'operation', type: 'string', required: true },
      { name: 'a', type: 'number', required: true },
      { name: 'b', type: 'number', required: true },
    ],
    component: CalculatorTool,  // Custom React component
  },
]);
```

### Chat Configuration

```tsx
const { chatEngine, messages, status } = useChat({
  chatServiceConfig: {
    endpoint: 'http://127.0.0.1:8080/agui',
    protocol: 'agui',  // Enable AG-UI protocol
    stream: true,
  },
});
```

### Tool Component Example

```tsx
const CalculatorTool: React.FC<ToolcallComponentProps<Args, Result>> = ({
  status,   // 'pending' | 'executing' | 'complete' | 'error'
  args,     // Tool arguments
  result,   // Tool execution result
  error,    // Error object if failed
}) => {
  return (
    <Card>
      {/* Render tool UI based on status and data */}
    </Card>
  );
};
```

## Advantages Over CopilotKit

### 1. Simpler Implementation

**CopilotKit** requires:
- Custom `HttpAgent` subclass to transform events
- Complex message parsing logic
- Custom rendering for different message types
- ~700 lines of code

**TDesign Chat** only needs:
- Tool component registration
- ~300 lines of clean, readable code

### 2. Better AG-UI Integration

TDesign Chat is designed specifically for AG-UI protocol:
- Native `TOOL_CALL_*` event handling
- Built-in `STATE_SNAPSHOT` and `STATE_DELTA` support
- Automatic tool call lifecycle management

### 3. More Flexible

- Fully customizable tool components
- Easy to add new tools
- Clean separation of concerns
- Better TypeScript support

## Testing Steps

### Step 1: Start the Go AG-UI Server

Open a terminal and navigate to the server directory:

```bash
cd /Users/caolin/workspace/private/trpc-agent-go/examples/agui/server/default
go run main.go
```

You should see output like:
```
2025-XX-XX XX:XX:XX+08:00       INFO    default/main.go:60      AG-UI: serving agent "agui-agent" on http://127.0.0.1:8080/agui
```

### Step 2: Install Frontend Dependencies

Open another terminal and navigate to the TDesign Chat client:

```bash
cd /Users/caolin/workspace/private/trpc-agent-go/examples/agui/client/tdesign-chat
pnpm install
```

If you don't have pnpm, you can use npm:
```bash
npm install
```

### Step 3: Start the Frontend Dev Server

```bash
pnpm dev
# or
npm run dev
```

You should see:
```
  ▲ Next.js 14.x.x
  - Local:        http://localhost:3000
  - Ready in XXXms
```

### Step 4: Open Browser and Test

1. Open your browser and navigate to `http://localhost:3000`

2. You should see the TDesign Chat interface with the header "AG-UI TDesign Chat Demo"

3. Try the following test cases:

#### Test Case 1: Basic Calculation
**Input:**
```
Calculate 2*(10+11)
```

**Expected Output:**
- AI explains the calculation idea
- Calculator tool is called with:
  - First: `2 * (10+11)` → calls with operation="add", a=10, b=11
  - Then: `2 * 21` → calls with operation="multiply", a=2, b=21
- Final result: 42
- Tool card shows the calculation process

#### Test Case 2: Division
**Input:**
```
What is 100 divided by 5?
```

**Expected Output:**
- Calculator tool called with operation="divide", a=100, b=5
- Result: 20

#### Test Case 3: Power Operation
**Input:**
```
Calculate 2 to the power of 8
```

**Expected Output:**
- Calculator tool called with operation="power", a=2, b=8
- Result: 256

#### Test Case 4: Multiple Operations
**Input:**
```
Calculate 2*(10+11), first explain the idea, then calculate, and finally give the conclusion.
```

**Expected Output:**
- AI explains the mathematical approach
- Multiple tool calls for sub-calculations
- Final conclusion with the answer

### Step 5: Verify Features

Check the following features work correctly:

- ✅ **Streaming Response**: Text appears character by character
- ✅ **Tool Call Display**: Calculator tool card appears with nice UI
- ✅ **Tool Status**: Shows "计算中..." → "计算完成" status
- ✅ **Tool Arguments**: Displays the calculation expression
- ✅ **Tool Result**: Shows the result with green success badge
- ✅ **Action Bar**: Copy, Good, Bad, Replay buttons appear
- ✅ **Regenerate**: Click replay button to regenerate response
- ✅ **Stop**: Click stop button while AI is responding
- ✅ **Scroll**: Auto-scrolls to bottom with new messages

## Troubleshooting

### Issue: Cannot connect to AG-UI server

**Symptoms:**
- Error in console: "Failed to fetch"
- No response from AI

**Solution:**
1. Verify the Go server is running on `http://127.0.0.1:8080/agui`
2. Check server logs for errors
3. Verify the endpoint in browser console network tab

### Issue: Tool calls not rendering

**Symptoms:**
- Tool calls appear as plain text
- No calculator card UI

**Solution:**
1. Check browser console for errors
2. Verify `useAgentToolcall` is called before `useChat`
3. Ensure tool name matches exactly: `'calculator'`

### Issue: CORS errors

**Symptoms:**
- Browser console shows CORS policy error

**Solution:**
The Go server should handle CORS automatically. If not:
1. Check server CORS configuration
2. Verify you're accessing from `localhost:3000`

### Issue: Styles not loading

**Symptoms:**
- UI looks broken, no TDesign styles

**Solution:**
1. Verify imports in `layout.tsx`:
   ```tsx
   import 'tdesign-react/es/style/index.css';
   import '@tdesign-react/chat/es/style/index.css';
   ```
2. Clear Next.js cache: `rm -rf .next`
3. Restart dev server

## Example Interaction

```
User: Calculate 2*(10+11)