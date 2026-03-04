# Artifacts

Artifacts（制品）是 trpc-agent-go 中的命名、版本化二进制数据对象，可以与用户会话关联或跨会话持久化存储。制品系统由两个主要组件组成：

1. **Artifacts（制品）**：数据对象本身 - 包含二进制内容、元数据和版本信息
2. **Artifact Service（制品服务）**：处理保存、检索和组织制品的存储管理服务

该系统使 Agent 能够存储、检索和管理各种类型的内容，包括图像、文档、文本文件和其他二进制数据。

## 什么是 Artifacts（制品）？

Artifacts（制品）是包含以下内容的数据容器：

- 二进制内容（图像、文档、文件等）
- 元数据（MIME 类型、名称、URL）
- 版本信息
- 与用户和会话的关联

## 什么是 Artifact Service（制品服务）？

Artifact Service（制品服务）是后端系统，负责：

- 存储和检索制品
- 管理版本
- 处理命名空间组织（会话范围 vs 用户范围）
- 提供不同的存储后端（内存、云存储）

## 系统概述

制品系统提供：

- **版本化存储**：每个制品都会自动版本化，允许您跟踪随时间的变化
- **基于会话的组织**：制品可以限定在特定用户会话范围内
- **用户持久化存储**：通过在 `artifact.Key` 中使用 `ScopeUser` 来实现跨会话持久化
- **多种存储后端**：支持内存存储（开发环境）和云存储（生产环境）
- **MIME 类型支持**：为不同文件格式提供适当的内容类型处理

## 核心组件

### Artifact 数据结构

Artifact（制品）是包含您内容的基本数据对象：

```go
type Artifact struct {
    // MimeType 是 IANA 标准 MIME 类型（可选，默认 application/octet-stream）
    MimeType string `json:"mime_type,omitempty"`
    // URL 是可访问制品的可选 URL（例如预签名链接；由后端尽力返回）
    URL string `json:"url,omitempty"`
    // Data 包含原始字节数据
    Data []byte `json:"data,omitempty"`
}
```

### Key / Descriptor / Version

```go
type VersionID string

type Key struct {
    AppName   string
    UserID    string
    SessionID string
    Scope     Scope // ScopeSession 或 ScopeUser
    Name      string
}

type Descriptor struct {
    Key      Key
    Version  VersionID
    MimeType string
    Size     int64
    URL      string
}
```

## Artifact Service 存储后端

Artifact Service 提供不同的存储实现来管理制品：

### 内存存储

适用于开发和测试：

```go
import "trpc.group/trpc-go/trpc-agent-go/artifact/inmemory"

service := inmemory.NewService()
```

### 腾讯云对象存储 (COS)

用于腾讯云生产部署：

```go
import "trpc.group/trpc-go/trpc-agent-go/artifact/cos"

// 设置环境变量
// export COS_SECRETID="your-secret-id"
// export COS_SECRETKEY="your-secret-key"

service, err := cos.NewService("my-service", "https://bucket.cos.region.myqcloud.com")
if err != nil {
    panic(err)
}
```

### S3 兼容存储

S3 后端支持 AWS S3 和 S3 兼容服务（MinIO、DigitalOcean Spaces、Cloudflare R2 等）。

```go
import "trpc.group/trpc-go/trpc-agent-go/artifact/s3"
```

#### 使用方法

```go
import (
    "context"
    "log"
    "os"

    "trpc.group/trpc-go/trpc-agent-go/artifact/s3"
)

ctx := context.Background()

// 创建服务（S3 客户端会自动在内部创建）
service, err := s3.NewService(ctx, os.Getenv("S3_BUCKET"))
if err != nil {
    log.Fatal(err)
}
defer service.Close()

// 使用自定义端点（用于 S3 兼容服务）
service, err := s3.NewService(ctx, os.Getenv("S3_BUCKET"),
    s3.WithEndpoint(os.Getenv("S3_ENDPOINT")),
    s3.WithCredentials(os.Getenv("S3_ACCESS_KEY"), os.Getenv("S3_SECRET_KEY")),
    s3.WithPathStyle(true),  // MinIO 和某些 S3 兼容服务需要
)

// 启用调试日志
import "trpc.group/trpc-go/trpc-agent-go/log"

service, err := s3.NewService(ctx, os.Getenv("S3_BUCKET"),
    s3.WithLogger(log.Default),
)
```

> **注意**：在使用服务之前，存储桶必须已存在。

#### 配置选项

