//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package main demonstrates the Wikipedia search tool usage.
package main

import (
	"bufio"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/model/openai"
	"trpc.group/trpc-go/trpc-agent-go/runner"
	"trpc.group/trpc-go/trpc-agent-go/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool/wikipedia"
)

var (
	modelName  = flag.String("model", "deepseek-v4-flash", "Name of the model to use")
	language   = flag.String("lang", "en", "Wikipedia language (en, zh, es, etc.)")
	maxResults = flag.Int("maxresults", 3, "Maximum number of search results")
)

func main() {
	// Parse command line flags
	flag.Parse()

	fmt.Printf("🔍 Wikipedia Search Tool Demo\n")
	fmt.Printf("Model: %s\n", *modelName)
	fmt.Printf("Language: %s\n", *language)
	fmt.Println(strings.Repeat("=", 50))

	// Create and run the chat
	chat := &wikiChat{
		modelName:  *modelName,
		language:   *language,
		maxResults: *maxResults,
	}

	if err := chat.run(); err != nil {
		log.Fatalf("Chat failed: %v", err)
	}
}

type wikiChat struct {
	modelName  string
	language   string
	runner     runner.Runner
	userID     string
	sessionID  string
	maxResults int
}

func (c *wikiChat) run() error {
	ctx := context.Background()

	if err := c.setup(ctx); err != nil {
		return fmt.Errorf("setup failed: %w", err)
	}

	return c.startChat(ctx)
}

func (c *wikiChat) setup(_ context.Context) error {
	// Create model instance
	modelInstance := openai.New(c.modelName)

	// Create Wikipedia tool set
	wikiToolSet, err := wikipedia.NewToolSet(
		wikipedia.WithLanguage(c.language),
		wikipedia.WithMaxResults(c.maxResults),
		wikipedia.WithUserAgent("trpc-agent-go-wiki-search"),
	)
	if err != nil {
		return fmt.Errorf("create wiki tool set: %w", err)
	}

	fmt.Printf("🔧 Tool available: Wiki Search Tool\n")
	fmt.Printf("📍 Language: %s\n", c.language)
	fmt.Printf("📊 Max results: %d\n", c.maxResults)
	fmt.Printf("👤 User-Agent: trpc-agent-go-wiki-search\n\n")

	// Create generation config
	genConfig := model.GenerationConfig{
		MaxTokens:   intPtr(2000),
		Temperature: floatPtr(0.7),
		Stream:      true,
	}

	// Create LLM agent
	llmAgent := llmagent.New(
		"wiki-search-agent",
		llmagent.WithModel(modelInstance),
		llmagent.WithDescription("An AI assistant with Wikipedia search capability"),
		llmagent.WithInstruction(c.buildInstruction()),
		llmagent.WithGenerationConfig(genConfig),
		llmagent.WithToolSets([]tool.ToolSet{wikiToolSet}),
	)

	// Create runner
	c.runner = runner.NewRunner("wiki-search-app", llmAgent)

	// Setup identifiers
	c.userID = "user"
	c.sessionID = fmt.Sprintf("session-%d", time.Now().Unix())

	fmt.Printf("✅ Chat ready! Ask me anything about Wikipedia.\n\n")

	return nil
}

// constructs the instruction for the LLM agent(felxibly change to fit your needs)
func (c *wikiChat) buildInstruction() string {
	return fmt.Sprintf(`You are a helpful AI assistant with access to Wikipedia search.

You have access to the WIKI SEARCH tool which provides:
- Comprehensive Wikipedia search with detailed metadata
- Article titles, URLs, and descriptions
- Page statistics (word count, size, last modified)
- Page IDs and namespace information
- **MULTI-LANGUAGE SUPPORT**: You can search ANY Wikipedia language edition!

IMPORTANT - Language Selection:
- You can specify the 'language' parameter in your wiki_search calls
- Common languages: 'en' (English), 'zh' (Chinese), 'es' (Spanish), 'fr' (French), 'de' (German), 'ja' (Japanese)
- Choose the language based on:
  * The user's question language (if user asks in Chinese, search 'zh' Wikipedia)
  * The topic's origin (for Chinese topics, 'zh' might have more details)
  * User's explicit request
- Default language if not specified: %s
- Example: For "什么是人工智能?" use language='zh'
- Example: For "What is AI?" use language='en'

Use this tool to:
- Answer questions about any topic
- Provide factual information from Wikipedia
- Research subjects in depth
- Get current information about topics

Maximum results per search: %d (do not request more than %d results)
Always provide helpful and accurate information based on the search results.`, c.language, c.maxResults, c.maxResults)
}

