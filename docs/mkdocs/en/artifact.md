# Artifacts

Artifacts in trpc-agent-go are named, versioned binary data objects that can be linked to user sessions or persistently associated with users across sessions. The artifact system consists of two main components:

1. **Artifacts**: The data objects themselves - containing binary content, metadata, and versioning information
2. **Artifact Service**: The storage and management service that handles saving, retrieving, and organizing artifacts

This system enables agents to store, retrieve, and manage various types of content including images, documents, text files, and other binary data.

## What are Artifacts?

Artifacts are data containers that hold:

- Binary content (images, documents, files, etc.)
- Metadata (MIME type, name, URL)
- Version information
- Association with users and sessions

## What is the Artifact Service?

The Artifact Service is the backend system that:

- Stores and retrieves artifacts
- Manages versioning
- Handles namespace organization (session vs user scope)
- Provides different storage backends (in-memory, cloud storage)

## System Overview

The artifact system provides:

- **Versioned Storage**: Each artifact is automatically versioned, allowing you to track changes over time
- **Session-based Organization**: Artifacts can be scoped to specific user sessions
- **User-persistent Storage**: Artifacts can be stored persistently for users across sessions using the `user:` namespace
- **Multiple Storage Backends**: Support for in-memory storage (development) and cloud storage (production)
- **MIME Type Support**: Proper content type handling for different file formats

## Core Components

### Artifact Data Structure

An Artifact is the fundamental data object that contains your content:

```go
type Artifact struct {
    // Data contains the raw bytes (required)
    Data []byte `json:"data,omitempty"`
    // MimeType is the IANA standard MIME type (required)
    MimeType string `json:"mime_type,omitempty"`
    // URL is the optional URL where the artifact can be accessed
    URL string `json:"url,omitempty"`
    // Name is an optional display name of the artifact
    Name string `json:"name,omitempty"`
}
```

### Session Information

```go
type SessionInfo struct {
    // AppName is the name of the application
    AppName string
    // UserID is the ID of the user
    UserID string
    // SessionID is the ID of the session
    SessionID string
}
```

## Artifact Service Backends

The Artifact Service provides different storage implementations for managing artifacts:

### In-Memory Storage

Perfect for development and testing:

```go
import "trpc.group/trpc-go/trpc-agent-go/artifact/inmemory"

service := inmemory.NewService()
```

### Tencent Cloud Object Storage (COS)

For production deployments with Tencent Cloud:

```go
import "trpc.group/trpc-go/trpc-agent-go/artifact/cos"

// Set environment variables
// export COS_SECRETID="your-secret-id"
// export COS_SECRETKEY="your-secret-key"

service := cos.NewService("https://bucket.cos.region.myqcloud.com")
```

### S3-Compatible Storage

The S3 backend supports AWS S3 and S3-compatible services (MinIO, DigitalOcean Spaces, Cloudflare R2, etc.).

```go
import "trpc.group/trpc-go/trpc-agent-go/artifact/s3"
```

#### Usage

```go
// Create the service
service, err := s3.NewService(os.Getenv("S3_BUCKET"))
if err != nil {
    log.Fatal(err)
}

// With custom endpoint (for S3-compatible services)
service, err := s3.NewService(os.Getenv("S3_BUCKET"),
    s3.WithEndpoint(os.Getenv("S3_ENDPOINT")),
    s3.WithCredentials(os.Getenv("S3_ACCESS_KEY"), os.Getenv("S3_SECRET_KEY")),
    s3.WithPathStyle(),  // Required for MinIO and some S3-compatible services
)
```

> **Note**: The bucket must already exist before using the service.

#### Configuration Options

| Option | Description | Default |
|--------|-------------|---------|
| `WithEndpoint(url)` | Custom endpoint for S3-compatible services (use `http://` for non-SSL) | AWS S3 |
| `WithRegion(region)` | AWS region | `AWS_REGION` env or `us-east-1` |
| `WithCredentials(key, secret)` | Static credentials | AWS credential chain |
| `WithSessionToken(token)` | STS session token for temporary credentials | - |
| `WithPathStyle()` | Use path-style URLs (required for MinIO, R2) | Virtual-hosted |
| `WithRetries(n)` | Max retry attempts | 3 |

#### Credential Resolution Order

When no explicit credentials are provided via `WithCredentials()`, the AWS SDK resolves credentials in the following order of precedence:

1. **Environment variables**: `AWS_ACCESS_KEY_ID` and `AWS_SECRET_ACCESS_KEY`
2. **Shared credentials file**: `~/.aws/credentials` (with optional `AWS_PROFILE`)
3. **IAM role**: EC2 instance profile, ECS task role, Lambda execution role, etc.

## Usage in Agents

### Setup Artifact Service with Runner

```go
import (
    "trpc.group/trpc-go/trpc-agent-go/artifact/inmemory"
    "trpc.group/trpc-go/trpc-agent-go/runner"
)

// Create artifact service
artifactService := inmemory.NewService()

// Create runner with artifact service
r := runner.NewRunner(
    "my-app",
    myAgent,
    runner.WithArtifactService(artifactService),
)
```

### Creating and Managing Artifacts in Tools

Tools can create artifacts and use the Artifact Service through the tool context:

