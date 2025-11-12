# Dify Agent 实际使用场景指南

本文档提供了 `difyagent` 在实际业务场景中的使用指南和最佳实践。

## 🎯 常见使用场景

### 1. 智能客服系统

**场景描述**: 构建一个智能客服系统，能够理解用户问题并提供准确回答。

**实现要点**:
- 使用流式响应提供实时体验
- 传递用户上下文和历史记录
- 处理多轮对话

```go
// 客服系统配置
func createCustomerServiceAgent() (*difyagent.DifyAgent, error) {
    return difyagent.New(
        difyagent.WithName("customer-service-bot"),
        difyagent.WithDescription("智能客服助手"),
        difyagent.WithEnableStreaming(true),
        difyagent.WithTransferStateKey(
            "customer_level",    // 客户等级
            "order_history",     // 订单历史
            "current_issue",     // 当前问题类型
        ),
        difyagent.WithStreamingRespHandler(func(resp *model.Response) (string, error) {
            // 实时显示客服回复
            content := resp.Choices[0].Delta.Content
            displayToCustomer(content)
            return content, nil
        }),
    )
}

// 处理客户咨询
func handleCustomerInquiry(userID, sessionID, message string, customerInfo map[string]any) {
    events, err := runner.Run(
        ctx, userID, sessionID,
        model.NewUserMessage(message),
        agent.WithRuntimeState(customerInfo),
    )
    
    // 处理响应...
}
```

### 2. 内容创作助手

**场景描述**: 帮助用户生成各种类型的内容，如文章、邮件、报告等。

**实现要点**:
- 根据内容类型定制请求格式
- 支持多种输出格式
- 提供创作建议和优化

```go
// 内容创作转换器
type ContentCreationConverter struct{}

func (c *ContentCreationConverter) ConvertToDifyRequest(
    ctx context.Context,
    invocation *agent.Invocation,
    isStream bool,
) (*dify.ChatMessageRequest, error) {
    req := &dify.ChatMessageRequest{
        Query:  invocation.Message.Content,
        Inputs: make(map[string]interface{}),
    }
    
    // 从状态中提取内容创作参数
    if contentType, ok := invocation.RunOptions.RuntimeState["content_type"]; ok {
        req.Inputs["content_type"] = contentType
    }
    if tone, ok := invocation.RunOptions.RuntimeState["writing_tone"]; ok {
        req.Inputs["tone"] = tone
    }
    if length, ok := invocation.RunOptions.RuntimeState["target_length"]; ok {
        req.Inputs["length"] = length
    }
    if audience, ok := invocation.RunOptions.RuntimeState["target_audience"]; ok {
        req.Inputs["audience"] = audience
    }
    
    return req, nil
}

// 使用示例
func generateContent(contentRequest ContentRequest) {
    state := map[string]any{
        "content_type":    contentRequest.Type,     // "article", "email", "report"
        "writing_tone":    contentRequest.Tone,     // "professional", "casual", "formal"
        "target_length":   contentRequest.Length,   // "short", "medium", "long"
        "target_audience": contentRequest.Audience, // "general", "technical", "executive"
    }
    
    events, err := runner.Run(
        ctx, userID, sessionID,
        model.NewUserMessage(contentRequest.Prompt),
        agent.WithRuntimeState(state),
    )
}
```

### 3. 教育培训系统

**场景描述**: 构建个性化的教育培训系统，根据学员水平提供适合的内容。

**实现要点**:
- 跟踪学习进度
- 个性化内容难度
- 提供学习建议