func (c *wikiChat) startChat(ctx context.Context) error {
	scanner := bufio.NewScanner(os.Stdin)

	c.printExamples()

	for {
		fmt.Print("👤 You: ")
		if !scanner.Scan() {
			break
		}

		userInput := strings.TrimSpace(scanner.Text())
		if userInput == "" {
			continue
		}

		// Handle special commands
		switch strings.ToLower(userInput) {
		case "exit", "/exit", "exit()", "quit":
			fmt.Println("👋 Goodbye!")
			return nil
		case "help", "/help":
			c.printExamples()
			continue
		case "info", "/info":
			c.printToolInfo()
			continue
		}

		// Process the user message
		if err := c.processMessage(ctx, userInput); err != nil {
			fmt.Printf("❌ Error: %v\n", err)
		}

		fmt.Println()
	}

	if err := scanner.Err(); err != nil {
		return fmt.Errorf("input scanner error: %w", err)
	}

	return nil
}

func (c *wikiChat) printExamples() {
	fmt.Println("💡 Example questions:")
	fmt.Println()
	fmt.Println("   Basic queries:")
	fmt.Println("   - What is artificial intelligence?")
	fmt.Println("   - Tell me about quantum computing")
	fmt.Println("   - Who is Albert Einstein?")
	fmt.Println()
	fmt.Println("   Multi-language queries:")
	fmt.Println("   - 什么是人工智能？(AI will auto search Chinese Wikipedia)")
	fmt.Println("   - ¿Qué es la inteligencia artificial? (Spanish)")
	fmt.Println("   - Search for '人工智能' in Chinese Wikipedia")
	fmt.Println("   - Find information about 'Beijing' in zh Wikipedia")
	fmt.Println()
	fmt.Println("   Detailed queries:")
	fmt.Println("   - How long is the Python programming article?")
	fmt.Println("   - When was the machine learning article last updated?")
	fmt.Println("   - Compare the sizes of different programming language articles")
	fmt.Println()
	fmt.Println("   Research queries:")
	fmt.Println("   - Find information about neural networks")
	fmt.Println("   - Research the history of computer science")
	fmt.Println("   - Get details about climate change")
	fmt.Println()
	fmt.Println("💬 Commands:")
	fmt.Println("   /help  - Show this help")
	fmt.Println("   /info  - Show tool information")
	fmt.Println("   /exit  - Exit the program")
	fmt.Println()
}

func (c *wikiChat) printToolInfo() {
	fmt.Println("\n🔧 Wiki Search Tool Information:")
	fmt.Println()
	fmt.Println("🔍 Wikipedia Search")
	fmt.Printf("   Default Language: %s\n", c.language)
	fmt.Println("   🌍 Multi-Language: AI can choose any Wikipedia language!")
	fmt.Println("   Speed: ⚡⚡⚡⚡ (fast)")
	fmt.Println("   Detail: ⭐⭐⭐⭐⭐ (comprehensive)")
	fmt.Println()
	fmt.Println("🌐 Supported Languages:")
	fmt.Println("   • en - English")
	fmt.Println("   • zh - 中文 (Chinese)")
	fmt.Println("   • es - Español (Spanish)")
	fmt.Println("   • fr - Français (French)")
	fmt.Println("   • de - Deutsch (German)")
	fmt.Println("   • ja - 日本語 (Japanese)")
	fmt.Println("   • And many more...")
	fmt.Println()
	fmt.Println("📊 Returns:")
	fmt.Println("   • Article title and URL")
	fmt.Println("   • Description/snippet")
	fmt.Println("   • Page ID and namespace")
	fmt.Println("   • Word count and page size")
	fmt.Println("   • Last modified timestamp")
	fmt.Println("   • Total search hits")
	fmt.Println("   • Search execution time")
	fmt.Println()
	fmt.Println("🎯 Best for:")
	fmt.Println("   • Research and fact-checking")
	fmt.Println("   • Getting comprehensive topic information")
	fmt.Println("   • Academic and educational use")
	fmt.Println("   • Detailed article analysis")
	fmt.Println("   • Cross-language information retrieval")
	fmt.Println()
}

func (c *wikiChat) processMessage(ctx context.Context, userMessage string) error {
	message := model.NewUserMessage(userMessage)

	eventChan, err := c.runner.Run(ctx, c.userID, c.sessionID, message)
	if err != nil {
		return fmt.Errorf("failed to run agent: %w", err)
	}

	return c.processResponse(eventChan)
}

