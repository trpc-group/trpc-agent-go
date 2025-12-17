# HTTP Web Fetch Tool Example

This example demonstrates how to use the HTTP web fetch tool with an AI agent for interactive conversations. The tool enables fetching and extracting content from web pages, converting HTML to markdown for better readability, and supporting various text formats.

## Running the Example

### Using environment variables:

```bash
export OPENAI_API_KEY="your-api-key-here"
export OPENAI_BASE_URL="https://api.openai.com/v1"  # Optional
go run main.go
```

### Using custom model:

```bash
export OPENAI_API_KEY="your-api-key-here"
go run main.go -model gpt-4o-mini
```

## Example Session

```
amdahliu@AMDAHLIU-MC2 trpc-agent-go % ./dpskv3.sh
🚀 HTTP Web Fetch Chat Demo
Model: deepseek-v3-local-II
Type 'exit' to end the conversation
Available tools: web_fetch
==================================================
✅ Web fetch chat ready! Session: web-fetch-session-1763989894

💡 Try asking questions like:
   - Summarize the content from https://example.com
   - Fetch and compare https://site1.com and https://site2.com
   - What's on the homepage of https://news.ycombinator.com
   - Extract the main points from https://blog.example.com/article
   - Get the API documentation from https://api.example.com/docs

ℹ️  Note: The tool supports HTML, JSON, XML, and plain text formats

👤 You: Summarize the content from https://ai.google.dev/gemini-api/docs/text-generation
🤖 Assistant: 🌐 Web fetch initiated:
   • web_fetch (ID: chatcmpl-tool-2f80eb6504fc43b0adb62f36f21ee339)
     Args: {"urls":["https://ai.google.dev/gemini-api/docs/text-generation"]}

🔄 Fetching web content...
✅ Fetch result (ID: chatcmpl-tool-2f80eb6504fc43b0adb62f36f21ee339): {"results":[{"retrieved_url":"https://ai.google.dev/gemini-api/docs/text-generation","status_code":200,"content_type":"text/html","content":"[Skip to main content](#main-content)\n\n[![Gemini API](htt...

🤖 Assistant: The page provides a comprehensive guide on using the Gemini API for text generation. Here's a summary of the key points:

### **Text Generation with Gemini API**
1. **Basic Text Generation**:
   - The API can generate text from various inputs (text, images, video, audio).
   - Example code snippets are provided for Python, JavaScript, Go, Java, REST, and Apps Script.

2. **Thinking with Gemini 2.5**:
   - Models like Gemini 2.5 Flash and Pro have "thinking" enabled by default for enhanced quality.
   - Thinking can be disabled by setting the `thinking_budget` to zero.

3. **System Instructions**:
   - You can guide the model's behavior using system instructions (e.g., "You are a cat. Your name is Neko.").
   - Examples are provided for multiple programming languages.

4. **Multimodal Inputs**:
   - The API supports combining text with media files (e.g., images).
   - Code examples demonstrate how to process multimodal inputs.

5. **Streaming Responses**:
   - For real-time interactions, streaming allows incremental responses.
   - Examples are provided for streaming in Python, JavaScript, Go, Java, REST, and Apps Script.

6. **Multi-Turn Conversations (Chat)**:
   - The SDKs support chat functionality to maintain conversation history.
   - Examples show how to implement multi-turn conversations and streaming chats.

7. **Supported Models**:
   - All Gemini models support text generation. Details about models and their capabilities are available on the [Models](https://ai.google.dev/gemini-api/docs/models) page.

8. **Best Practices**:
   - **Prompting Tips**: Use zero-shot or few-shot prompts for tailored outputs.
   - **Structured Output**: The API can generate structured outputs like JSON.

### **Next Steps**
- Try the [Gemini API getting started Colab](https://colab.research.google.com/github/google-gemini/cookbook/blob/main/quickstarts/Get_started.ipynb).
- Explore multimodal capabilities (image, video, audio, document understanding).

For more details, refer to the [official documentation](https://ai.google.dev/gemini-api/docs/text-generation).

👤 You: exit
👋 Goodbye!
```