| 选项 | 描述 | 默认值 |
|------|------|--------|
| `WithEndpoint(url)` | S3 兼容服务的自定义端点（使用 `http://` 禁用 SSL） | AWS S3 |
| `WithRegion(region)` | AWS 区域 | AWS SDK 自动检测 |
| `WithCredentials(key, secret)` | 静态凭证 | AWS 凭证链 |
| `WithSessionToken(token)` | 临时凭证的 STS 会话令牌 | - |
| `WithPathStyle(bool)` | 使用路径样式 URL（MinIO、R2 需要） | 虚拟主机样式 |
| `WithRetries(n)` | 最大重试次数 | 3 |
| `WithClient(client)` | 使用预创建的 S3 客户端（高级用法） | 自动创建 |
| `WithLogger(logger)` | 调试消息日志器（如制品未找到） | `nil`（无日志） |

#### 凭证解析顺序

当未通过 `WithCredentials()` 提供显式凭证时，AWS SDK 按以下优先级顺序解析凭证：

1. **环境变量**：`AWS_ACCESS_KEY_ID` 和 `AWS_SECRET_ACCESS_KEY`
2. **共享凭证文件**：`~/.aws/credentials`（可选配合 `AWS_PROFILE`）
3. **IAM 角色**：EC2 实例配置文件、ECS 任务角色、Lambda 执行角色等

#### 高级用法：客户端管理

对于高级用例，`storage/s3` 包提供了可复用的 S3 客户端，可在多个服务之间共享。适用于以下场景：

- 在多个制品服务之间共享单个客户端
- 将同一客户端复用于其他 S3 操作（如向量存储）
- 需要对客户端生命周期进行精细控制

```go
import (
    "context"
    "log"

    "trpc.group/trpc-go/trpc-agent-go/artifact/s3"
    s3storage "trpc.group/trpc-go/trpc-agent-go/storage/s3"
)

ctx := context.Background()

// 创建可复用的 S3 客户端
client, err := s3storage.NewClient(ctx,
    s3storage.WithBucket("my-bucket"),
    s3storage.WithRegion("us-west-2"),
    s3storage.WithEndpoint("http://localhost:9000"),
    s3storage.WithCredentials("access-key", "secret-key"),
    s3storage.WithPathStyle(true),
)
if err != nil {
    log.Fatal(err)
}
defer client.Close()

// 在多个服务之间共享客户端
artifactService, _ := s3.NewService(ctx, "my-bucket", s3.WithClient(client))
// 该客户端也可用于其他服务，如向量存储
```

> **注意**：使用 `WithClient` 时，制品服务不拥有该客户端的所有权。
> 您必须自行负责在完成后关闭客户端。如果未提供客户端，
> 服务会自动创建并管理一个客户端。

## 在 Agent 中的使用

### 配置 Artifact Service 与 Runner

```go
import (
    "trpc.group/trpc-go/trpc-agent-go/artifact/inmemory"
    "trpc.group/trpc-go/trpc-agent-go/runner"
)

// 创建制品服务
artifactService := inmemory.NewService()

// 创建带制品服务的 runner
r := runner.NewRunner(
    "my-app",
    myAgent,
    runner.WithArtifactService(artifactService),
)
```

### 在工具中创建和管理 Artifacts

工具可以创建制品并通过工具上下文使用 Artifact Service：

```go
func myTool(ctx context.Context, input MyInput) (MyOutput, error) {
    // 获取工具上下文
    toolCtx, err := agent.NewToolContext(ctx)
    if err != nil {
        return MyOutput{}, err
    }

    // 保存制品
    desc, err := toolCtx.PutArtifact(
        "greeting.txt",
        bytes.NewReader([]byte("你好，世界！")),
        artifact.WithPutMimeType("text/plain"),
    )
    if err != nil {
        return MyOutput{}, err
    }

    // 稍后加载制品（读入内存）
    rc, got, err := toolCtx.OpenArtifact("greeting.txt", nil) // nil 表示最新版本
    if err != nil {
        return MyOutput{}, err
    }
    defer rc.Close()
    data, err := io.ReadAll(rc)
    if err != nil {
        return MyOutput{}, err
    }

    _ = desc
    _ = got
    _ = data
    return MyOutput{}, nil
}
```

## 命名空间和版本管理

### 会话范围的制品

默认情况下，制品限定在当前会话范围内：

```go
// 此文件只能在当前会话中访问
desc, err := toolCtx.PutArtifact("session-file.txt", bytes.NewReader([]byte("hello")), artifact.WithPutMimeType("text/plain"))
```

### 用户持久化制品（ScopeUser）

用户级（跨 session）持久化由 `artifact.Key.Scope = artifact.ScopeUser` 显式控制。

- `ToolContext.PutArtifact` 默认写入 **ScopeSession**。
- 如果需要写入/读取 **ScopeUser**，请直接使用底层 `artifact.Service`（`Put/Head/Open/...`）并构造 `artifact.Key{Scope: artifact.ScopeUser, ...}`。

### 版本管理

