# Putting Work in the Right Boundary: Multi-Agent Design in tRPC-Agent-Go

> A multi-agent design problem is rarely just "should we add more roles?" The more useful question is: why should this piece of work leave the current Agent boundary? Context, capability surface, control flow, lifecycle, output visibility, and runtime ownership lead to different tRPC-Agent-Go mechanisms.
>
> [tRPC-Agent-Go](https://github.com/trpc-group/trpc-agent-go/) is an autonomous multi-Agent framework for Go. It provides tool calling, session and memory management, artifact management, multi-Agent collaboration, graph orchestration, knowledge bases, observability, and more. tRPC-Agent-Go grows with community support. Stars are welcome.

When teams integrate multi-Agent capabilities, the hard part is often not the concept. It is a set of concrete decisions:

- After a specialist analyzes a large search or statistics task, should the parent Agent receive the result and continue coordinating?
- After an entry Agent routes a user to a specialist, should the next user turn continue with that specialist or start from the entry Agent again?
- If a generation task keeps modifying the same artifact, should the child Agent keep its own stable history?
- In support, review, or consultation handoffs, should the target Agent receive full conversation history or only the input it needs?
- If each request needs a different combination of tools, skills, and constraints, should the parent Agent expose more tools or create a narrower per-call execution unit?
- If the outer workflow is already a Graph, does a node still need to delegate internally to another Agent?

These questions look different on the surface, but they ask the same thing: what runtime boundary should this work move into?

This article is not an API catalog. AgentTool, Dynamic AgentTool, `transfer_to_agent`, Team / Swarm, Chain / Parallel / Cycle / Graph, TaskRun, A2A / Remote Agent, and Explorer are not different names for the same idea. They change different boundaries.

## 1. Start With One Agent

Most Agent systems should start as a single Agent: one main Agent, a controlled set of tools, an instruction, the necessary knowledge sources, and session management. If that structure completes the task reliably, do not split it just to make the system look like a team.

Multi-agent design becomes an engineering problem when one boundary of the single Agent starts to fail:

- The context becomes noisy because search results, reads, retries, and tool outputs keep rolling into later turns.
- The capability surface becomes too wide, so the model misselects tools, misses tools, or hallucinates capabilities.
- Control should move to a specialist for the current turn instead of returning to the entry Agent.
- The work outlives one invocation and needs wait, query, cancel, transcript, or recovery semantics.
- Output and observability need layering: the user may not need to see the process, but the parent Agent still needs a decision-ready result.
- The runtime is actually another model, executor, service identity, permission domain, or remote system.

So the first question should not be "how many Agents do we need?" It should be "why is this work no longer a good fit for the current Agent boundary?"

## 2. What an Agent Boundary Means

In tRPC-Agent-Go, an Agent is best understood as a runtime boundary, not only as a prompt object. It decides who executes a piece of work, which instructions, messages, tools, skills, runtime state, and callbacks are assembled for this turn, and how events are written back to session and trace.

From the model-call perspective, the model sees the current `messages` and `tools`. `session`, `memory`, runtime state, artifacts, and traces are framework-side facts. Unless they are projected into `messages` or declared as `tools`, they do not directly affect the model call.

Terms such as "parent Agent", "child Agent", "current response owner", and "available tool" are not identities remembered by the model. They are runtime facts reconstructed by the framework for the current invocation.

Multi-agent design is therefore boundary design. When you switch Agents, the real changes are visible information, available capabilities, control ownership, state continuity, output routing, and runtime responsibility.

## 3. Six Boundary Questions

Use this table as the first decision pass. A real requirement may touch several rows, but identifying the main boundary makes the API choice much easier.

| Boundary | Signal | Ask First | Common Misread |
| --- | --- | --- | --- |
| Context | Search, read, trial, and tool results create a low-signal history tail | Does the parent need the full process or only conclusion, evidence, and references? | Isolation does not mean the parent should see nothing |
| Capability | Too many tools, skills, or knowledge sources cause misselection or hallucinated ability | Is this just filtering inside one Agent, or a new capability boundary? | Tool visibility is not a permission boundary |
| Control | The current turn should be continued by a specialist | Who owns this invocation, and who owns the next turn? | Call-and-return delegation is not control handoff |
| Lifecycle | Work must be waited on, queried, canceled, or resumed after the current call | Does it need a run id, state machine, persistence, and result query? | A background task is not a longer tool call |
| Output and Observability | Process should be visible or auditable, but not necessarily in future prompts | What does the parent receive, what does the user see, and what enters later `messages`? | Hiding UI does not remove the need for information flow |
| Runtime | Work must run under another model, executor, identity, or remote service | Is the boundary about capability, deployment, permission, team, or state ownership? | Do not split services only because the prompt sounds like a specialist |

## 4. Reading tRPC-Agent-Go Mechanisms by Boundary

The table below is a boundary index, not an API reference. It shows where the request goes, what context it carries, where the result returns, and who continues the current turn by default. If the default boundary does not match the product expectation, then look for options or external coordination.

| Mechanism | Default Boundary | Good Fit | Options or Extra Design |
| --- | --- | --- | --- |
| Single LLMAgent | One Agent advances with its own instruction and tool surface | Controlled tools and simple flow | Tool surface, context, permission, model, or runtime boundary grows |
| AgentTool | A fixed child Agent runs as a synchronous tool; history is isolated by default; result returns as tool result | Search, review, analysis, small specialist work | Parent history, persistent child history, result trimming, inner stream forwarding, skip outer summarization |
| Dynamic AgentTool | One `dynamic_agent` entrypoint creates a short-lived child Agent per call; isolated by default; no stable child history | Per-task narrowing of instruction, tools, or skills | Capability bounds, providers, exposed fields, timeout, response mode |
| `transfer_to_agent` | `WithSubAgents` exposes transfer; the target Agent takes over the current invocation; the source Agent ends the turn by default | The specialist should continue answering the current turn | Default handoff message, whether to end, message projection, cross-turn owner |
| Coordinator Team | Members are wrapped as tools; results return to the coordinator | A coordinator calls multiple members in one turn | Member history, inner events, member text visibility, coordinator summarization |
| Swarm | Entry member starts; members hand off via transfer; the next user turn starts from entry by default | Handoff chains inside the current turn | Cross-request transfer, independent member history, custom handoff input |
| Chain / Parallel / Cycle | Code structure carries sequential, parallel, or loop execution | Clear, repeatable, evaluable steps | Agent input/output wiring, termination condition, error handling |
| Graph | Graph structure carries flow, routing, state, and node execution | Fixed or semi-fixed workflow with state, conditional routing, checkpoints, or recovery | Whether nodes need AgentTool or transfer internally; how Graph state enters nodes |
| TaskRun | Control tools start a worker with a run id and child session; parent uses list / get / wait / cancel | Long-running, waitable, cancelable, queryable work | External store, queue, lease, controller, recovery |
| A2A / Remote Agent | A remote Agent is accessed through a local proxy; local session, memory, and runtime state are not inherited automatically | Reusing Agent services owned by another deployment or team | Identity, session mapping, permissions, memory, result contract, observability |
| Explorer | Built-in exploration Agent; by default inherits user tools, knowledge, and skills from the direct parent invocation | Read-only exploration, context discovery | Read-only is an advisory prompt constraint; use tool narrowing or permission policy for hard isolation |

Graph is easy to misread. Graph owns outer flow, state, and routing. Whether a node can delegate internally depends on the Agent that runs inside that node. `graphagent.WithSubAgents` lets `AddAgentNode(id)` find the configured Agent. It does not automatically inject delegation tools into every LLM node. If a fixed specialist step is needed, use an Agent node. If an LLM node should autonomously delegate, configure AgentTool or `WithSubAgents` on that node's Agent.

## 5. First Separate Three Return Paths

When another Agent participates, do not start with options. First ask how the request leaves, how the result returns, and who continues the turn. In tRPC-Agent-Go, most cases fit three paths.

| Path | Mechanisms | Default Mental Model |
| --- | --- | --- |
| Call-and-return delegation | AgentTool, Dynamic AgentTool, Coordinator member tool, Remote Agent wrapped as AgentTool | Parent sends a request; target finishes and returns a tool result to the parent |
| Handoff | `transfer_to_agent`, Swarm handoff, Remote Agent used as sub-agent | Target Agent takes over the current invocation; output is not first returned as a tool result to the source Agent |
| Managed run | TaskRun | Parent starts a worker, receives a run id, then manages status and result through list / get / wait / cancel |

### 5.1 AgentTool: Call and Return

AgentTool is for "I need a specialist result, then the parent should continue deciding." The parent model sees a normal tool. The tool internally runs the child Agent. The child result returns as a tool result, and the parent can continue with another model call.

The important part is the result contract. The parent should not receive a raw transcript, and it should not receive only "done." A useful result usually contains conclusion, evidence, important actions, uncertainty, next step, and artifact references.

Read common options by output path:

- `WithHistoryScope(agenttool.HistoryScopeParentBranch)`: the child can inherit parent branch history. `NewTool` defaults to isolated history.
- `WithPersistentHistory*`: a fixed AgentTool can use stable child history, useful when the same child continues work on the same artifact or thread.
- `WithResponseMode(agenttool.ResponseModeFinalOnly)`: the tool result uses only the child's last complete assistant message.
- `WithStreamInner(true)`: inner events are forwarded to the parent flow. This affects UI / event stream and does not mean every internal step enters the parent prompt.
- `WithSkipSummarization(true)`: the outer post-tool summarization model call is skipped. This fits passthrough behavior, not coordinator synthesis.

### 5.2 Dynamic AgentTool: Per-Call Capability Assembly

Dynamic AgentTool is for tasks where each call needs a narrower instruction, tool set, or skill set. It exposes a default entrypoint named `dynamic_agent` and creates one short-lived child Agent for the current tool call.

"Dynamic" does not mean the model can create arbitrary Agents. Code still defines the maximum boundary. The boundary can come from the parent invocation's effective capability surface, or from explicit options such as `WithCapabilityTools`, `WithCapabilityProvider`, `WithCapabilitySurfaceProvider`, and `WithCapabilitySkills`. The model can only select a subset within that boundary. It cannot choose arbitrary models, executors, or remote targets.

This solves capability-surface narrowing, not long-term memory. `NewDynamicTool` is short-lived, and `WithPersistentHistory*` is ignored. If the requirement is "the same child should keep editing the same artifact next time," prefer a fixed AgentTool with persistent history, or put artifact state in an external object.

### 5.3 transfer and Swarm: Who Owns This Turn

`transfer_to_agent` answers "who should continue this invocation?" When an LLMAgent is configured with `WithSubAgents`, the framework exposes `transfer_to_agent`. The target Agent starts with its own instruction and tool surface, and by default the source Agent ends the turn after the transfer.

This differs from AgentTool:

- AgentTool means "the specialist returns a result to me as a tool result."
- transfer means "the specialist continues this turn."

Swarm wraps this handoff behavior in a Team abstraction. By default, each new user message starts from the entry member. If the last handoff target should own future user turns, enable `team.WithCrossRequestTransfer(true)` and reuse the same session. If members need private history, use `team.WithSwarmIndependentAgents()`. If handoff input should be built from the root input, a template, or other context, use `team.WithSwarmHandoffInputBuilder`.

Therefore, "after transfer, will the next turn stay with the child?" is not answered by `sessionID` alone. The session stores events. The active member decides the next entrypoint. Message filters and projectors decide what later model requests actually see.

### 5.4 TaskRun: Becoming a Managed Run

TaskRun is for work that cannot be contained by the current invocation. It may take longer than the parent should wait, and later code may need to query status, wait for result, cancel it, or read a transcript.

The core of TaskRun is not "a longer AgentTool." It is a run contract. `start_task_run` creates work with a run id, usually in a child session. The parent later uses `list_task_runs`, `get_task_run`, `wait_task_run`, and `cancel_task_run` to manage it. If transcript tooling is configured, the parent can also read child session fragments.

`sync` mode only means the control tool waits for the child run to reach a terminal state. It still creates a run id and uses the same lifecycle. Production TaskRun needs external storage, queueing, leases, cancellation, retry, and recovery. The in-process implementation is suitable for local use, tests, and single-process adapters. Multi-node deployments should implement their own `taskrun.Controller`.

### 5.5 Remote Agent: Runtime Ownership Is Elsewhere

A2AAgent is a local proxy for a remote Agent and implements `agent.Agent`. It can be run by a Runner and used in other collaboration modes like a regular Agent.

Remote execution does not decide the return path by itself. If an A2AAgent is wrapped as AgentTool, it is call-and-return delegation. If it is used as a sub-agent, it can participate in transfer. If it is used as a Graph node target, it is a workflow node.

Remote boundaries require explicit design: identity, session mapping, permissions, memory, runtime state, artifacts, error semantics, trace correlation, and result contracts do not become consistent automatically just because both sides expose an Agent shape.

## 6. Capability Boundaries: Filter, Fix, or Assemble

When the capability surface grows, there are three common layers.

**Filter inside the same Agent.** `agent.WithToolFilter(...)` or `llmagent.WithToolFilter(...)` is useful for stable filtering by user, session, tenant, or mode. It is low cost, but it does not create a new Agent boundary. Tool visibility is not permission; execution must still be enforced by tools, executors, and permission policies.

**Create a fixed capability boundary.** AgentTool, `WithSubAgents`, Coordinator members, Swarm members, TaskRun workers, and A2AAgent can all wrap a stable set of instructions, tools, skills, model, or executor into a smaller work unit. The parent Agent no longer carries every tool directly; it sees a stable capability entrypoint.

**Assemble per call.** Dynamic AgentTool is appropriate when each subtask needs a different narrower capability set. Code should define the capability pool, and the model should select a subset for the current invocation. Do not use it as a long-lived child Agent, and do not rebuild the parent Agent's tool schema on every model request. Frequent tool-schema and instruction changes usually reduce prompt-cache stability and make behavior harder to reproduce.

A practical rule: use filtering for low-frequency stable narrowing; use fixed child Agents for stable division of work; use Dynamic AgentTool only when the boundary changes per task.

## 7. Output and Observability: Keep Four Exits Separate

After introducing a child Agent, four exits are often mixed together:

| Exit | Question |
| --- | --- |
| What the parent receives | Which conclusion, evidence, assumptions, next step, and artifact references does it need? |
| What the user sees | Should inner stream, progress, member output, or final answer be displayed? |
| What trace / artifact keeps | Which process details are needed for debugging, audit, replay, or large-result storage? |
| What later `messages` include | Which history, tool result, or foreign-Agent context enters the next model request? |

Hiding inner process from the UI does not mean the parent does not need a result. Keeping the full process in traces does not mean it should be placed back into prompts. Skipping parent summarization does not remove the need for a good tool-result contract.

A robust child result contract usually includes:

- Conclusion: what was done and what the judgment is.
- Evidence: where the judgment came from and whether it is traceable.
- Assumptions and uncertainty: what was not verified and what may be wrong.
- Key actions: important searches, checks, or edits without replaying the whole process.
- Next step: whether to call another tool, ask the user, or finalize.
- Artifact reference: keep large files, reports, and task state out of the prompt and pass a handle.

## 8. Cases Where You Should Not Split Yet

Good selection also means knowing when not to split.

| Situation | More Stable Choice |
| --- | --- |
| You only need to adjust instructions or tool descriptions | Improve the single Agent instruction, tool descriptions, or planner |
| Input and output are fixed and one call is enough | Wrap it as a direct tool instead of adding an Agent layer |
| The process is fixed, such as plan -> execute -> review | Chain, Cycle, or Graph is often clearer than autonomous collaboration |
| The parent needs key process information to decide | Do not over-isolate; return evidence, summary, or artifact references |
| Only tool visibility differs | Use tool filter for stable visibility and permission policy for execution |
| Trace, UI, and state management are not ready | Avoid complex async or long-lived child Agents |

Parallelism and async execution can reduce end-to-end latency, but speed should not be the default reason to split Agents. In many systems, users need stable quality, clean context, and reliable tool selection more than they need a larger cast of roles.

## Closing

Call-and-return delegation, control handoff, cross-turn entrypoint ownership, run lifecycle, remote runtime ownership, UI / trace visibility, and `messages` projection are different boundaries.

Do not ask which API "looks more multi-agent." Ask which boundary this work needs to move across. If the parent context is noisy, manage context. If the capability surface is too broad, narrow capabilities. If the current turn should be owned by another Agent, use transfer or Swarm. If the task outlives the current invocation, use TaskRun. If the process is fixed, let code, Chain, Cycle, or Graph carry the skeleton.

Once the boundary is clear, the API choice becomes much less ambiguous.

## References

- [tRPC-Agent-Go GitHub](https://github.com/trpc-group/trpc-agent-go)
- [Multi-Agent documentation](../multiagent.md)
- [Team documentation](../team.md)
- [TaskRun documentation](../taskrun.md)
- [Graph documentation](../graph.md)
- [A2A documentation](../a2a.md)
- [Tool documentation](../tool.md)
