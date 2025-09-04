# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.0.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).


## [0.1.0] - 2025-09-03

### Features
- **storage**: Reimplement Elasticsearch vectorstore architecture with enhanced performance and reliability. (#277)
- **graph**: Introduce new reducers to enhance graph messages processing for better workflow orchestration. (#280)
- **graph**: Add node responses keys for improved graph node communication. (#278)
- **graph**: Support subagent in graph agent node for hierarchical agent execution. (#246)
- **a2a**: Support streaming protocol for agent-to-agent communication with real-time data transfer. (#248)
- **agent**: Support structured output for better response formatting and validation. (#233)
- **model**: Add OpenAI Batch API support for efficient batch processing. (#231)
- **tool**: Support file tool set for enhanced file operations. (#229)
- **session**: Add cleaning functionality to the session service for better resource management. (#244)
- **memory**: Introduce custom instruction builder for memory services with improved configuration. (#241)
- **tool**: Support nested object properties in MCP schema conversion for complex data structures. (#262)
- **agent**: Wrap invocation context in runner for better execution context management. (#268)
- **llmflow**: Enhance transfer_to_agent tool compatibility for improved agent transfer workflows. (#249)
- **artifact**: Artifact service with in-memory and cos support (#272) 

### Bug Fixes
- **session**: Fix message truncated in session to ensure complete message delivery. (#276)
- **session**: Fix Redis session events issues for proper event ordering and delivery. (#238)
- **chunking**: Fix markdown chunking issue to prevent text corruption during processing. (#281)
- **examples**: Fix React example implementation and dependencies. (#243)
- **log**: Fix log.SetLevel not taking effect issue for proper logging level control. (#234)
- **examples**: Fix version of go.mod in examples and session modules. (#282)

### Examples
- **examples**: Add retry mechanism examples and documentation for MCP tools with comprehensive usage guides. (#264)
- **examples**: Refactor callbacks examples and add invocation context usage demonstrations. (#265)
- **examples**: Move specialized agents to their own file for better organization. (#260)

### Documentation
- **docs**: Add comprehensive Memory documentation with detailed usage instructions. (#271)
- **docs**: Add documentation chapter in README and zh-CN README for better accessibility. (#263)
- **docs**: Add full documentation with comprehensive guides and API references. (#257)
- **docs**: Update README with new examples and usage instructions. (#259)
- **docs**: Add streaming output configuration in documentation. (#261)
- **docs**: Update ecosystem path for better navigation. (#275)
- **docs**: Building documentation with mkdocs for improved user experience. (#247)

### Enhancements
- **agent**: Enhance LLMAgent configuration validation in New function for better error handling. (#232)
- **log**: Enhance error logging for tool call failures with detailed error information. (#242)
- **graph**: Properly align user, tool, and assistant messages for better conversation flow. (#267)
- **storage**: Refactor storage and add tests for Redis and TcVector clients with improved reliability. (#235)
- **memory**: Refactor memory service for clarity and better performance. (#237)
- **agent**: Refactor for cyclomatic complexity and replace 'interface{}' with 'any' for modern Go practices. (#255)
- **a2a**: Use agent URL as fallback URL for agent card for improved service discovery. (#251)

### Dependencies
- **deps**: Bump trpc.group/trpc-go/trpc-mcp-go to v0.0.4 for latest MCP protocol support. (#274)

### Chore
- **lsc**: Update license and gofmt files for compliance. (#273)
- **ci**: Allow manual triggering of deployment and update trigger conditions. (#254)
- **ci**: Upgrade codecov version to v5 for better code coverage reporting. (#230)
- **test**: Remove duplicated dummy tool implementations for cleaner codebase. (#250)


## [0.0.4] - 2025-08-18

### Features
- **agent**: Add A2A agent support for agent-to-agent communication. (#222)
- **agent**: Support input schema for agent configuration. (#212)
- **agent**: Implement multi-model switching functionality with dynamic model selection. (#224)
- **llmagent**: Add content prefix option for enhanced prompt customization. (#219)
- **model**: Add reasoning content to non-streaming and final response. (#226)
- **graph**: Switch to Pregel engine with rich event output for better workflow orchestration. (#220)
- **graphagent**: Support setting of initial state at each run for improved state management. (#210)
- **tool**: Enhance JSON schema support with descriptions, enum and required fields for input/output structs. (#216)

### Bug Fixes
- **session**: Fix Redis session event order issue to ensure proper event sequencing. (#223)

### Examples
- **examples**: Add comprehensive model retry example with detailed usage documentation. (#218)
- **examples**: Enhance model example with detailed README and improved output messages. (#221)
- **examples**: Provide token tracker example for monitoring token usage. (#214)
- **examples**: Demonstrate the usage of placeholders for dynamic content. (#213)
- **examples**: Enhance knowledge chat example with multiple embedder support and improved configuration. (#211)
- **examples/telemetry**: Refactor to use environment variables for LangFuse configuration. (#215)
- **examples**: Reorganize model retry example structure for better clarity. (#225)
- **examples**: Update placeholder example. (#228)

### Documentation
- **docs**: Update license files across the project. (#217)

### Dependencies
- **deps**: Bump A2A and MCP requirement versions. （#227）


## [0.0.3] - 2025-08-13

### Features
- **telemetry**: Support HTTP protocol to integrate Langfuse. (#203)
- **storage**: Add extra options for Redis storage. (#202)
- **memory**: Add Redis memory service support. (#172)
- **knowledge**: Add metadata handling and consistency tests. (#201)
- **planner**: Add `actionPreamble` for ReAct prompt. (#169)
- **processor**: Add time-aware processor. (#168)
- **model**: Add support for `reasoning_content` field. (#167)
- **agent**: Support output key and output schema. (#153)
- **agent**: Export `Options` struct for easier reuse. (#163)
- **model**: Suppress events during tool-call chunks. (#165)
- **model**: Suppress empty chat chunks and add default object for completion. (#164)
- **chunking**: Ensure safe UTF-8 chunking. (#170)

### Bug Fixes
- **model**: Fix issue on internal platform. (#204)
- **mcp**: Fix default values and enum support. (#166)
- **redis**: Remove over-strict validation of Redis URL to avoid false errors. (#205)

### Chore
- **gomod**: Update go.mod in submodules. (#162)

### Examples
- **examples**: Update Cycle example. (#171)


## [0.0.2] - 2025-08-07

### Features
- **tool/stream**: Add context and error for streaming tool call. (#160)

### Bug Fixes
- **model**: Fix issue of tool call on different platform. (#159)

## [0.0.1] - 2025-08-07

### Features

#### Core Framework
- **Agent Interface**: Core `agent.Agent` interface with support for sub-agents, tools, and execution lifecycle.
- **Runner System**: Session-based agent execution with event streaming and lifecycle management.
- **Event System**: Comprehensive event-driven architecture for tracking agent execution progress.
- **Model Integration**: Support for multiple LLM providers including OpenAI and Google GenAI.

#### Built-in Agents
- **LLMAgent**: Wrapper for chat-completion models with configurable system instructions and parameters.
- **ChainAgent**: Sequential execution of multiple sub-agents in a pipeline.
- **ParallelAgent**: Concurrent execution of sub-agents with result merging.
- **CycleAgent**: Iterative execution with termination conditions.
- **GraphAgent**: Complex workflow orchestration with conditional routing and state management.

#### Tool System
- **Tool Interface**: Unified tool specification with JSON schema validation.
- **Function Tools**: JSON-schema based function tools with automatic argument validation.
- **Streamable Tools**: Support for streaming tool responses and progressive data delivery.
- **Tool Merging**: Intelligent merging of tool results and responses.
- **Built-in Tools**: DuckDuckGo search, file operations, and document processing tools.
- **MCP Integration**: Model Context Protocol (MCP) support for dynamic tool execution.

#### Planning & Reasoning
- **Planner Interface**: Extensible planning system for agent reasoning.
- **Built-in Planner**: Simple planning with system instruction injection.
- **ReAct Planner**: Reasoning and Acting (ReAct) pattern implementation for step-by-step problem solving.

#### Memory System
- **Memory Interface**: Abstract memory service with CRUD operations.
- **In-Memory Storage**: Session-based memory with topic tagging and search capabilities.
- **Memory Tools**: Built-in tools for memory operations (add, update, delete, search, load).
- **Memory Instructions**: Automatic instruction generation for memory-enabled agents.

#### Knowledge Management
- **Knowledge Interface**: Document processing and retrieval system.
- **Vector Store Support**: Integration with vector databases for semantic search.
- **Document Processing**: Support for PDF, DOCX, and text document ingestion.
- **Chunking & Embedding**: Document chunking strategies and embedding generation.
- **Retrieval System**: RAG (Retrieval-Augmented Generation) capabilities with reranking.

#### Code Execution
- **CodeExecutor Interface**: Safe code execution in controlled environments.
- **Local Execution**: Local code execution with sandboxing.
- **Container Execution**: Docker-based code execution for isolation.

#### Session Management
- **Session Interface**: User session management with state persistence.
- **In-Memory Sessions**: Fast in-memory session storage.
- **Redis Sessions**: Distributed session storage with Redis backend.
- **State Management**: Session state tracking and persistence.

#### Telemetry & Observability
- **OpenTelemetry Integration**: Comprehensive tracing across all framework layers.
- **Metrics Collection**: Performance metrics and monitoring capabilities.
- **Event Streaming**: Real-time event streaming for debugging and monitoring.
- **Debug Server**: HTTP server for real-time agent interaction and debugging.

#### Examples & Documentation
- **Tool Usage Examples**: Complete examples demonstrating tool creation and usage.
- **Multi-Agent Examples**: Chain, parallel, and cycle agent compositions.
- **Graph Workflow Examples**: Complex workflow orchestration demonstrations.
- **Telemetry Examples**: OpenTelemetry setup and usage examples.
- **MCP Tool Examples**: Model Context Protocol integration examples.
- **Debug Web Demo**: Interactive web interface for agent testing and debugging.
- **Memory Examples**: Memory system usage and integration examples.
- **Code Execution Examples**: Safe code execution demonstrations.

#### Developer Experience
- **Comprehensive Testing**: Extensive test coverage across all packages.
- **Go Modules**: Modern Go module system with dependency management.
- **Linting & Code Quality**: golangci-lint configuration and code quality tools.
- **Documentation**: Detailed README, contributing guidelines, and code documentation.
- **Error Handling**: Structured error types and comprehensive error handling.
- **Context Support**: Full context.Context support for cancellation and timeouts.

### Technical Features
- **Streaming Support**: Both input and output streaming for real-time interactions.
- **JSON Schema Validation**: Automatic validation of tool arguments and responses.
- **Concurrent Execution**: Thread-safe agent execution with proper synchronization.
- **Resource Management**: Proper cleanup and resource management across all components.
- **Extensible Architecture**: Plugin-based architecture for easy extension and customization.
- **Cross-Platform Support**: Works on Linux, macOS, and Windows.
- **Go 1.24.1+ Support**: Modern Go features and optimizations.

### Dependencies
- **OpenAI Go SDK**: OpenAI API integration.
- **Google GenAI**: Google's Generative AI integration.
- **OpenTelemetry**: Observability and tracing.
- **Docker SDK**: Container-based code execution.
- **Redis**: Distributed session storage.
- **PDF Processing**: Document processing libraries.
- **DOCX Processing**: Microsoft Word document support.
- **Vector Store Libraries**: Vector database integrations.

---

This is the initial release of tRPC-Agent-Go, providing a comprehensive framework for building intelligent agent systems with large language models, hierarchical planning, memory management, and a rich tool ecosystem. 