func (c *wikiChat) processResponse(eventChan <-chan *event.Event) error {
	fmt.Print("🤖 Assistant: ")

	var fullContent strings.Builder
	var toolUsed bool

	for evt := range eventChan {
		if evt.Error != nil {
			fmt.Printf("\n❌ Error: %s\n", evt.Error.Message)
			continue
		}

		// Detect tool calls
		if evt.Response != nil && len(evt.Response.Choices) > 0 {
			choice := evt.Response.Choices[0]
			if len(choice.Message.ToolCalls) > 0 {
				for _, toolCall := range choice.Message.ToolCalls {
					toolUsed = true
					fmt.Printf("\n🔍 Wiki Search Tool Called:\n")
					fmt.Printf("   Function: %s\n", toolCall.Function.Name)

					// 解析并显示搜索参数
					if params := c.parseToolArguments(string(toolCall.Function.Arguments)); params != nil {
						if query, ok := params["query"].(string); ok {
							fmt.Printf("   Query: %s\n", query)
						}
						// 显示语言参数
						lang := c.language // 默认语言
						if langParam, ok := params["language"].(string); ok && langParam != "" {
							lang = langParam
							fmt.Printf("   Language: %s (AI selected)\n", lang)
						} else {
							fmt.Printf("   Language: %s (default)\n", lang)
						}
						// 构建Wikipedia搜索URL
						if query, ok := params["query"].(string); ok {
							searchURL := fmt.Sprintf("https://%s.wikipedia.org/w/api.php?action=query&list=search&srsearch=%s",
								lang, strings.ReplaceAll(query, " ", "%20"))
							fmt.Printf("   🌐 API URL: %s\n", searchURL)
						}
						if limit, ok := params["limit"]; ok {
							fmt.Printf("   Limit: %v\n", limit)
						}
						if includeAll, ok := params["include_all"]; ok {
							fmt.Printf("   Include All: %v\n", includeAll)
						}
					}
				}
				fmt.Print("\n🤖 Assistant: ")
			}
		}

		// Handle tool results (when the tool returns data)
		if evt.Response != nil && len(evt.Response.Choices) > 0 {
			choice := evt.Response.Choices[0]
			// 检查是否是工具返回的结果
			if choice.Message.Role == "tool" {
				fmt.Printf("\n📊 Wiki Search Results Received:\n")
				c.displayToolResults(choice.Message.Content)
				fmt.Print("\n🤖 Assistant: ")
				continue
			}
		}

		// Handle content
		if evt.Response != nil && len(evt.Response.Choices) > 0 {
			content := evt.Response.Choices[0].Delta.Content
			if content != "" {
				fmt.Print(content)
				fullContent.WriteString(content)
			}
		}

		if evt.Done {
			fmt.Printf("\n")
			if toolUsed {
				fmt.Printf("   [Used: 🔍 Wiki Search]\n")
			}
			break
		}
	}

	return nil
}

// parseToolArguments parses tool call arguments JSON
func (c *wikiChat) parseToolArguments(args string) map[string]any {
	var params map[string]any
	if err := json.Unmarshal([]byte(args), &params); err != nil {
		fmt.Printf("   ⚠️ Failed to parse arguments: %v\n", err)
		return nil
	}
	return params
}

// displayToolResults displays formatted tool results
func (c *wikiChat) displayToolResults(content string) {
	var result map[string]any
	if err := json.Unmarshal([]byte(content), &result); err != nil {
		fmt.Printf("   Raw Result: %s\n", content)
		return
	}

	// 显示搜索结果摘要
	if query, ok := result["query"].(string); ok {
		fmt.Printf("   Query: %s\n", query)
	}
	if language, ok := result["language"].(string); ok {
		fmt.Printf("   Language: %s Wikipedia\n", language)
	}
	if summary, ok := result["summary"].(string); ok {
		fmt.Printf("   Summary: %s\n", summary)
	}
	if searchTime, ok := result["search_time"].(string); ok {
		fmt.Printf("   Search Time: %s\n", searchTime)
	}

	// 显示找到的文章链接
	if results, ok := result["results"].([]any); ok {
		fmt.Printf("   📚 Found Articles:\n")
		for i, item := range results {
			if article, ok := item.(map[string]any); ok {
				if title, ok := article["title"].(string); ok {
					if url, ok := article["url"].(string); ok {
						fmt.Printf("      %d. %s\n", i+1, title)
						fmt.Printf("         🔗 %s\n", url)
						if desc, ok := article["description"].(string); ok && len(desc) > 0 {
							// 截断描述以避免输出过长
							// if len(desc) > 100 {
							// 	desc = desc[:100] + "..."
							// }
							fmt.Printf("         📝 desc：%s\n", desc)
						}
					}
				}
			}
		}
	}
}

func intPtr(i int) *int {
	return &i
}

func floatPtr(f float64) *float64 {
	return &f
}
