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

如果文件内容只给工具或代码执行器使用，可以为 OpenAI 模型配置
`openai.WithOmitFileContentParts(true)`，让 provider 请求省略 file content
parts。这个选项不会隐藏你放在 prompt 里的普通消息文本或文件名提示。当前
OpenAI adapter 使用 Chat Completions API，它只支持该接口接受的文件内容请求
形式。PDF 文件数据可以作为 file content 发送；Markdown 或纯文本内容应作为
普通消息文本传入，或只 stage 给工具使用，而不是作为 file content part 发送。

### 方式 2：先上传到 artifact，再在消息里只放引用

如果文件已经提前上传到 artifact，可以把 `artifact://...` 作为 `file_id`
放进消息：

```go
msg := model.NewUserMessage("请处理这个文件")
msg.AddFileIDWithName("artifact://uploads/report.pdf@1", "report.pdf")
```

执行前，框架会解析这个引用，并把文件写到 `work/inputs/` 下。

### 完整示例：先上传，再交给执行环境

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

## provider 文件 ID 与 artifact 引用的区别

有些模型厂商支持原生 `file_id`。这类 ID 能不能在执行器侧重新读取，取决于
具体模型实现是否支持下载。

如果你希望文件能稳定地被执行器读取，通常更建议使用：

- `artifact://...`

因为这条链路由框架自己的 artifact service 管理，不依赖模型厂商的文件下载能力。

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

## 应用代码访问 workspace

模型通过 `workspace_exec` 操作 workspace；**应用代码**有时也需要从
agent 跑完后的 workspace 里读出东西——例如 `AfterAgent` 把约定路径
下的产物回写到自己的 profile store，或者 `AfterTool` 把某次工具产
生的中间文件镜像出去；某些场景下还会需要在 callback 里直接跑一条
命令做校验或后处理。

`codeexecutor/workspaceio` 提供 facade `Workspace`。在
`WithCodeExecutor(...)` 配过的前提下，`LLMAgent.Run` 入口就把它注入
ctx，**任何**带 ctx 的钩子点都能直接拿到——`BeforeAgent` /
`AfterAgent` / `BeforeTool` / `AfterTool` / `BeforeModel` /
`AfterModel` / 你自己实现的工具内部均可。

它是 `codeexecutor.WorkspaceFS` + `ProgramRunner` 的薄封装：截断、
总量、原子性、非零 exit code 这类策略框架不替你做主——读完之后怎么
校验、超限怎么拒绝、命令失败怎么处理，都在 callback 里按需写。详
见后文 *调用方负责的策略*。

> **关于 `codeexecutor.Workspace` 和 `workspaceio.Workspace` 同名**
> ：前者是 v1 起公开的 workspace descriptor（`{ID, Path}`，无方法），
> 后者是本节的 facade。两者通过 import path 区分，编译器零歧义；
> 业务代码极少同行引用 descriptor。如确需同时 import：
>
> ```go
> import (
>     "trpc.group/trpc-go/trpc-agent-go/codeexecutor"
>     wsio "trpc.group/trpc-go/trpc-agent-go/codeexecutor/workspaceio"
> )
> ```

### 在 callback 里读写

`Workspace` 主要的两类用法：

- **`AfterAgent`**：本轮跑完，把约定路径下的产物
  （`skills/*/SKILL.md`、`out/report.pdf` 等）整体回写到自己的 store。
  失败时 workspace 状态不可信，跳过。
- **`AfterTool` + `args.ToolName` 特判**：只关心某个工具产生的产物
  时（`workspace_exec` 跑完后镜像它写出的文件、某个自定义 skill 工
  具结束后取中间结果），按工具名过滤再读，避免每次工具调用都触发
  一次 `Collect`。

下面示例里 `myStore.Save(...)` 和 `mirror(ctx, files)` 是你自己实现
的接口——文档不绑死它们的签名，按你的存储形态自己定。