```go
// 教育系统事件转换器
type EducationEventConverter struct{}

func (e *EducationEventConverter) ConvertToEvent(
    resp *dify.ChatMessageResponse,
    agentName string,
    invocation *agent.Invocation,
) *event.Event {
    // 解析教育相关的响应内容
    content := resp.Answer
    
    // 提取学习要点和建议
    learningPoints := extractLearningPoints(content)
    suggestions := extractSuggestions(content)
    
    evt := event.New(invocation.InvocationID, agentName)
    evt.Response = &model.Response{
        Choices: []model.Choice{{
            Message: model.Message{
                Role:    model.RoleAssistant,
                Content: content,
            },
        }},
        Done: true,
    }
    
    // 添加教育相关的元数据
    evt.Metadata = map[string]any{
        "learning_points": learningPoints,
        "suggestions":     suggestions,
        "difficulty_level": extractDifficultyLevel(content),
    }
    
    return evt
}

// 学习会话管理
func conductLearningSession(studentID, subject, currentLevel string) {
    sessionState := map[string]any{
        "student_level":    currentLevel,
        "subject":          subject,
        "learning_style":   getStudentPreference(studentID),
        "previous_topics":  getCompletedTopics(studentID),
    }
    
    // 开始学习会话
    events, err := runner.Run(
        ctx, studentID, generateSessionID(),
        model.NewUserMessage("开始今天的学习"),
        agent.WithRuntimeState(sessionState),
    )
}
```

### 4. 代码助手系统

**场景描述**: 帮助开发者进行代码审查、生成代码、解释技术概念。

**实现要点**:
- 支持多种编程语言
- 代码格式化和高亮
- 提供最佳实践建议

```go
// 代码助手配置
func createCodeAssistant() (*difyagent.DifyAgent, error) {
    return difyagent.New(
        difyagent.WithName("code-assistant"),
        difyagent.WithCustomRequestConverter(&CodeRequestConverter{}),
        difyagent.WithCustomEventConverter(&CodeEventConverter{}),
        difyagent.WithTransferStateKey(
            "programming_language",
            "project_context",
            "code_style_preference",
        ),
    )
}

// 代码请求转换器
type CodeRequestConverter struct{}

func (c *CodeRequestConverter) ConvertToDifyRequest(
    ctx context.Context,
    invocation *agent.Invocation,
    isStream bool,
) (*dify.ChatMessageRequest, error) {
    req := &dify.ChatMessageRequest{
        Query:  invocation.Message.Content,
        Inputs: make(map[string]interface{}),
    }
    
    // 处理代码内容部分
    for _, part := range invocation.Message.ContentParts {
        if part.Type == model.ContentTypeFile && strings.HasSuffix(part.File.Name, ".go") {
            req.Inputs["source_code"] = part.File.Content
            req.Inputs["file_type"] = "golang"
        }
    }
    
    return req, nil
}

// 使用示例
func reviewCode(codeContent, language string) {
    message := model.Message{
        Role:    model.RoleUser,
        Content: "请审查这段代码并提供改进建议",
        ContentParts: []model.ContentPart{
            {
                Type: model.ContentTypeFile,
                File: &model.FileContent{
                    Name:    "code.go",
                    Content: codeContent,
                },
            },
        },
    }
    
    state := map[string]any{
        "programming_language":   language,
        "review_focus":          "performance,security,readability",
        "code_style_preference": "google_style_guide",
    }
    
    events, err := runner.Run(
        ctx, userID, sessionID, message,
        agent.WithRuntimeState(state),
    )
}
```

### 5. 多语言翻译系统

**场景描述**: 提供高质量的多语言翻译服务，支持上下文感知翻译。

**实现要点**:
- 保持翻译一致性
- 处理专业术语
- 支持批量翻译

