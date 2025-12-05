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
- **用户持久化存储**：制品可以使用 `user:` 命名空间为用户跨会话持久化存储
- **多种存储后端**：支持内存存储（开发环境）和云存储（生产环境）
- **MIME 类型支持**：为不同文件格式提供适当的内容类型处理

## 核心组件

### Artifact 数据结构

Artifact（制品）是包含您内容的基本数据对象：

```go
type Artifact struct {
    // Data 包含原始字节数据（必需）
    Data []byte `json:"data,omitempty"`
    // MimeType 是 IANA 标准 MIME 类型（必需）
    MimeType string `json:"mime_type,omitempty"`
    // URL 是可访问制品的可选 URL
    URL string `json:"url,omitempty"`
    // Name 是制品的可选显示名称
    Name string `json:"name,omitempty"`
}
```

### 会话信息

```go
type SessionInfo struct {
    // AppName 是应用程序名称
    AppName string
    // UserID 是用户 ID
    UserID string
    // SessionID 是会话 ID
    SessionID string
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

service := cos.NewService("https://bucket.cos.region.myqcloud.com")
```

### S3 兼容存储

S3 后端支持 AWS S3 和 S3 兼容服务（MinIO、DigitalOcean Spaces、Cloudflare R2 等）。

```go
import "trpc.group/trpc-go/trpc-agent-go/artifact/s3"
```

#### 使用方法

```go
// 创建服务
service, err := s3.NewService(os.Getenv("S3_BUCKET"))
if err != nil {
    log.Fatal(err)
}

// 使用自定义端点（用于 S3 兼容服务）
service, err := s3.NewService(os.Getenv("S3_BUCKET"),
    s3.WithEndpoint(os.Getenv("S3_ENDPOINT")),
    s3.WithCredentials(os.Getenv("S3_ACCESS_KEY"), os.Getenv("S3_SECRET_KEY")),
    s3.WithPathStyle(),  // MinIO 和某些 S3 兼容服务需要
)
```

> **注意**：在使用服务之前，存储桶必须已存在。

#### 配置选项

| 选项 | 描述 | 默认值 |
|------|------|--------|
| `WithEndpoint(url)` | S3 兼容服务的自定义端点（使用 `http://` 禁用 SSL） | AWS S3 |
| `WithRegion(region)` | AWS 区域 | `AWS_REGION` 环境变量或 `us-east-1` |
| `WithCredentials(key, secret)` | 静态凭证 | AWS 凭证链 |
| `WithSessionToken(token)` | 临时凭证的 STS 会话令牌 | - |
| `WithPathStyle()` | 使用路径样式 URL（MinIO、R2 需要） | 虚拟主机样式 |
| `WithRetries(n)` | 最大重试次数 | 3 |

#### 凭证解析顺序

当未通过 `WithCredentials()` 提供显式凭证时，AWS SDK 按以下优先级顺序解析凭证：

1. **环境变量**：`AWS_ACCESS_KEY_ID` 和 `AWS_SECRET_ACCESS_KEY`
2. **共享凭证文件**：`~/.aws/credentials`（可选配合 `AWS_PROFILE`）
3. **IAM 角色**：EC2 实例配置文件、ECS 任务角色、Lambda 执行角色等

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

    // 创建制品
    artifact := &artifact.Artifact{
        Data:     []byte("你好，世界！"),
        MimeType: "text/plain",
        Name:     "greeting.txt",
    }

    // 保存制品
    version, err := toolCtx.SaveArtifact("greeting.txt", artifact)
    if err != nil {
        return MyOutput{}, err
    }

    // 稍后加载制品
    loadedArtifact, err := toolCtx.LoadArtifact("greeting.txt", nil) // nil 表示最新版本
    if err != nil {
        return MyOutput{}, err
    }

    return MyOutput{}, nil
}
```

## 命名空间和版本管理

### 会话范围的制品

默认情况下，制品限定在当前会话范围内：

```go
// 此文件只能在当前会话中访问
version, err := toolCtx.SaveArtifact("session-file.txt", artifact)
```

### 用户持久化制品

使用 `user:` 前缀创建跨会话持久化的制品：

```go
// 此文件在用户的所有会话中持久化
version, err := toolCtx.SaveArtifact("user:profile.json", artifact)
```

### 版本管理

每次保存操作都会创建新版本：

```go
// 保存版本 0
v0, _ := toolCtx.SaveArtifact("document.txt", artifact1)

// 保存版本 1
v1, _ := toolCtx.SaveArtifact("document.txt", artifact2)

// 加载特定版本
oldVersion := 0
artifact, _ := toolCtx.LoadArtifact("document.txt", &oldVersion)

// 加载最新版本
artifact, _ := toolCtx.LoadArtifact("document.txt", nil)
```

## Artifact Service 接口

Artifact Service 提供以下操作来管理制品：

```go
type Service interface {
    // 保存制品并返回版本 ID
    SaveArtifact(ctx context.Context, sessionInfo SessionInfo, filename string, artifact *Artifact) (int, error)
    
    // 加载制品（如果 version 为 nil 则加载最新版本）
    LoadArtifact(ctx context.Context, sessionInfo SessionInfo, filename string, version *int) (*Artifact, error)
    
    // 列出会话中的所有制品文件名
    ListArtifactKeys(ctx context.Context, sessionInfo SessionInfo) ([]string, error)
    
    // 删除制品（所有版本）
    DeleteArtifact(ctx context.Context, sessionInfo SessionInfo, filename string) error
    
    // 列出制品的所有版本
    ListVersions(ctx context.Context, sessionInfo SessionInfo, filename string) ([]int, error)
}
```

## 示例

### 图像生成和存储

```go
// 生成并保存图像的工具
func generateImageTool(ctx context.Context, input GenerateImageInput) (GenerateImageOutput, error) {
    // 生成图像（实现细节省略）
    imageData := generateImage(input.Prompt)
    
    // 创建制品
    artifact := &artifact.Artifact{
        Data:     imageData,
        MimeType: "image/png",
        Name:     "generated-image.png",
    }
    
    // 保存到制品存储
    toolCtx, _ := agent.NewToolContext(ctx)
    version, err := toolCtx.SaveArtifact("generated-image.png", artifact)
    
    return GenerateImageOutput{
        ImagePath: "generated-image.png",
        Version:   version,
    }, err
}
```

### 文本处理和存储

```go
// 处理并保存文本的工具
func processTextTool(ctx context.Context, input ProcessTextInput) (ProcessTextOutput, error) {
    // 处理文本
    processedText := strings.ToUpper(input.Text)
    
    // 创建制品
    artifact := &artifact.Artifact{
        Data:     []byte(processedText),
        MimeType: "text/plain",
        Name:     "processed-text.txt",
    }
    
    // 保存到用户命名空间以实现持久化
    toolCtx, _ := agent.NewToolContext(ctx)
    version, err := toolCtx.SaveArtifact("user:processed-text.txt", artifact)
    
    return ProcessTextOutput{
        ProcessedText: processedText,
        Version:       version,
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