```go
import (
    "trpc.group/trpc-go/trpc-agent-go/agent"
    "trpc.group/trpc-go/trpc-agent-go/codeexecutor/workspaceio"
    "trpc.group/trpc-go/trpc-agent-go/tool"
)

agentCB := agent.NewCallbacks()
agentCB.RegisterAfterAgent(func(ctx context.Context, args *agent.AfterAgentArgs) (*agent.AfterAgentResult, error) {
    if args.Error != nil {
        return nil, nil // 失败时 workspace 状态不可信，跳过镜像
    }
    ws, ok := workspaceio.WorkspaceFromContext(ctx)
    if !ok {
        return nil, nil
    }
    files, err := ws.Collect(ctx, "skills/*/SKILL.md")
    if err != nil {
        return nil, err
    }
    for _, f := range files {
        if f.Truncated {
            return nil, fmt.Errorf("%s 被 backend 截断", f.Path)
        }
        if err := myStore.Save(ctx, args.Invocation, f); err != nil {
            return nil, err
        }
    }
    return nil, nil
})

toolCB := tool.NewCallbacks()
toolCB.RegisterAfterTool(func(ctx context.Context, args *tool.AfterToolArgs) (*tool.AfterToolResult, error) {
    // 只在目标工具结束后镜像，避免对每个工具调用都全量扫 workspace。
    if args.ToolName != "workspace_exec" {
        return nil, nil
    }
    ws, ok := workspaceio.WorkspaceFromContext(ctx)
    if !ok {
        return nil, nil
    }
    files, err := ws.Collect(ctx, "out/**/*.json")
    if err != nil {
        return nil, err
    }
    return nil, mirror(ctx, files)
})

agent := llmagent.New("demo",
    llmagent.WithCodeExecutor(local.New()),
    llmagent.WithAgentCallbacks(agentCB),
    llmagent.WithToolCallbacks(toolCB),
)
```

> 不建议在 `BeforeAgent` 里用 `PutFiles` 把外部 profile / skill 文
> 件投影进 workspace ——那是「workspace 初始化」职责，应当走
> `codeexecutor.WorkspaceInitHook`（见前文 *Workspace init hooks*
> 章节，用 `InputSpec` 配合 `skill://` / `artifact://` / `host://`
> 等 scheme 在工作区创建时一次完成），而不是借 `Workspace` 在
> callback 里偷做。`Workspace` 的设计点是「读出 agent 跑出来的东
> 西」「把 workspace 文件转成 artifact」「在 callback 里跑一条命
> 令做校验/后处理」，不是为初始化路径服务的。

### 可用方法（`path` 一律相对 workspace 根）

- `Collect(ctx, patterns...)` — 按模式批量读，返回 `[]*File`（`Path` /
  `Data` / `MIMEType` / `SizeBytes` / `Truncated`）。模式语法与
  `codeexecutor.WorkspaceFS.Collect` 一致：字面量路径
  （`skills/echoer/SKILL.md`）、通配（`out/*.json`、`runs/**/result.md`）
  都是合法 pattern。单文件读也用这个入口（传字面量 pattern，取
  `result[0]`）。
- `PutFiles(ctx, files...)` — 写入 1～N 份 `codeexecutor.PutFile`，
  自动建父目录。单文件写也用这个入口（传一个 `PutFile`）。
  `PutFile.Mode == 0` 时回落到 backend 默认（local 是 0o644）；要
  0o755 用 `codeexecutor.DefaultExecFileMode`。
- `SaveArtifact(ctx, relPath, opts...)` — 把 workspace 文件持久化为
  artifact（需要 Runner 配 `artifact.Service`），返回 `*ArtifactRef`。
- `StageInputs(ctx, specs)` — 按 `artifact://` / `host://` /
  `workspace://` / `skill://` 把外部输入拉进 workspace。
- `RunProgram(ctx, spec)` — 在 workspace 里跑一条命令（`spec.Cwd` 相
  对 workspace 根，不能越界），返回 `codeexecutor.RunResult`
  （`Stdout` / `Stderr` / `ExitCode` / `Duration` / `TimedOut`）。
  **非零 exit code 不是 error**，按 Go `os/exec` 惯例通过
  `RunResult.ExitCode` 报告——调用方自己看 `ExitCode` / `TimedOut`
  决定怎么处理。`error` 仅在「框架层失败」时返回（没配 executor、
  backend 拒绝、launch 失败、内部超时）。

`Workspace` 没有内部锁，并发使用要自行串行化。