```go
// 翻译系统配置
func createTranslationAgent() (*difyagent.DifyAgent, error) {
    return difyagent.New(
        difyagent.WithName("translation-assistant"),
        difyagent.WithCustomRequestConverter(&TranslationConverter{}),
        difyagent.WithTransferStateKey(
            "source_language",
            "target_language",
            "domain_context",
            "translation_style",
        ),
    )
}

// 翻译请求转换器
type TranslationConverter struct{}

func (t *TranslationConverter) ConvertToDifyRequest(
    ctx context.Context,
    invocation *agent.Invocation,
    isStream bool,
) (*dify.ChatMessageRequest, error) {
    req := &dify.ChatMessageRequest{
        Inputs: make(map[string]interface{}),
    }
    
    // 构建翻译请求
    sourceLang := invocation.RunOptions.RuntimeState["source_language"]
    targetLang := invocation.RunOptions.RuntimeState["target_language"]
    
    req.Query = fmt.Sprintf("请将以下%s文本翻译成%s：\n%s", 
        sourceLang, targetLang, invocation.Message.Content)
    
    // 添加领域上下文
    if domain, ok := invocation.RunOptions.RuntimeState["domain_context"]; ok {
        req.Inputs["domain"] = domain
    }
    
    return req, nil
}

// 批量翻译
func batchTranslate(texts []string, sourceLang, targetLang, domain string) {
    for i, text := range texts {
        state := map[string]any{
            "source_language":   sourceLang,
            "target_language":   targetLang,
            "domain_context":    domain,
            "batch_index":       i,
            "total_count":       len(texts),
        }
        
        events, err := runner.Run(
            ctx, userID, fmt.Sprintf("translation-batch-%d", i),
            model.NewUserMessage(text),
            agent.WithRuntimeState(state),
        )
        
        // 处理翻译结果...
    }
}
```

## 🔧 高级配置模式

### 1. 动态配置切换

根据不同场景动态切换 Dify 工作流：

```go
type DynamicDifyAgent struct {
    agents map[string]*difyagent.DifyAgent
}

func (d *DynamicDifyAgent) GetAgent(scenario string) *difyagent.DifyAgent {
    return d.agents[scenario]
}

// 初始化多个代理
func initializeDynamicAgent() *DynamicDifyAgent {
    return &DynamicDifyAgent{
        agents: map[string]*difyagent.DifyAgent{
            "customer_service": createCustomerServiceAgent(),
            "content_creation": createContentCreationAgent(),
            "code_review":      createCodeReviewAgent(),
        },
    }
}
```

### 2. 负载均衡和故障转移

```go
type LoadBalancedDifyAgent struct {
    agents []*difyagent.DifyAgent
    current int
}

func (l *LoadBalancedDifyAgent) Run(ctx context.Context, invocation *agent.Invocation) (<-chan *event.Event, error) {
    // 轮询选择代理
    agent := l.agents[l.current%len(l.agents)]
    l.current++
    
    // 尝试执行，失败则切换到下一个
    events, err := agent.Run(ctx, invocation)
    if err != nil && l.current < len(l.agents) {
        return l.Run(ctx, invocation) // 重试下一个代理
    }
    
    return events, err
}
```

### 3. 缓存和性能优化

```go
type CachedDifyAgent struct {
    agent *difyagent.DifyAgent
    cache map[string]*model.Response
    mutex sync.RWMutex
}

func (c *CachedDifyAgent) Run(ctx context.Context, invocation *agent.Invocation) (<-chan *event.Event, error) {
    // 检查缓存
    cacheKey := generateCacheKey(invocation)
    
    c.mutex.RLock()
    if cached, exists := c.cache[cacheKey]; exists {
        c.mutex.RUnlock()
        return c.createCachedEventChannel(cached), nil
    }
    c.mutex.RUnlock()
    
    // 执行并缓存结果
    events, err := c.agent.Run(ctx, invocation)
    if err != nil {
        return nil, err
    }
    
    // 包装事件通道以进行缓存
    return c.wrapEventsForCaching(events, cacheKey), nil
}
```

## 📊 监控和指标

### 1. 性能监控

```go
type MetricsDifyAgent struct {
    agent *difyagent.DifyAgent
    metrics *Metrics
}

func (m *MetricsDifyAgent) Run(ctx context.Context, invocation *agent.Invocation) (<-chan *event.Event, error) {
    start := time.Now()
    
    events, err := m.agent.Run(ctx, invocation)
    if err != nil {
        m.metrics.RecordError(err)
        return nil, err
    }
    
    // 包装事件通道以收集指标
    return m.wrapEventsForMetrics(events, start), nil
}

type Metrics struct {
    RequestCount    int64
    ErrorCount      int64
    AverageLatency  time.Duration
    TokenUsage      int64
}
```

