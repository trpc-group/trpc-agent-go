# CodeExecutor 与 Workspace

`codeexecutor` 用来给 Agent 提供一个可控的执行环境。

## 能做什么

启用 `codeexecutor` 后，Agent 可以在 workspace 中执行程序，并围绕这个
workspace 读写文件。

常见能力包括：

- 运行 shell 命令或代码
- 在固定工作目录中处理输入文件
- 把用户上传的文件放进执行环境
- 输出结果文件并在后续步骤继续使用
- 在需要时切换本地、容器或 Jupyter 后端

如果你的场景只是让模型生成文本，不需要运行程序，也不需要处理本地文件，
通常不需要配置这一层。

## 快速接入

给 `LLMAgent` 配置执行器即可启用 `codeexecutor`：

```go
package main

import (
    "context"

    "trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
    "trpc.group/trpc-go/trpc-agent-go/codeexecutor/local"
    "trpc.group/trpc-go/trpc-agent-go/model"
    "trpc.group/trpc-go/trpc-agent-go/model/openai"
    "trpc.group/trpc-go/trpc-agent-go/runner"
)

func main() {
    m := openai.New("gpt-4.1-mini")

    agent := llmagent.New(
        "demo",
        llmagent.WithModel(m),
        llmagent.WithInstruction("使用工作区中的文件完成任务。"),
        llmagent.WithCodeExecutor(local.New()),
    )

    r := runner.NewRunner("demo", agent)
    defer r.Close()

    msg := model.NewUserMessage("请读取输入文件并总结要点。")
    events, _ := r.Run(context.Background(), "user-1", "session-1", msg)
    for range events {
    }
}
```

更完整的示例可以参考：