### `RunProgram` 怎么写 `Cmd` / `Args`

`Cmd` 是可执行文件名（**不走 shell 解析**），`Args` 是传给它的参数
列表。`workspace_exec` LLM 工具底层也是同一条 `ProgramRunner.RunProgram`
路径——两种形态够用。

**跑一行 shell**（跟 `workspace_exec` 接收 LLM 命令时完全一致）：

```go
res, err := ws.RunProgram(ctx, codeexecutor.RunProgramSpec{
    Cmd:     "sh",
    Args:    []string{"-lc", "ls -la work && cat out/report.md"},
    Cwd:     "",                  // 留空就是 workspace 根
    Timeout: 30 * time.Second,
})
if err != nil {
    return err
}
if res.ExitCode != 0 || res.TimedOut {
    return fmt.Errorf("post-check failed: exit=%d timed_out=%v: %s",
        res.ExitCode, res.TimedOut, res.Stderr)
}
```

**直接跑某个程序**（不开 shell，参数零转义、更可控）：

```go
res, err := ws.RunProgram(ctx, codeexecutor.RunProgramSpec{
    Cmd:  "go",
    Args: []string{"vet", "./..."},
    Cwd:  "work",                 // 在 ${WORK} 下执行
    Env:  map[string]string{"GOFLAGS": "-count=1"}, // 追加/覆盖，不替换整个 env
})
```

其余字段：

- `Stdin` — 字符串，启动后由框架喂给 stdin，跑完即关；跑交互式程序请用
  `workspace_exec` LLM 工具或自己起 session。
- `Timeout` — 单次执行墙钟超时；超时后 `RunResult.TimedOut = true`，
  `error` 仍为 `nil`。
- `Env` — 追加/覆盖一组环境变量；workspace 已注入 `${WORK}` / `${OUT}`
  / `${RUNS}` / `${WORKSPACE_DIR}` 这几个根目录路径，可在 `Args` 里直
  接引用。

### `*File`：读出来的文件长什么样

`Collect` 返回的 `*File` 是后端无关的快照：

```go
type File struct {
    Path      string // workspace 相对路径，例如 "skills/echoer/SKILL.md"
    Data      []byte // 拷贝过的字节，调用方可随意 mutate
    MIMEType  string // 后端检测的 MIME；空表示后端没填
    SizeBytes int64  // 文件实际大小；可能 > len(Data) 当 Truncated=true
    Truncated bool   // 后端读到内部上限被截断；框架不会因此报错
}
```

实际用法就两类：

- **Mirror 到外部存储**：`os.WriteFile(dst, f.Data, 0o644)` /
  `s3.PutObject(key, bytes.NewReader(f.Data))`；`f.Path` 直接当目标
  key / 子路径。
- **结构性校验**：`bytes.Contains(f.Data, []byte("# "))`、
  `yaml.Unmarshal(f.Data, &frontmatter)` 之类，校验失败就拒绝镜像。

### `*ArtifactRef` + `WithSaveArtifactMaxBytes`：把 workspace 产物公开给后续轮次

`SaveArtifact` 适用于"模型这一轮在 `out/` 写出了一份要被后面引用的
产物（生成的 PDF、合成数据集、训练 checkpoint ...）"。返回的
`ArtifactRef` 字段名和 LLM 工具 `workspace_save_artifact` 输出一致：

```go
ref, err := ws.SaveArtifact(ctx, "out/report.pdf",
    workspaceio.WithSaveArtifactMaxBytes(8 * 1024 * 1024))
if err != nil {
    return err
}

// ref.Ref       == "artifact://out/report.pdf@3"  // 拼好的引用串
// ref.SavedAs   == "out/report.pdf"               // artifact key
// ref.Version   == 3
// ref.SizeBytes == 4_812_345

// 把引用回写到 session state，让下一轮的 prompt 可以直接用 ref.Ref
args.Invocation.Session.AppendStateValue("last_report", ref.Ref)
```

`WithSaveArtifactMaxBytes` 在后端**读取阶段**就生效（不是读完再判长
度），超限直接报错——比"读出来再丢弃"省内存、也能更早失败。典型
场景：

- **可能过大的产物**（生成的数据集、模型权重、视频）— 设 8 MiB /
  32 MiB 让后端早失败，不要把 GiB 级数据塞进 artifact service。