```go
func myTool(ctx context.Context, input MyInput) (MyOutput, error) {
    // Get tool context
    toolCtx, err := agent.NewToolContext(ctx)
    if err != nil {
        return MyOutput{}, err
    }

    // Create an artifact
    artifact := &artifact.Artifact{
        Data:     []byte("Hello, World!"),
        MimeType: "text/plain",
        Name:     "greeting.txt",
    }

    // Save the artifact
    version, err := toolCtx.SaveArtifact("greeting.txt", artifact)
    if err != nil {
        return MyOutput{}, err
    }

    // Load the artifact later
    loadedArtifact, err := toolCtx.LoadArtifact("greeting.txt", nil) // nil for latest version
    if err != nil {
        return MyOutput{}, err
    }

    return MyOutput{}, nil
}
```

## Namespace and Versioning

### Session-scoped Artifacts

By default, artifacts are scoped to the current session:

```go
// This file is only accessible within the current session
version, err := toolCtx.SaveArtifact("session-file.txt", artifact)
```

### User-persistent Artifacts

Use the `user:` prefix to create artifacts that persist across sessions:

```go
// This file persists across all sessions for the user
version, err := toolCtx.SaveArtifact("user:profile.json", artifact)
```

### Version Management

Each save operation creates a new version:

```go
// Save version 0
v0, _ := toolCtx.SaveArtifact("document.txt", artifact1)

// Save version 1
v1, _ := toolCtx.SaveArtifact("document.txt", artifact2)

// Load specific version
oldVersion := 0
artifact, _ := toolCtx.LoadArtifact("document.txt", &oldVersion)

// Load latest version
artifact, _ := toolCtx.LoadArtifact("document.txt", nil)
```

## Artifact Service Interface

The Artifact Service provides the following operations for managing artifacts:

```go
type Service interface {
    // Save an artifact and return the version ID
    SaveArtifact(ctx context.Context, sessionInfo SessionInfo, filename string, artifact *Artifact) (int, error)
    
    // Load an artifact (latest version if version is nil)
    LoadArtifact(ctx context.Context, sessionInfo SessionInfo, filename string, version *int) (*Artifact, error)
    
    // List all artifact filenames in a session
    ListArtifactKeys(ctx context.Context, sessionInfo SessionInfo) ([]string, error)
    
    // Delete an artifact (all versions)
    DeleteArtifact(ctx context.Context, sessionInfo SessionInfo, filename string) error
    
    // List all versions of an artifact
    ListVersions(ctx context.Context, sessionInfo SessionInfo, filename string) ([]int, error)
}
```

## Examples

### Image Generation and Storage

```go
// Tool to generate and save images
func generateImageTool(ctx context.Context, input GenerateImageInput) (GenerateImageOutput, error) {
    // Generate image (implementation details omitted)
    imageData := generateImage(input.Prompt)
    
    // Create artifact
    artifact := &artifact.Artifact{
        Data:     imageData,
        MimeType: "image/png",
        Name:     "generated-image.png",
    }
    
    // Save to artifacts
    toolCtx, _ := agent.NewToolContext(ctx)
    version, err := toolCtx.SaveArtifact("generated-image.png", artifact)
    
    return GenerateImageOutput{
        ImagePath: "generated-image.png",
        Version:   version,
    }, err
}
```

### Text Processing and Storage

```go
// Tool to process and save text
func processTextTool(ctx context.Context, input ProcessTextInput) (ProcessTextOutput, error) {
    // Process the text
    processedText := strings.ToUpper(input.Text)
    
    // Create artifact
    artifact := &artifact.Artifact{
        Data:     []byte(processedText),
        MimeType: "text/plain",
        Name:     "processed-text.txt",
    }
    
    // Save to user namespace for persistence
    toolCtx, _ := agent.NewToolContext(ctx)
    version, err := toolCtx.SaveArtifact("user:processed-text.txt", artifact)
    
    return ProcessTextOutput{
        ProcessedText: processedText,
        Version:       version,
    }, err
}
```

## Best Practices

1. **Use Appropriate Namespaces**: Use session-scoped artifacts for temporary data and user-persistent artifacts for data that should survive across sessions.

2. **Set Proper MIME Types**: Always specify the correct MIME type for your artifacts to ensure proper handling.

3. **Handle Versions**: Consider whether you need to track versions and use the versioning system appropriately.

4. **Choose the Right Storage Backend**: Use in-memory storage for development and cloud storage for production.

5. **Error Handling**: Always handle errors when saving and loading artifacts, as storage operations can fail.

6. **Resource Management**: Be mindful of storage costs and data lifecycle when using cloud storage backends.

## Configuration

### Environment Variables for S3

When using AWS S3 or S3-compatible services:

```bash
export AWS_ACCESS_KEY_ID="your-access-key"
export AWS_SECRET_ACCESS_KEY="your-secret-key"
export AWS_REGION="your-region"
export S3_BUCKET="your-bucket-name"

# Optional: for S3-compatible services (MinIO, R2, Spaces, etc.)
export S3_ENDPOINT="https://your-endpoint.com"
```

### Environment Variables for COS

When using Tencent Cloud Object Storage:

```bash
export COS_SECRETID="your-secret-id"
export COS_SECRETKEY="your-secret-key"
```

### Storage Path Structure

The artifact system organizes files using the following path structure:

- Session-scoped: `{app_name}/{user_id}/{session_id}/{filename}/{version}`
- User-persistent: `{app_name}/{user_id}/user/{filename}/{version}`

This structure ensures proper isolation between applications, users, and sessions while maintaining version history.