每次保存操作都会创建新版本：

```go
// 保存版本 0
d0, _ := toolCtx.PutArtifact("document.txt", strings.NewReader("v0"), artifact.WithPutMimeType("text/plain"))

// 保存版本 1
d1, _ := toolCtx.PutArtifact("document.txt", strings.NewReader("v1"), artifact.WithPutMimeType("text/plain"))

// 加载特定版本
v0 := d0.Version
rc, desc, _ := toolCtx.OpenArtifact("document.txt", &v0)
data, _ := io.ReadAll(rc)
_ = rc.Close()

// 加载最新版本
rc, desc, _ = toolCtx.OpenArtifact("document.txt", nil)
data, _ = io.ReadAll(rc)
_ = rc.Close()
```

## Artifact Service 接口

Artifact Service 提供以下操作来管理制品：

```go
type Service interface {
    // 写入一个新版本（不覆盖老版本）
    Put(ctx context.Context, key Key, r io.Reader, opts ...PutOption) (Descriptor, error)
    
    // 只读元信息（不下载内容）；version=nil 表示 latest
    Head(ctx context.Context, key Key, version *VersionID) (Descriptor, error)
    
    // 流式读取内容；version=nil 表示 latest
    Open(ctx context.Context, key Key, version *VersionID) (io.ReadCloser, Descriptor, error)
    
    // 分页列出（每个 name 返回 latest 的 Descriptor）
    // key.Name 会被忽略。
    List(ctx context.Context, key Key, opts ...ListOption) ([]Descriptor, string, error)
    
    // 删除制品版本（全部/最新/指定版本）
    Delete(ctx context.Context, key Key, opts ...DeleteOption) error
    
    // 列出全部版本 ID
    Versions(ctx context.Context, key Key) ([]VersionID, error)
}
```

## 示例

### 图像生成和存储

```go
// 生成并保存图像的工具
func generateImageTool(ctx context.Context, input GenerateImageInput) (GenerateImageOutput, error) {
    // 生成图像（实现细节省略）
    imageData := generateImage(input.Prompt)
    
    // 保存到制品存储
    toolCtx, _ := agent.NewToolContext(ctx)
    desc, err := toolCtx.PutArtifact(
        "generated-image.png",
        bytes.NewReader(imageData),
        artifact.WithPutMimeType("image/png"),
    )
    
    return GenerateImageOutput{
        ImagePath: "generated-image.png",
        Version:   desc.Version,
    }, err
}
```

### 文本处理和存储

```go
// 处理并保存文本的工具
func processTextTool(ctx context.Context, input ProcessTextInput) (ProcessTextOutput, error) {
    // 处理文本
    processedText := strings.ToUpper(input.Text)
    
    // 保存（默认 session scope）。如需 ScopeUser，请直接使用 artifact.Service + Key{Scope: ScopeUser}。
    toolCtx, _ := agent.NewToolContext(ctx)
    desc, err := toolCtx.PutArtifact(
        "processed-text.txt",
        strings.NewReader(processedText),
        artifact.WithPutMimeType("text/plain"),
    )
    
    return ProcessTextOutput{
        ProcessedText: processedText,
        Version:       desc.Version,
    }, err
}
```

## 最佳实践

1. **使用适当的命名空间**：对临时数据使用会话范围的制品，对需要跨会话保存的数据使用用户持久化制品。

2. **设置正确的 MIME 类型**：始终为制品指定正确的 MIME 类型以确保正确处理。

3. **处理版本**：考虑是否需要跟踪版本并适当使用版本管理系统。

4. **选择合适的存储后端**：开发环境使用内存存储，生产环境使用云存储。

5. **错误处理**：保存和加载制品时始终处理错误，因为存储操作可能失败。

6. **资源管理**：使用云存储后端时要注意存储成本和数据生命周期。

## 配置

### S3 的环境变量

使用 AWS S3 或 S3 兼容服务时：

```bash
export AWS_ACCESS_KEY_ID="your-access-key"
export AWS_SECRET_ACCESS_KEY="your-secret-key"
export AWS_REGION="your-region"
export S3_BUCKET="your-bucket-name"

# 可选：用于 S3 兼容服务（MinIO、R2、Spaces 等）
export S3_ENDPOINT="https://your-endpoint.com"
```

### COS 的环境变量

使用腾讯云对象存储时：

```bash
export COS_SECRETID="your-secret-id"
export COS_SECRETKEY="your-secret-key"
```

### 存储路径结构

制品系统使用以下路径结构组织文件：

- 会话范围：`{app_name}/{user_id}/{session_id}/{filename}/{version}`
- 用户持久化：`{app_name}/{user_id}/user/{filename}/{version}`

此结构确保应用程序、用户和会话之间的适当隔离，同时维护版本历史。