- **审计 / 合规边界** — 把"单 artifact 体量"钉死成业务约束，防止
  agent 出 bug 产生超大产物。

不传时默认 `64 MiB`，够大多数文档 / 小数据，一般场景不用关心。

### 调用方负责的策略

框架不替你做主，下面这些都在你的 callback 里自己写。

**读到内容后怎么处理**

- *失败时是否镜像？* 默认行为：示例 `args.Error != nil` 时直接
  `return`——workspace 在失败时状态不可信。**要事故快照**就反过来，
  错误也照镜。
- *单文件被截断了怎么办？* 默认行为：backend 单文件读有内部上限
  （一般几 MiB），命中后置 `File.Truncated = true` 并照常返回，**不**
  自动报错。**要严格语义**自己加 `if f.Truncated { return error }`。
- *扫出来太多 / 太大怎么办？* 默认行为：`Collect` 不限返回数量、
  也不限总字节。**要预算**就在结果上 `len(files)` / 累加
  `SizeBytes` 自己卡，超了拒绝。
- *`RunProgram` 退出码非零怎么办？* 默认行为：通过 `RunResult.ExitCode`
  / `TimedOut` 报告，**不**作为 error 返回。要把非零 exit 当失败处
  理就自己 `if res.ExitCode != 0 { return error }`。

**写到自己的 store 时**

- *需要 all-or-nothing？* `Collect` + 循环 `Save` **不是事务**。要原
  子性就先写临时位置最后 rename，或用支持事务的存储。

**callback 返回 error 会发生什么**

- 从 `AfterAgent` / `AfterTool` 返回非 `nil` 会**终止当前 invocation**。
  flush 想做 best-effort 的话自己 log 后吞掉。

