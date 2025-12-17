# Gemini Web Fetch Tool Example

This example demonstrates how to use the Gemini web fetch tool with an AI agent for interactive conversations. The tool leverages Gemini's URL Context feature for server-side web fetching and intelligent content analysis.


## Running the Example

### Using environment variables:

```bash
export OPENAI_API_KEY="your-openai-api-key"
export GEMINI_API_KEY="your-gemini-api-key"
go run main.go
```
### Example Session

```
 trpc-agent-go % ./dpskv3.sh
🚀 Gemini Web Fetch Chat Demo
Model: deepseek-v3-local-II
Gemini Fetch Model: gemini-2.5-flash
Type 'exit' to end the conversation
Available tools: gemini_web_fetch
==================================================
✅ Gemini web fetch chat ready! Session: gemini-web-fetch-session-1763990940

💡 Try asking questions like:
   - Summarize https://example.com
   - Compare https://site1.com and https://site2.com
   - What's the main content of https://news.ycombinator.com
   - Analyze the article at https://blog.example.com/post
   - Extract key points from https://ai.google.dev/gemini-api/docs/url-context

ℹ️  Note: URLs are automatically detected and fetched by Gemini's server

👤 You: compare https://platform.claude.com/docs/en/agents-and-tools/tool-use/web-fetch-tool#how-to-use-web-fetch  and https://ai.google.dev/gemini-api/docs/url-context
🤖 Assistant: 🌐 Gemini web fetch initiated:
   • gemini_web_fetch (ID: chatcmpl-tool-309371dd1ac24ee5a76f6aa7a6ebe8b7)
     Prompt: {"prompt": "Compare the web fetch tool documentation for Claude AI (https://platform.claude.com/docs/en/agents-and-tools/tool-use/web-fetch-tool#how-to-use-web-fetch) and Google's Gemini API (https://ai.google.dev/gemini-api/docs/url-context). Highlight the key differences and similarities in their functionality, usage, and capabilities."}

🔄 Gemini fetching and analyzing content...
✅ Fetch result (ID: chatcmpl-tool-309371dd1ac24ee5a76f6aa7a6ebe8b7): {"content":"Both Claude AI's \"Web fetch tool\" and Google's Gemini API's \"URL context\" feature allow their respective models to access and process content from specified URLs to enhance their respo...

🤖 Assistant: Here’s a detailed comparison of Claude AI's **Web Fetch Tool** and Google's **Gemini API URL Context** feature:

### **Similarities:**
1. **Core Functionality**: Both tools allow AI models to fetch and process content from URLs for tasks like summarization, analysis, and data extraction.
2. **PDF Support**: Both can retrieve and process PDF documents.
3. **Token Consumption**: Content fetched from URLs counts toward input token limits and affects pricing.
4. **Error Handling**: Both provide mechanisms to handle inaccessible or unprocessable URLs.
5. **Integration with Search**: Both can be combined with their respective search tools (Claude's Web Search and Gemini's Grounding with Google Search) for broader information gathering.

---

### **Key Differences:**

#### **1. Usage and Activation:**
- **Claude**:
  - Requires enabling a beta header (`web-fetch-2025-09-10`) in API requests.
  - Explicitly defined in the `tools` array with a `type` and `name`.
- **Gemini**:
  - Activated by including `{"url_context": {}}` in the `tools` configuration.
  - URLs are included directly in the prompt's `contents`.

#### **2. Content Retrieval Mechanism:**
- **Claude**:
  - Fetches full text content from URLs and extracts text from PDFs.
  - Does not support dynamically rendered JavaScript websites.
- **Gemini**:
  - Uses a two-step process: first checks an internal cache, then fetches live if needed.
  - Balances speed, cost, and access to fresh data.

#### **3. Security and Control:**
- **Claude**:
  - Emphasizes data exfiltration risks; recommends trusted environments.
  - Offers fine-grained control with `max_uses`, `allowed_domains`, and `blocked_domains`.
- **Gemini**:
  - Content undergoes safety checks.
  - Documentation lacks detailed controls like Claude's domain restrictions.

#### **4. Supported Content Types:**
- **Claude**:
  - Supports web pages and PDFs; no JavaScript-rendered sites.
- **Gemini**:
  - Supports text-based formats (HTML, JSON, etc.), images (PNG, JPEG), and PDFs.
  - Excludes paywalled content, YouTube videos, and large files (>34MB per URL).

#### **5. Citations:**
- **Claude**:
  - Citations are optional and must be enabled (`"citations": {"enabled": true}`).
- **Gemini**:
  - Provides `url_context_metadata` for verification but lacks explicit in-text citations.

#### **6. Model Support:**
- **Claude**:
  - Available on specific versions of Sonnet, Haiku, and Opus models.
- **Gemini**:
  - Supports models like `gemini-2.5-pro` and `gemini-2.5-flash`.

#### **7. Additional Features:**
- **Claude**:
  - Offers prompt caching, streaming, and batch request integration.
- **Gemini**:
  - Provides `url_context_metadata` and `usage_metadata` for debugging and token tracking.

---

### **Summary:**
- **Claude** excels in security controls and fine-grained URL management but has stricter limitations on content types.
- **Gemini** offers broader content support and a hybrid retrieval system but lacks some of Claude's granular security features.

Choose Claude for stricter control over sensitive data and Gemini for versatility in handling diverse content types.
```