### 2. 日志记录

```go
func createLoggedDifyAgent(logger *log.Logger) *difyagent.DifyAgent {
    return difyagent.New(
        difyagent.WithCustomEventConverter(&LoggingEventConverter{logger: logger}),
        difyagent.WithCustomRequestConverter(&LoggingRequestConverter{logger: logger}),
    )
}

type LoggingEventConverter struct {
    defaultDifyEventConverter
    logger *log.Logger
}

func (l *LoggingEventConverter) ConvertToEvent(
    resp *dify.ChatMessageResponse,
    agentName string,
    invocation *agent.Invocation,
) *event.Event {
    l.logger.Printf("Dify Response: ConversationID=%s, MessageID=%s, Length=%d",
        resp.ConversationID, resp.MessageID, len(resp.Answer))
    
    return l.defaultDifyEventConverter.ConvertToEvent(resp, agentName, invocation)
}
```

## 🚀 部署和扩展

### 1. 微服务架构

```go
// Dify 代理服务
type DifyAgentService struct {
    agents map[string]*difyagent.DifyAgent
}

func (s *DifyAgentService) ProcessRequest(req *ProcessRequest) (*ProcessResponse, error) {
    agent := s.agents[req.AgentType]
    if agent == nil {
        return nil, fmt.Errorf("unknown agent type: %s", req.AgentType)
    }
    
    // 处理请求...
    return response, nil
}

// HTTP 服务器
func startDifyAgentServer() {
    service := &DifyAgentService{
        agents: initializeAgents(),
    }
    
    http.HandleFunc("/process", service.handleHTTPRequest)
    log.Fatal(http.ListenAndServe(":8080", nil))
}
```

### 2. 配置管理

```go
type DifyConfig struct {
    BaseURL    string            `yaml:"base_url"`
    APISecret  string            `yaml:"api_secret"`
    Agents     map[string]AgentConfig `yaml:"agents"`
}

type AgentConfig struct {
    Name        string   `yaml:"name"`
    Description string   `yaml:"description"`
    Streaming   bool     `yaml:"streaming"`
    StateKeys   []string `yaml:"state_keys"`
}

func loadConfig(path string) (*DifyConfig, error) {
    data, err := ioutil.ReadFile(path)
    if err != nil {
        return nil, err
    }
    
    var config DifyConfig
    err = yaml.Unmarshal(data, &config)
    return &config, err
}
```

## 🔍 故障排查

### 常见问题和解决方案

1. **连接超时**
   ```go
   // 设置合适的超时时间
   difyagent.WithGetDifyClientFunc(func(invocation *agent.Invocation) (*dify.Client, error) {
       return dify.NewClientWithConfig(&dify.ClientConfig{
           Timeout: 60 * time.Second, // 增加超时时间
       }), nil
   })
   ```

2. **内存占用过高**
   ```go
   // 调整流式缓冲区大小
   difyagent.WithStreamingChannelBufSize(512) // 减少缓冲区大小
   ```

3. **响应质量问题**
   ```go
   // 优化上下文传递
   state := map[string]any{
       "conversation_history": getRecentHistory(sessionID, 5), // 限制历史记录
       "user_context":        getUserContext(userID),
   }
   ```

## 📈 最佳实践总结

1. **性能优化**
   - 合理设置超时时间
   - 使用连接池
   - 实施请求缓存

2. **错误处理**
   - 实施重试机制
   - 记录详细日志
   - 优雅降级

3. **安全考虑**
   - 保护 API 密钥
   - 验证用户输入
   - 限制请求频率

4. **监控运维**
   - 收集关键指标
   - 设置告警规则
   - 定期性能评估

通过这些实际使用场景和最佳实践，您可以更好地在生产环境中使用 Dify Agent，构建稳定、高效的 AI 应用系统。