完整可运行示例：[examples/workspace_io](https://github.com/trpc-group/trpc-agent-go/tree/main/examples/workspace_io)。

## 限制 `workspace_exec` 可执行的命令

`workspace_exec` 会执行模型发过来的任意 shell 命令。当沙箱允许出网时，
这会变成一个 prompt-injection / SSRF 入口：用户 prompt 诱导模型跑
`curl <内网域名>`，沙箱就老老实实地跑了。

为了收敛这个面，可以给它配上可执行命令的白名单和/或黑名单。只要任意
一个列表非空，命令在执行前会被解析，不通过就直接拒掉。

### 怎么配

在 Agent 级别：

```go
agent := llmagent.New(
    "my-agent",
    llmagent.WithCodeExecutor(executor),
    llmagent.WithWorkspaceExecAllowedCommands("ls", "cat", "rg", "git"),
    llmagent.WithWorkspaceExecDeniedCommands("curl", "wget", "nc"),
)
```

也可以直接在 `ExecTool` 上配（第一个参数是 `codeexecutor.CodeExecutor`，
例如 `localexec.New()`）：

```go
tool := workspaceexec.NewExecTool(
    executor,
    workspaceexec.WithAllowedCommands("ls", "cat", "rg", "git"),
    workspaceexec.WithDeniedCommands("curl", "wget", "nc"),
)
```

或通过环境变量做部署期配置（用逗号或空白分隔）：

- `TRPC_AGENT_WORKSPACE_EXEC_ALLOWED_COMMANDS`
- `TRPC_AGENT_WORKSPACE_EXEC_DENIED_COMMANDS`

显式 Option 优先级高于环境变量。两个都不设就关闭策略，行为与历史完全一致。

### 还能怎么写

由白名单命令通过安全的顺序操作符串起来的管道是允许的：

```text
ls | rg foo
git status && git diff
test -f x.txt || echo missing
mkdir -p out; cp a.txt out/
```

参数里允许单引号串、双引号串和 `\X` 转义字面量。`{}` 作为字面量是允许的，
所以 `find -exec {} \;` 这种写法能正常解析。注意：`xargs` 本身在下文那一
组**不可覆盖**的内置拒绝集合里，所以 `xargs -I{}` 不管 allow 列表里有
没有 `xargs` 都会被拒掉。

### 会被拒掉的写法

只要策略开启，命令在做名称查表**之前**就会被结构性拒掉，前提是出现以下
任意一种：

- 命令替换、参数展开、算术展开、进程替换
  （`$(…)`、`` `…` ``、`$VAR`、`${X}`、`$((…))`、`<(…)`、`>(…)`）
- 任意形式的重定向（`>`、`>>`、`<`、`2>&1`、here-doc）
- 子 shell、复合块、控制流、函数定义
  （`(…)`、`{…}`、`if/for/while/case`、`f() { … }`）
- 后台执行、`|&`、行首 `VAR=… cmd`、通配符、`!`、`#`、裸换行或转义换行

所以即使 `curl` 在拒绝列表里，也没法通过 `$(c\url)`、
`echo \`curl http://x\``、`curl > /tmp/x`、`(curl http://x)`、
`HOME=/tmp curl http://x` 等方式绕开。

在解析器之上还有一组**不可覆盖的内置拒绝集合**，涵盖 shell 包装器、
会重新执行命令的 builtin、"拿后续 argv 当命令跑"的进程包装器，以及
会注册延迟执行代码或改变后续解析状态的有状态 shell builtin。只要
策略开启就会被无条件拒掉，因为它们能以人畜无害的 `argv[0]` 启动
任意代码（如 `time curl http://x` 绕过对 `curl` 的 deny）、注册延迟
执行的代码（如 `trap 'curl http://x' EXIT`），或者改写后续解析环境
（如 `export PATH=./bin && allowed_cmd`）：

- shell 包装器：`sh`、`bash`、`zsh`、`ash`、`dash`、`ksh`、`mksh`、
  `fish`、`pwsh`、`powershell`、`cmd`、`busybox`、`toybox`
- 会重执行命令的 builtin：`eval`、`exec`、`command`、`source`、`.`、
  `builtin`
- 进程包装器：`xargs`、`env`、`nohup`、`timeout`、`sudo`、`su`、`doas`、
  `setsid`、`unshare`、`chroot`、`runuser`、`time`、`nice`、`ionice`、
  `taskset`、`stdbuf`、`strace`、`ltrace`、`script`、`flock`
- 有状态 shell builtin：`trap`、`alias`、`unalias`、`enable`、`export`、
  `unset`、`readonly`、`local`、`declare`、`typeset`、`set`、`shopt`、
  `hash`、`cd`、`pushd`、`popd`
- 会给 shell 变量赋值的 builtin：`printf`、`read`、`getopts`、`let`、
  `mapfile`、`readarray`。在同一进程里跑整条管道的 shell（macOS 以及
  很多容器镜像里 `/bin/sh` 就是 bash）上，这些 builtin 可以在后续允许
  的命令之前改写 `PATH` 等解析状态。例如 `printf -v PATH ./work/bin; git`
  在不拦 `printf` 的策略下，`git` 会被解析成 `./work/bin/git`，即便
  `printf` 与 `git` 的 `argv[0]` 都通过了 allow/deny 检查。

`workspace_exec` 自带 `cwd` 参数来覆盖正常的 cwd 切换场景，所以模型
不需要也不应该自己调 `cd`。

这个集合 **不能** 通过 `WithWorkspaceExecAllowedCommands` 覆盖——把这些名字
写进白名单也会被忽略。如果业务真的需要其中某一个（少见但合理），更稳妥
的做法是写一个 workspace 下的脚本，把要做的事固化下来，再把这个脚本放进
`allowed_commands`。脚本本身可审计，reviewer 一眼就能看明白到底放开了
什么。

### 匹配规则

allow 和 deny 的匹配是**不对称的**，目的是防止工作区里被注入写入的同名
文件绕过 allow 列表：

- **Allow 是严格匹配**：写 `echo` 只能放过裸 `echo`，`./echo`、
  `work/bin/echo`、`/usr/bin/echo` 都会被拒。要放开某个具体路径，就把
  完整路径写进去（例如 `WithWorkspaceExecAllowedCommands("/usr/bin/echo")`）。
- **Deny 是宽松匹配**：写 `curl` 会同时拦下 `curl`、`/usr/bin/curl`、
  `./curl`，避免攻击者通过加路径绕过黑名单。

大小写处理按底层文件系统的语义来，避免白名单在大小写敏感 FS 上被
默默放宽：

- **Deny 与内置 deny** 在所有 OS 上都做大小写折叠。`deny: ["curl"]`
  同时拦下 `curl`、`Curl`、`CURL`；内置 deny 也能拦下 `SH -c`、
  `Sh`、`Bash` 等变体。macOS 默认 APFS 大小写不敏感（`CURL` 解析到
  `/usr/bin/curl`），Windows 同理；Linux 上折叠是对工作区里可能塞进
  大写同名二进制的纵深防御。
- **Allow** 按条目形式分开处理：
    - **带路径的条目**（含 `/` 或 `\`，如 `./safe`、`work/bin/echo`、
      `/usr/bin/echo`）在所有 OS 上都保持**精确大小写**匹配。我们没办法
      可靠地判断工作区所在卷的大小写敏感性（macOS APFS 支持创建大小写
      敏感卷，容器层也可能挂载不同的 FS），一旦折叠就会在这些卷上把
      `./safe` 默默放宽到包含工作区里被注入写入的 `./SAFE`。需要两种
      写法时请同时列出。
    - **裸命令名条目**（如 `echo`）走 `PATH` 解析，而策略开启时 `PATH`
      已经被重置为可信默认值。这一类按 OS 习惯：Windows 与 macOS 折叠
      大小写，Linux 保持精确。所以 `WithWorkspaceExecAllowedCommands("echo")`
      在 macOS / Windows 上能放过 `ECHO`，但在 Linux 上只放过 `echo`。

Windows 下匹配时还会额外忽略常见可执行后缀（`.exe`、`.cmd`、`.bat`、
`.com`、`.ps1`），所以 `cmd` 能拦住 `cmd.exe`、`curl` 能拦住
`CURL.EXE`、`echo` 也能放过 `ECHO.EXE`。配置项本身也会走同一套规则，
所以 `WithWorkspaceExecDeniedCommands("CURL")` 同样能拦住裸 `curl` 和
`curl.exe`。

### 优先级

同一个名字同时出现在两个列表里时，**deny 赢**：

```text
explicit Deny  >  implicit deny  >  explicit Allow  >  implicit allow
```

也就是说 `WithWorkspaceExecAllowedCommands("git") + WithWorkspaceExecDeniedCommands("git")`
仍然会拒掉 `git`。把 `sh` 写进 allow 列表也救不回来，它仍然在 implicit
deny 里。

### 拉起 shell 时的加固

策略开启时，拉起 shell 这一步本身也会做加固，避免 shell 启动文件和搜索
路径成为绕过通道：

- 调用形式从 `sh -lc` 改为 `sh -c`，不再先 source `/etc/profile` 和
  `$HOME/.profile`；
- 单次调用的 env 会被清洗：`HOME`、`ENV`、`BASH_ENV`、`PROMPT_COMMAND`、
  `PS4`、`SHELL`、`SHELLOPTS`、`BASHOPTS`、`PATH`、`IFS`、`CDPATH`、
  `GLOBIGNORE`、`LD_PRELOAD`、`LD_LIBRARY_PATH`、`LD_AUDIT`、
  `DYLD_INSERT_LIBRARIES`、`DYLD_LIBRARY_PATH`、`DYLD_FORCE_FLAT_NAMESPACE`
  以及任何 `BASH_FUNC_*` 条目（Shellshock）都会被去掉。`LANG` 等无害
  变量原样透传。
- 之所以连 `PATH` 也丢，是因为策略只按命令名匹配；调用方推一个
  `PATH=./bin:$PATH`、再在工作区里塞一个 `./bin/echo`，按名字校验是通的，
  但实际跑的是攻击者代码。丢掉 `PATH` 之后，被允许的命令会按 shell 默认
  `PATH` 解析。
- Windows 下清洗时会先把 env key 折成大写再比对，因为 Windows 运行时本身
  就把 env key 当作大小写不敏感。所以调用方传 `Path=./bin`、`Home=.`、
  `Bash_Env=…` 或 `bash_func_x%%=…`，都会跟规范形式一样被清掉。
- env 里 **key** 不符合 POSIX 名（`/^[A-Za-z_][A-Za-z0-9_]*$/`）的条目
  会被直接丢掉。这覆盖最直接的几种绕过（key 含 `=`、嵌入 `\n` / `\r` /
  `\0`），同时也挡掉 runtime 用 shell 字符串拼 env 注入（`env KEY=value
  <cmd>`）时的 shell 元字符绕过：一个 key 写成 `"X; curl http://x #"`
  放进那个模板，shell 会先跑 `curl` 再跑被校验过的命令。
- 策略开启时 `RunEnvProvider` 返回的条目也走同一套清洗。
  `codeexecutor.mergeProviderEnv` 会读 `spec.CleanEnv`，把 provider
  给出的 key 过一遍 `internal/envscrub` 的黑名单，所以
  `NewEnvInjectingCodeExecutor` 的 provider 即便返回 `PATH` /
  `BASH_ENV` / `LD_PRELOAD`，也不能在 `workspace_exec` 清洗完之后
  再把它们塞回去。

不设策略时这套都不会生效：`sh -lc` 和调用方传入的 env（包括 `PATH`）
都保持原样。

!!! warning "策略模式要求 runtime 支持 CleanEnv"
    上面的 `sh -c` 改写和 env 清洗只有在底层 runtime 真正 honor
    `RunProgramSpec.CleanEnv` 时才安全。为了避免在忽略 `CleanEnv` 的后端
    上悄悄降级安全契约，`workspace_exec` **fail-closed**：配置了
    `WithWorkspaceExecAllowedCommands` / `WithWorkspaceExecDeniedCommands`
    后，如果 runtime 的 `codeexecutor.Capabilities.SupportsCleanEnv`
    是 `false`，工具会在 spawn 之前直接拒掉这次调用，错误信息指引
    operator 切到支持的 runtime。

    目前只有 `codeexecutor/local` 声明了 `SupportsCleanEnv: true`。
    `codeexecutor/container` 和 `codeexecutor/e2b` 保持 zero-valued
    capabilities，所以这两个后端上的策略模式当前会在闸门处被拒掉。
    给它们实现 `CleanEnv`（让它们声明 capability 后闸门自动放开）
    跟在 [#1845](https://github.com/trpc-group/trpc-agent-go/issues/1845)
    里。在那之前，请在 local 后端上开策略模式，或者去掉 allow/deny
    列表，把 env / 网络隔离交给沙箱层。

### 边界

强制点是**可执行文件名**这一级。如果一个被允许的命令本身会根据参数再去
执行别的命令——比如 `find . -exec curl …`、
`awk 'BEGIN{system("curl …")}'`、`git -c protocol.ext.allow=…` ——内层命令
是它自己的子进程，不会再走一遍策略。按参数粒度做校验是后续迭代项；
在那之前，请把沙箱出口网络策略当成主防线，把这里的 allow/deny 当成纵深
防御的一层。

!!! note "Allow 列表比 deny-only 严格得多"
    `WithWorkspaceExecAllowedCommands(...)` 是闭集：列表（以及内置 deny
    集合）之外的一切都会被拒掉。
    `WithWorkspaceExecDeniedCommands(...)` 只挡明确列出的工具。如果只配
    deny，攻击者只要能找到任何不在 deny 集合里的二进制——包括他自己
    通过被放开的编辑器写进工作区的脚本——就能执行任意代码。条件允许的
    话请优先用 allow 列表，deny 只作为额外的纵深防御。

    一些**目前不在**内置 deny 集合里，但本身能根据参数执行任意代码的常见
    工具类别：调试器 / 探针（`gdb`、`lldb`、`valgrind`、`perf`）、解释器
    （`python`、`perl`、`ruby`、`node`、`awk`、`lua`）、构建 / 包管理 / git
    （`make`、`npm`、`pip`、`cargo`、`go run`、`git -c …`、
    `git --exec-path=…`）、`find -exec`，以及编辑器逃逸（`vim -c :!`、
    `less !`）。如果你确实选了 deny-only 模式，请把这些里你不需要的也加进
    `denied_commands`，或者改用 allow 模式。

如果你需要的是更底层、不经 shell 的 skill 执行限制，可以看 `skill_run`
上对应的 `WithSkillRunAllowedCommands` / `WithSkillRunDeniedCommands`，
参考 [skill](skill.md)。

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