- [examples/codeexecution/main.go](https://github.com/trpc-group/trpc-agent-go/blob/main/examples/codeexecution/main.go)（本地 backend）
- [examples/codeexecution/container/README.md](https://github.com/trpc-group/trpc-agent-go/blob/main/examples/codeexecution/container/README.md)（Docker container backend）
- [examples/codeexecution/jupyter/README.md](https://github.com/trpc-group/trpc-agent-go/blob/main/examples/codeexecution/jupyter/README.md)（Jupyter kernel backend）

### `WithCodeExecutor` 与围栏代码自动执行

`llmagent.WithCodeExecutor(...)` 和响应阶段的围栏代码自动执行是
**两个独立开关**。先分清二者，能避免「只配了 executor，却自动执行了
回复里代码块」这类常见困惑。

- `WithCodeExecutor(...)` 提供的是**运行时**（runtime），给那些依赖
  执行器的工具（最典型的就是 `workspace_exec`）执行命令用。它本身
  不会让框架去扫描模型的最终回复、然后自动跑里面的代码。
- `EnableCodeExecutionResponseProcessor`（默认：`true`，由
  `WithEnableCodeExecutionResponseProcessor(enable bool)` 控制）
  决定框架是否扫描 assistant 回复，如果恰好是一个可执行的围栏代码块
  就自动运行。

回复里的代码块真的被自动执行，必须**两个条件同时满足**：有可用的
executor**且**响应处理器开着。

如果你只想让 executor 服务于 `workspace_exec` 或其他工具驱动的
执行路径，不希望自动执行模型回复里的代码块，**显式**关掉响应
处理器：

```go
agent := llmagent.New(
    "demo",
    llmagent.WithModel(m),
    llmagent.WithCodeExecutor(local.New()),
    llmagent.WithEnableCodeExecutionResponseProcessor(false),
)
```

适合关掉围栏代码自动执行的典型场景：

- 只想使用 `workspace_exec`
- 只需要给某些工具提供 workspace/runtime
- 希望代码执行必须通过显式工具调用触发

与 `WithSkills(repo)` auto-fallback 的联动：当 skills 层代你**隐式**
注入本地 `CodeExecutor` 时（见 Agent Skills 指南），这个隐式 executor
的用途被严格收敛为 "只是给 `workspace_exec` 供电"：如果你没有显式
调用过 `WithEnableCodeExecutionResponseProcessor(...)`，框架会自动
把 `EnableCodeExecutionResponseProcessor` 置为 `false`，避免在未经你
配置时自动打开额外能力。相对地，**显式** `WithCodeExecutor(...)` 时该
开关保持框架默认，且不会被 skills 的隐式逻辑改写，从而不影响你原有
行为。

## 怎么选后端

常见后端有三种：

- `local.New()`
  直接在宿主机执行。接入最简单，调试最方便，适合本地开发和可信环境。
- `container.New()`
  在容器中执行。隔离更强，更接近生产环境，适合希望限制执行环境的场景。
- `jupyter.New()`
  适合 notebook / kernel 风格的代码执行，常用于数据分析或 Python 交互场景。

选择建议：

- 本地验证功能：优先 `local`
- 生产环境或更强调隔离：优先 `container`
- 明确需要 Jupyter kernel：使用 `jupyter`

## Workspace 中有哪些目录

执行器会在一个 workspace 中运行程序。常见目录约定如下：

- `work/inputs/`
  执行前准备好的输入文件。用户上传的文件通常会出现在这里。
- `work/`
  临时工作目录，适合处理中间文件。
- `out/`
  输出目录，适合放最终结果或后续还要继续使用的文件。
- `runs/`
  运行过程目录，常用于日志或辅助文件。

常用目录：

- 读用户输入：`work/inputs/`
- 写中间文件：`work/`
- 写结果文件：`out/`

## 用户上传的文件会出现在哪里

在支持会话文件自动 stage 的执行路径里，框架会在执行前把这些文件物化到：

- `work/inputs/` 目录下

实际文件名可能经过清洗或去重，不保证逐字保留原始文件名。

常见的传入方式有两种。

### 方式 1：直接把文件内容放进消息

```go
msg := model.NewUserMessage("请处理这个文件")
_ = msg.AddFilePath("/tmp/report.pdf")
```

也可以直接传二进制数据：

```go
msg := model.NewUserMessage("请处理这个文件")
_ = msg.AddFileData("report.pdf", pdfBytes, "application/pdf")
```

### 方式 2：先上传到 artifact，再在消息里只放引用

如果文件已经提前上传到 artifact，可以把 `artifact://...` 作为 `file_id`
放进消息：

```go
msg := model.NewUserMessage("请处理这个文件")
msg.AddFileIDWithName("artifact://uploads/report.pdf@1", "report.pdf")
```

执行前，框架会解析这个引用，并把文件写到 `work/inputs/` 下。

## 示例：先上传 artifact，再交给执行环境

下面的示例演示“先上传，再执行时自动 stage”的完整链路：

```go
package main

import (
    "context"
    "fmt"
    "os"
    "path/filepath"

    "trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
    "trpc.group/trpc-go/trpc-agent-go/artifact"
    "trpc.group/trpc-go/trpc-agent-go/artifact/inmemory"
    "trpc.group/trpc-go/trpc-agent-go/codeexecutor/local"
    "trpc.group/trpc-go/trpc-agent-go/model"
    "trpc.group/trpc-go/trpc-agent-go/model/openai"
    "trpc.group/trpc-go/trpc-agent-go/runner"
)

func main() {
    ctx := context.Background()

    const (
        appName   = "my-app"
        userID    = "user-1"
        sessionID = "sess-1"
    )

    artifactService := inmemory.NewService()

    rawPath := "/tmp/report.pdf"
    data, err := os.ReadFile(rawPath)
    if err != nil {
        panic(err)
    }
    base := filepath.Base(rawPath)

    info := artifact.SessionInfo{
        AppName:   appName,
        UserID:    userID,
        SessionID: sessionID,
    }

    name := "uploads/" + base
    version, err := artifactService.SaveArtifact(
        ctx,
        info,
        name,
        &artifact.Artifact{
            Data:     data,
            MimeType: "application/pdf",
            Name:     base,
        },
    )
    if err != nil {
        panic(err)
    }

    ref := fmt.Sprintf("artifact://%s@%d", name, version)

    msg := model.NewUserMessage("请读取这个文件并总结要点。")
    msg.AddFileIDWithName(ref, base)

    agent := llmagent.New(
        "demo",
        llmagent.WithModel(openai.New("gpt-4.1-mini")),
        llmagent.WithInstruction("请读取 work/inputs/ 中的文件并总结内容。"),
        llmagent.WithCodeExecutor(local.New()),
    )

    r := runner.NewRunner(
        appName,
        agent,
        runner.WithArtifactService(artifactService),
    )
    defer r.Close()

    events, err := r.Run(ctx, userID, sessionID, msg)
    if err != nil {
        panic(err)
    }
    for range events {
    }
}
```

要求：

- `SaveArtifact` 使用的 `AppName / UserID / SessionID`
- 需要和后面 `Runner.Run(...)` 使用的值保持一致

否则执行前解析 `artifact://...` 时，找不到对应 artifact。

## 与工作区工具的配合

暴露 `workspace_exec` 一类工作区工具后，常见使用方式如下：

1. 读取 `work/inputs/...`
2. 在 `work/` 或 `out/` 下处理文件
3. 再读取 `out/...` 并组织最终回答

目录约定的作用，是让模型和工具在稳定路径上协作，而不需要暴露底层
staging 过程。

## Workspace init hooks

Init hook 在 `WorkspaceManager.CreateWorkspace` **成功返回之后**、**在把 `Workspace` 交还给调用方之前**执行。通过常见的 `WorkspaceRegistry` 做会话级去重时，这通常对应**每个逻辑 workspace id 各一次**（多数情况下即每个 agent 会话工作区一次）。在 **trusted-local** 等会复用同一条物理目录的模式下，不同会话若仍走一次新的「工作区获取」，init 仍可能再跑；不要理解成「整个磁盘目录一生只跑一遍」。

适合先用 `InputSpec` 拉固定输入（`artifact://`、`host://` 等），再跑确定性初始化命令（例如
`pip install`）。使用 `codeexecutor.NewWorkspaceInitExecutor` 包装 `CodeExecutor`；若执行器不实现
`EngineProvider` 等，构造会**返回 error**，应对返回值做显式处理。

基于制品的输入要求在调用 `CreateWorkspace` 时的 `context` 上带有 artifact service 与（如适用）会话信息，要求与
`WorkspaceFS.StageInputs` 一致。常规的 llmagent workspace 工具链会从当前 `agent.Invocation` 注入这些信息，
下面的示例无需手写 context 装配。

```go
import (
    "fmt"
    "time"

    "trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
    "trpc.group/trpc-go/trpc-agent-go/codeexecutor"
    "trpc.group/trpc-go/trpc-agent-go/codeexecutor/local"
)

func newAnalystAgent() (*llmagent.LLMAgent, error) {
    exec, err := codeexecutor.NewWorkspaceInitExecutor(
        local.New(),
        codeexecutor.NewWorkspaceInitHook(codeexecutor.WorkspaceInitSpec{
            Inputs: []codeexecutor.InputSpec{
                {
                    From: "artifact://app/requirements.txt@3",
                    To:   "work/requirements.txt",
                    Mode: "copy",
                },
            },
            Commands: []codeexecutor.WorkspaceInitCommand{
                {
                    Name: "install-deps",
                    Cmd:  "bash",
                    Args: []string{
                        "-lc",
                        "python3 -m venv .venv && " +
                            ".venv/bin/pip install -q -r work/requirements.txt",
                    },
                    Timeout: 2 * time.Minute,
                },
            },
        }),
    )
    if err != nil {
        return nil, fmt.Errorf("workspace init executor: %w", err)
    }
    return llmagent.New(
        "analyst",
        llmagent.WithCodeExecutor(exec),
    ), nil
}
```

若后续改了 pin 过的依赖或其他初始化输入，需要换一个**新的逻辑 workspace**（例如新会话 id），或由你自己的流程再跑安装；
init hook 不会在磁盘文件变化后自动按文件再执行。

### 与 `skill_load` 的关系

通过 `skill_load` 加载的技能会在 `workspace_exec` 执行时按当前会话
写入 `skills/<name>`。不必在 init hook 的 `Inputs` 里重复声明技能来源；init hook 负责
与会话无关、且希望在任意工具运行前就位的那份固定物料与初始化命令。

## 哪些文件会保留

通常有两类情况：

- 仍然命中同一个物理 workspace
- 后续换成了一个新的 workspace

在同一个物理 workspace 中：

- `work/`、`out/` 里的文件通常还能继续用
- 之前写出的结果文件可以直接再次读取

如果换成了新的 workspace：

- 用户会话里的文件输入通常可以根据消息历史重新放回 `work/inputs/`
- 旧的 `out/**` 不会自动恢复
- 旧的 `work/**` 也不应该假设还能继续存在

如果你需要跨新 workspace 稳定复用某个文件，建议把它保存为 artifact，
或者让业务层自己管理持久化。

## provider 文件 ID 与 artifact 引用的区别

有些模型厂商支持原生 `file_id`。这类 ID 能不能在执行器侧重新读取，取决于
具体模型实现是否支持下载。

如果你希望文件能稳定地被执行器读取，通常更建议使用：

- `artifact://...`

因为这条链路由框架自己的 artifact service 管理，不依赖模型厂商的文件下载能力。

## 环境变量与执行环境

如果你的执行器运行在容器、远端或隔离环境里，通常需要显式注入环境变量。

这类场景常见用途包括：

- 给程序注入用户级 token
- 注入租户、地域或业务配置
- 避免把敏感值直接暴露给模型

如果你的场景需要这类能力，可以继续看 `codeexecutor` 的环境注入包装器：

- `NewEnvInjectingCodeExecutor`
- `NewEnvInjectingEngine`

## 常见问题

### 为什么模型找不到上传的文件

检查项：

- 是否真的把文件放进了消息
- 文件名是否和你提示里写的一致
- 是否为 Runner 配置了 `artifact.Service`
- `artifact://...` 使用的会话信息是否匹配

### 为什么 `out/` 里的旧文件下一轮没了

后续请求没有回到同一个物理 workspace 时，旧的 `out/**` 可能不存在。  
当前默认行为下，历史上传文件可以重新 stage，但 `out/**` 不会自动恢复。

### 什么时候该用 `work/`，什么时候该用 `out/`

- 临时中间文件：放 `work/`
- 想让后续步骤继续读取的结果文件：放 `out/`

### `codeexecutor` 是不是某个工具专属能力

不是。它是更底层的执行与 workspace 能力。  
是否暴露成某个具体工具，取决于上层 Agent 和工具编排方式。

## 参考

- 示例：
  - [examples/codeexecution/main.go](https://github.com/trpc-group/trpc-agent-go/blob/main/examples/codeexecution/main.go)（本地 backend）
  - [examples/codeexecution/container/README.md](https://github.com/trpc-group/trpc-agent-go/blob/main/examples/codeexecution/container/README.md)（Docker container backend）
  - [examples/codeexecution/jupyter/README.md](https://github.com/trpc-group/trpc-agent-go/blob/main/examples/codeexecution/jupyter/README.md)（Jupyter kernel backend）
- 相关文档：
  - [Artifact 文档](artifact.md)
