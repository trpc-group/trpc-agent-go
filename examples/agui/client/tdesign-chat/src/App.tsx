import { useEffect, useMemo, useRef, useState, type FC } from "react";
import { ChatMarkdown, ChatMessage, ToolCallRenderer, agentToolcallRegistry, type ToolCall } from "@tdesign-react/chat";
import {
  Alert,
  Button,
  Divider,
  Drawer,
  Input,
  Layout,
  Progress,
  Space,
  Tag,
  Textarea,
} from "tdesign-react";
import { ChatIcon, RefreshIcon } from "tdesign-icons-react";
import { formatTimestamp } from "./agui/format";
import { useAguiChat, type RawAguiEvent, type UiMessage } from "./hooks/useAguiChat";

const AGUI_OPEN_REPORT_EVENT = "agui-open-report";
const AGUI_GRAPH_APPROVAL_EVENT = "agui-graph-approval";
const REPORT_OPEN_TOOL_NAME = "open_report_sidebar";
const GRAPH_APPROVAL_TOOL_NAME = "graph_interrupt_approval";
const DEFAULT_INPUT_MESSAGE = "计算123+456";

function createThreadId(): string {
  if (typeof crypto !== "undefined") {
    if (typeof crypto.randomUUID === "function") {
      return crypto.randomUUID().replace(/-/g, "").slice(0, 12);
    }
    if (typeof crypto.getRandomValues === "function") {
      const bytes = new Uint8Array(6);
      crypto.getRandomValues(bytes);
      return Array.from(bytes, (byte) => byte.toString(16).padStart(2, "0")).join("");
    }
  }
  const rand = Math.random().toString(16).slice(2).padEnd(12, "0");
  return rand.slice(0, 12);
}

function isHttpUrl(value: string): boolean {
  return /^https?:\/\//i.test(value.trim());
}

function isLoopbackHost(hostname: string): boolean {
  const normalized = hostname.trim().toLowerCase();
  return normalized === "localhost" || normalized === "127.0.0.1";
}

function parseHostname(address: string): string {
  const trimmed = address.trim();
  if (!trimmed) {
    return "";
  }
  try {
    return new URL(trimmed).hostname;
  } catch {}
  try {
    return new URL(`http://${trimmed}`).hostname;
  } catch {}
  return "";
}

function shouldUseDevProxy(base: string): boolean {
  if (!import.meta.env.DEV) {
    return false;
  }
  if (typeof window === "undefined") {
    return false;
  }
  const baseHost = parseHostname(base);
  if (!isLoopbackHost(baseHost)) {
    return false;
  }
  return !isLoopbackHost(window.location.hostname);
}

function normalizePath(path: string): string {
  const trimmed = path.trim();
  if (!trimmed) {
    return "/";
  }
  if (trimmed.startsWith("/")) {
    return trimmed;
  }
  return `/${trimmed}`;
}

function buildHttpUrl(base: string, pathOrUrl: string): string {
  const trimmed = pathOrUrl.trim();
  if (!trimmed) {
    return "";
  }
  if (isHttpUrl(trimmed)) {
    if (shouldUseDevProxy(trimmed)) {
      try {
        const url = new URL(trimmed);
        return normalizePath(url.pathname + url.search);
      } catch {
        return trimmed;
      }
    }
    return trimmed;
  }
  if (shouldUseDevProxy(base)) {
    return normalizePath(trimmed);
  }
  const baseTrimmed = base.trim() || "127.0.0.1:8080";
  const baseUrl = isHttpUrl(baseTrimmed) ? baseTrimmed : `http://${baseTrimmed}`;
  return new URL(normalizePath(trimmed), baseUrl).toString();
}

type ToolcallStatus = "idle" | "executing" | "complete" | "error";
type ToolcallComponentProps = {
  status: ToolcallStatus;
  args: Record<string, unknown>;
  result?: unknown;
  error?: Error;
  respond?: (response: unknown) => void;
  agentState?: Record<string, unknown>;
};

function formatToolcallValue(value: unknown): string {
  if (value === null || value === undefined) {
    return "";
  }
  if (typeof value === "string") {
    return value.trim();
  }
  try {
    return JSON.stringify(value, null, 2);
  } catch {
    return String(value);
  }
}

function toolcallStatusLabel(status: ToolcallStatus): string {
  if (status === "executing") {
    return "Running";
  }
  if (status === "complete") {
    return "Done";
  }
  if (status === "error") {
    return "Error";
  }
  return "Idle";
}

function toolcallStatusTheme(status: ToolcallStatus): "default" | "primary" | "success" | "danger" {
  if (status === "executing") {
    return "primary";
  }
  if (status === "complete") {
    return "success";
  }
  if (status === "error") {
    return "danger";
  }
  return "default";
}

function reportStatusTheme(status: string): "warning" | "success" | "default" {
  if (status === "open") {
    return "warning";
  }
  if (status === "closed") {
    return "success";
  }
  return "default";
}

function reportStatusLabel(status: string): string {
  if (status === "open") {
    return "生成中";
  }
  if (status === "closed") {
    return "已完成";
  }
  return status || "Unknown";
}

const toolcallComponentCache = new Map<string, FC<ToolcallComponentProps>>();

function extractReportDocumentId(result: unknown): string {
  if (!result || typeof result !== "object") {
    return "";
  }
  const anyResult = result as any;
  const documentId = typeof anyResult.documentId === "string"
    ? anyResult.documentId
    : typeof anyResult.documentID === "string"
      ? anyResult.documentID
      : "";
  return documentId.trim();
}

function extractReportTitle(args: Record<string, unknown>, result: unknown): string {
  if (result && typeof result === "object" && typeof (result as any).title === "string") {
    return String((result as any).title).trim() || "Report";
  }
  if (typeof args?.title === "string") {
    return String(args.title).trim() || "Report";
  }
  return "Report";
}

function getGenericToolcallComponent(toolCallName: string): FC<ToolcallComponentProps> {
  const cached = toolcallComponentCache.get(toolCallName);
  if (cached) {
    return cached;
  }
  const Component: FC<ToolcallComponentProps> = ({ status, args, result, error }) => {
    const argsText = formatToolcallValue(args);
    const resultText = formatToolcallValue(result);
    const errorText = error ? String(error.message || error) : "";
    const hasResult = result !== undefined;

    return (
      <details className="toolcall">
        <summary className="toolcall__summary">
          <span className="toolcall__summary-title">{toolCallName}</span>
        </summary>
        <div className="toolcall__body">
          <Space size="small" align="center" breakLine>
            <Tag theme={toolcallStatusTheme(status)} variant="outline">{toolcallStatusLabel(status)}</Tag>
          </Space>
          <Divider style={{ margin: "10px 0" }} />
          <div className="toolcall__panels">
            <details className="toolcall__panel">
              <summary className="toolcall__panel-summary">
                <span className="toolcall__panel-title">工具调用</span>
              </summary>
              <pre className="toolcall__code">{argsText || "(empty)"}</pre>
            </details>
            <details className="toolcall__panel">
              <summary className="toolcall__panel-summary">
                <span className="toolcall__panel-title">工具结果</span>
              </summary>
              {status === "error" ? (
                <pre className="toolcall__code">{errorText || "(unknown error)"}</pre>
              ) : (
                <pre className="toolcall__code">{hasResult ? resultText || "(empty)" : "等待结果..."}</pre>
              )}
            </details>
          </div>
        </div>
      </details>
    );
  };
  toolcallComponentCache.set(toolCallName, Component);
  return Component;
}

function ensureToolcallRegistered(toolCallName: string) {
  if (!toolCallName) {
    return;
  }
  if (agentToolcallRegistry.get(toolCallName)) {
    return;
  }
  agentToolcallRegistry.register({
    name: toolCallName,
    description: `AG-UI tool call: ${toolCallName}.`,
    component: getGenericToolcallComponent(toolCallName),
  });
}

ensureToolcallRegistered("calculator");
ensureToolcallRegistered("open_report_document");
ensureToolcallRegistered("close_report_document");

agentToolcallRegistry.register({
  name: REPORT_OPEN_TOOL_NAME,
  description: "Open a report sidebar in the AG-UI frontend.",
  component: ({ args, result }) => {
    const documentId = extractReportDocumentId(result);
    const title = extractReportTitle(args, result);
    const reportStatus = result && typeof result === "object" && typeof (result as any).status === "string"
      ? String((result as any).status)
      : "open";
    const createdAt = result && typeof result === "object" && typeof (result as any).createdAt === "string"
      ? String((result as any).createdAt)
      : "";
    const closedAt = result && typeof result === "object" && typeof (result as any).closedAt === "string"
      ? String((result as any).closedAt)
      : "";
    const reason = result && typeof result === "object" && typeof (result as any).reason === "string"
      ? String((result as any).reason)
      : "";

    return (
      <div className="toolcall">
        <div className="toolcall__summary">
          <span className="toolcall__summary-title">打开报告</span>
        </div>
        <div className="toolcall__body">
          <Space size="small" align="center" breakLine>
            <Tag theme={reportStatusTheme(reportStatus)} variant="outline">{reportStatusLabel(reportStatus)}</Tag>
            <Button
              size="small"
              theme="primary"
              disabled={!documentId}
              onClick={() => {
                if (!documentId) {
                  return;
                }
                window.dispatchEvent(new CustomEvent(AGUI_OPEN_REPORT_EVENT, { detail: { documentId } }));
              }}
            >
              打开报告
            </Button>
          </Space>
          <Divider style={{ margin: "10px 0" }} />
          <Space direction="vertical" size="small" style={{ width: "100%" }}>
            <div>
              <Tag theme="default" variant="outline">Title</Tag>{" "}
              <span>{title}</span>
            </div>
            {documentId ? (
              <div>
                <Tag theme="default" variant="outline">docId</Tag>{" "}
                <span>{documentId}</span>
              </div>
            ) : null}
            {createdAt ? (
              <div>
                <Tag theme="default" variant="outline">createdAt</Tag>{" "}
                <span>{createdAt}</span>
              </div>
            ) : null}
            {closedAt ? (
              <div>
                <Tag theme="default" variant="outline">closedAt</Tag>{" "}
                <span>{closedAt}</span>
              </div>
            ) : null}
            {reason ? (
              <div>
                <Tag theme="default" variant="outline">reason</Tag>{" "}
                <span>{reason}</span>
              </div>
            ) : null}
          </Space>
        </div>
      </div>
    );
  },
});

agentToolcallRegistry.register({
  name: GRAPH_APPROVAL_TOOL_NAME,
  description: "Approve an AG-UI graph interrupt in the frontend.",
  component: ({ args }) => {
    const prompt = typeof (args as any)?.prompt === "string" ? String((args as any).prompt) : "";
    const decision = typeof (args as any)?.decision === "string" ? String((args as any).decision) : "pending";
    const decided = decision === "approve" || decision === "dismiss";
    const alertTheme: "success" | "warning" | "error" = decision === "approve" ? "success" : decision === "dismiss" ? "error" : "warning";
    const decisionLabel = decision === "approve" ? "已选择：允许" : decision === "dismiss" ? "已选择：拒绝" : "";
    const decisionTagTheme: "success" | "danger" = decision === "approve" ? "success" : "danger";

    return (
      <div className="toolcall">
        <Alert
          theme={alertTheme}
          title="审批（人机交互）"
          message={prompt ? <div style={{ whiteSpace: "pre-wrap" }}>{prompt}</div> : undefined}
          operation={(
            decided ? (
              <Tag theme={decisionTagTheme} variant="outline">{decisionLabel}</Tag>
            ) : (
              <Space size="small">
                <Button
                  size="small"
                  theme="primary"
                  onClick={() => window.dispatchEvent(new CustomEvent(AGUI_GRAPH_APPROVAL_EVENT, { detail: { action: "approve" } }))}
                >
                  允许
                </Button>
                <Button
                  size="small"
                  variant="outline"
                  theme="danger"
                  onClick={() => window.dispatchEvent(new CustomEvent(AGUI_GRAPH_APPROVAL_EVENT, { detail: { action: "dismiss" } }))}
                >
                  拒绝
                </Button>
              </Space>
            )
          )}
        />
      </div>
    );
  },
});

function summarizeRawEvent(event: RawAguiEvent): string {
  const payload = event.payload as any;
  if (event.type === "CUSTOM") {
    const name = typeof payload?.name === "string" ? payload.name : "";
    return name ? `name=${name}` : "";
  }
  if (event.type === "ACTIVITY_DELTA") {
    const activityType = typeof payload?.activityType === "string" ? payload.activityType : "";
    return activityType ? `activityType=${activityType}` : "";
  }
  if (event.type === "TEXT_MESSAGE_START") {
    const id = typeof payload?.messageId === "string" ? payload.messageId : "";
    return id ? `messageId=${id}` : "";
  }
  if (event.type === "TEXT_MESSAGE_CONTENT") {
    const delta = typeof payload?.delta === "string" ? payload.delta : "";
    const trimmed = delta.replace(/\s+/g, " ").trim();
    if (!trimmed) {
      return "";
    }
    const short = trimmed.length > 42 ? `${trimmed.slice(0, 42)}…` : trimmed;
    return `delta=${short}`;
  }
  if (event.type === "TOOL_CALL_START") {
    const tool = typeof payload?.toolCallName === "string" ? payload.toolCallName : "";
    return tool ? `tool=${tool}` : "";
  }
  if (event.type === "TOOL_CALL_ARGS") {
    const delta = typeof payload?.delta === "string" ? payload.delta : "";
    const trimmed = delta.replace(/\s+/g, " ").trim();
    if (!trimmed) {
      return "";
    }
    const short = trimmed.length > 42 ? `${trimmed.slice(0, 42)}…` : trimmed;
    return `args=${short}`;
  }
  if (event.type === "TOOL_CALL_RESULT") {
    const id = typeof payload?.toolCallId === "string" ? payload.toolCallId : "";
    return id ? `toolCallId=${id}` : "";
  }
  if (event.type === "RUN_FINISHED") {
    const result = typeof payload?.result === "string" ? payload.result : "";
    return result ? `result=${result}` : "";
  }
  return "";
}

function isVisibleMainMessage(message: UiMessage): boolean {
  if (message.kind === "thinking") {
    return true;
  }
  if (message.kind === "tool-call") {
    return true;
  }
  if (message.kind === "text" && (message.role === "user" || message.role === "assistant")) {
    return true;
  }
  return false;
}

type RenderedChatItem =
  | {
      kind: "user";
      key: string;
      message: UiMessage;
    }
  | {
      kind: "assistant";
      key: string;
      messages: UiMessage[];
    };

function groupChatItems(messages: UiMessage[]): RenderedChatItem[] {
  const items: RenderedChatItem[] = [];
  let assistantGroup: UiMessage[] = [];

  const flushAssistantGroup = () => {
    if (assistantGroup.length === 0) {
      return;
    }
    items.push({
      kind: "assistant",
      key: `assistant-${assistantGroup[0]?.id ?? "unknown"}`,
      messages: assistantGroup,
    });
    assistantGroup = [];
  };

  for (const message of messages) {
    if (message.kind === "text" && message.role === "user") {
      flushAssistantGroup();
      items.push({ kind: "user", key: `user-${message.id}`, message });
      continue;
    }
    assistantGroup.push(message);
  }

  flushAssistantGroup();
  return items;
}

export default function App() {
  const [serverAddress, setServerAddress] = useState<string>("127.0.0.1:8080");
  const [serverAddressDraft, setServerAddressDraft] = useState<string>("127.0.0.1:8080");
  const [endpointPath, setEndpointPath] = useState<string>("/agui");
  const [endpointPathDraft, setEndpointPathDraft] = useState<string>("/agui");
  const [historyPathDraft, setHistoryPathDraft] = useState<string>("/history");
  const [historyHint, setHistoryHint] = useState<string>("");
  const [userId, setUserId] = useState<string>("demo-user");
  const initialThreadId = useMemo(() => createThreadId(), []);
  const [threadId, setThreadId] = useState<string>(initialThreadId);
  const [threadIdDraft, setThreadIdDraft] = useState<string>(initialThreadId);
  const [input, setInput] = useState<string>(DEFAULT_INPUT_MESSAGE);
  const [isComposing, setIsComposing] = useState(false);
  const [errorDrawerOpen, setErrorDrawerOpen] = useState(false);
  const [dismissedError, setDismissedError] = useState<string | null>(null);

  const forwardedProps = useMemo(() => ({ userId }), [userId]);
  const endpoint = useMemo(() => buildHttpUrl(serverAddress, endpointPath), [endpointPath, serverAddress]);

  const chat = useAguiChat({
    endpoint,
    threadId,
    forwardedProps,
  });

  const activeReport = useMemo(() => {
    if (!chat.activeReportId) {
      return null;
    }
    return chat.reportSessions.find((session) => session.documentId === chat.activeReportId) ?? null;
  }, [chat.activeReportId, chat.reportSessions]);

  useEffect(() => {
    const handler = (event: Event) => {
      const detail = (event as CustomEvent).detail as any;
      const documentId = typeof detail?.documentId === "string" ? detail.documentId : "";
      if (!documentId) {
        return;
      }
      chat.openReport(documentId);
    };
    window.addEventListener(AGUI_OPEN_REPORT_EVENT, handler as EventListener);
    return () => {
      window.removeEventListener(AGUI_OPEN_REPORT_EVENT, handler as EventListener);
    };
  }, [chat.openReport]);

  useEffect(() => {
    const handler = (event: Event) => {
      const detail = (event as CustomEvent).detail as any;
      const action = typeof detail?.action === "string" ? detail.action : "";
      if (action === "approve") {
        void chat.approveGraphInterrupt();
        return;
      }
      if (action === "dismiss") {
        void chat.dismissGraphInterrupt();
      }
    };
    window.addEventListener(AGUI_GRAPH_APPROVAL_EVENT, handler as EventListener);
    return () => {
      window.removeEventListener(AGUI_GRAPH_APPROVAL_EVENT, handler as EventListener);
    };
  }, [chat.approveGraphInterrupt, chat.dismissGraphInterrupt]);

  const send = async () => {
    if (chat.inProgress) {
      return;
    }
    const text = input.trim();
    if (!text) {
      return;
    }
    setInput("");
    await chat.send(text);
  };

  const canSend = !chat.inProgress;

  const chatRef = useRef<HTMLDivElement | null>(null);
  const [shouldAutoScroll, setShouldAutoScroll] = useState(true);
  const rawRef = useRef<HTMLDivElement | null>(null);
  const [rawShouldAutoScroll, setRawShouldAutoScroll] = useState(true);

  useEffect(() => {
    if (!rawShouldAutoScroll) {
      return;
    }
    const container = rawRef.current;
    if (!container) {
      return;
    }
    const id = window.requestAnimationFrame(() => {
      container.scrollTop = container.scrollHeight;
    });
    return () => window.cancelAnimationFrame(id);
  }, [chat.rawEvents, rawShouldAutoScroll]);

  const visibleMessages = useMemo(() => chat.messages.filter(isVisibleMainMessage), [chat.messages]);

  useEffect(() => {
    if (!shouldAutoScroll) {
      return;
    }
    const container = chatRef.current;
    if (!container) {
      return;
    }
    const id = window.requestAnimationFrame(() => {
      container.scrollTop = container.scrollHeight;
    });
    return () => window.cancelAnimationFrame(id);
  }, [visibleMessages, shouldAutoScroll]);

  const renderedItems = useMemo(() => groupChatItems(visibleMessages), [visibleMessages]);

  useEffect(() => {
    if (!chat.lastError) {
      setDismissedError(null);
      setErrorDrawerOpen(false);
    }
  }, [chat.lastError]);

  const errorSummary = useMemo(() => {
    const error = chat.lastError || "";
    if (!error) {
      return "";
    }
    const firstLine = error.split(/\r?\n/)[0] ?? error;
    const trimmed = firstLine.trim() || error.trim();
    if (!trimmed) {
      return "";
    }
    return trimmed.length > 140 ? `${trimmed.slice(0, 140)}…` : trimmed;
  }, [chat.lastError]);

  const showErrorBanner = Boolean(chat.lastError) && dismissedError !== chat.lastError;

  const applyConnection = async () => {
    if (chat.inProgress) {
      chat.stop();
    }
    chat.reset();
    setInput(DEFAULT_INPUT_MESSAGE);

    const nextServerAddress = serverAddressDraft.trim() || "127.0.0.1:8080";
    const nextEndpointPath = endpointPathDraft.trim() || "/agui";
    const nextHistoryPath = historyPathDraft.trim() || "/history";
    const nextThreadId = threadIdDraft.trim() || createThreadId();

    setServerAddress(nextServerAddress);
    setServerAddressDraft(nextServerAddress);
    setEndpointPath(nextEndpointPath);
    setEndpointPathDraft(nextEndpointPath);
    setHistoryPathDraft(nextHistoryPath);
    setThreadId(nextThreadId);
    setThreadIdDraft(nextThreadId);

    setHistoryHint("正在载入历史...");
    const result = await chat.loadHistory({
      endpoint: buildHttpUrl(nextServerAddress, nextHistoryPath),
      threadId: nextThreadId,
      forwardedProps,
    });
    if (result.ok) {
      setHistoryHint(result.count > 0 ? "" : "暂无历史记录");
      return;
    }
    const message = result.message || "";
    if (message.includes("session not found")) {
      setHistoryHint("暂无历史记录");
      return;
    }
    setHistoryHint("");
  };

  const toolcallNames = useMemo(() => {
    const names = new Set<string>();
    for (const message of visibleMessages) {
      if (message.kind === "tool-call" && message.toolCall?.toolCallName) {
        names.add(message.toolCall.toolCallName);
      }
    }
    return Array.from(names).sort();
  }, [visibleMessages]);

  useEffect(() => {
    for (const name of toolcallNames) {
      ensureToolcallRegistered(name);
    }
  }, [toolcallNames]);

  return (
    <Layout className="app">
      <Layout.Content className="app__content">
        <div className="app__body">
          <aside className="app__sider">
            <div className="app__sider-header">
              <Space size="small" align="center">
                <strong>AG-UI 事件</strong>
                <Tag theme="default" variant="outline">{chat.rawEvents.length}</Tag>
              </Space>
              <Space size="small">
                <Button size="small" variant="outline" onClick={chat.clearRawEvents}>
                  清空
                </Button>
              </Space>
            </div>
            <div
              className="app__sider-content"
              ref={rawRef}
              onScroll={() => {
                const el = rawRef.current;
                if (!el) {
                  return;
                }
                const distance = el.scrollHeight - el.scrollTop - el.clientHeight;
                setRawShouldAutoScroll(distance < 80);
              }}
            >
              {chat.rawEvents.length === 0 ? (
                <div className="raw-events__empty">等待事件...</div>
              ) : (
                <div className="raw-events">
                  {chat.rawEvents.map((event) => (
                    <details key={event.id} className="raw-event">
                      <summary className="raw-event__summary">
                        <span className="raw-event__time">{formatTimestamp(event.timestamp)}</span>
                        <Tag theme="default" variant="outline">{event.type}</Tag>
                        <span className="raw-event__extra">{summarizeRawEvent(event)}</span>
                      </summary>
                      <pre className="raw-event__body">
                        {JSON.stringify(event.payload, null, 2)}
                      </pre>
                    </details>
                  ))}
                </div>
              )}
            </div>
          </aside>

          <div className="app__chat-area">
	            <div className="app__topbar">
	              <div className="status-row">
                <div className="status-row__left">
                  <Space size="small" align="center">
                    <ChatIcon />
                    <strong>AG-UI TDesign Chat</strong>
                  </Space>
                  {chat.graphNodeId ? <Tag theme="warning" variant="outline">node: {chat.graphNodeId}</Tag> : null}
                  {chat.finishReason && chat.finishReason !== "stop"
                    ? <Tag theme="success" variant="outline">finish: {chat.finishReason}</Tag>
                    : null}
                </div>

                <div className="status-row__center">
                  <Input
                    label="IP:Port"
                    value={serverAddressDraft}
                    onChange={(v) => {
                      const next = String(v);
                      setServerAddressDraft(next);
                      setServerAddress(next);
                    }}
                    className="header-field"
                    style={{ width: "100%" }}
                    placeholder="127.0.0.1:8080"
                  />

                  <Input
                    label="实时对话"
                    value={endpointPathDraft}
                    onChange={(v) => {
                      const next = String(v);
                      setEndpointPathDraft(next);
                      setEndpointPath(next);
                    }}
                    className="header-field"
                    style={{ width: "100%" }}
                    placeholder="/agui 或 完整URL"
                  />

                  <Input
                    label="消息快照"
                    value={historyPathDraft}
                    onChange={(v) => setHistoryPathDraft(String(v))}
                    className="header-field"
                    style={{ width: "100%" }}
                    placeholder="/history"
                  />

                  <Input
                    label="Thread"
                    value={threadIdDraft}
                    onChange={(v) => {
                      const next = String(v);
                      setThreadIdDraft(next);
                      if (next.trim()) {
                        setThreadId(next);
                      }
                    }}
                    className="header-field"
                    style={{ width: "100%" }}
                    placeholder="留空自动生成"
                  />

                  <Input
                    label="User"
                    value={userId}
                    onChange={(v) => setUserId(String(v))}
                    className="header-field"
                    style={{ width: "100%" }}
                    placeholder="demo-user"
                  />
                </div>

                <div className="status-row__right">
                  {chat.progress ? (
                    <Progress
                      theme="line"
                      percentage={Math.max(0, Math.min(100, Math.round(chat.progress.percent)))}
                      label={chat.progress.label ? chat.progress.label : undefined}
                      style={{ width: 200 }}
                    />
                  ) : null}

                  {historyHint ? (
                    <span className="status-row__hint" title={historyHint}>
                      <Tag theme="default" variant="outline">{historyHint}</Tag>
                    </span>
                  ) : null}

                  <Button variant="outline" onClick={applyConnection} disabled={chat.inProgress}>
                    载入历史
                  </Button>

                  <Button
                    variant="outline"
                    icon={<RefreshIcon />}
                    onClick={() => {
                      chat.reset();
                      const nextThreadId = createThreadId();
                      setThreadId(nextThreadId);
                      setThreadIdDraft(nextThreadId);
                      setHistoryHint("");
                      setInput(DEFAULT_INPUT_MESSAGE);
                    }}
                  >
                    新会话
                  </Button>
                </div>
              </div>
            </div>

            <div
              className="chat"
              ref={chatRef}
              onScroll={() => {
                const el = chatRef.current;
                if (!el) {
                  return;
                }
                const distance = el.scrollHeight - el.scrollTop - el.clientHeight;
                setShouldAutoScroll(distance < 80);
              }}
            >
              {visibleMessages.length === 0 ? (
                <div className="chat__empty">
                  <div>输入内容后发送。</div>
                </div>
              ) : (
                <div className="chat__list">
                  {renderedItems.map((item) => {
                    if (item.kind === "user") {
                      return (
                        <ChatMessage
                          key={item.key}
                          role="user"
                          name="You"
                          placement="right"
                          content={[{ type: "text", data: item.message.content || "" }] as any}
                          variant="base"
                        />
                      );
                    }

                    const blocks: any[] = [];
                    const toolSlots: JSX.Element[] = [];

                    for (const message of item.messages) {
                      if (message.kind === "thinking") {
                        blocks.push({
                          type: "thinking",
                          data: { title: message.title ?? "Thinking", text: message.content || "" },
                          status: message.status,
                        });
                        continue;
                      }

                      if (message.kind === "tool-call" && message.toolCall) {
                        const toolCall: ToolCall = {
                          toolCallId: message.toolCall.toolCallId,
                          toolCallName: message.toolCall.toolCallName,
                          parentMessageId: message.toolCall.parentMessageId,
                          args: message.toolCall.args,
                          result: message.toolCall.result,
                        };
                        const index = blocks.length;
                        blocks.push({ type: "toolcall", data: toolCall });
                        toolSlots.push(
                          <div key={toolCall.toolCallId} slot={`toolcall-${index}`} className="toolcall__slot">
                            <ToolCallRenderer toolCall={toolCall} />
                          </div>,
                        );
                        continue;
                      }

                      if (message.kind === "text" && message.role === "assistant") {
                        blocks.push({ type: "markdown", data: message.content || "" });
                      }
                    }

                    return (
                      <ChatMessage
                        key={item.key}
                        role="assistant"
                        name="Assistant"
                        placement="left"
                        content={blocks as any}
                        variant="base"
                        chatContentProps={{
                          thinking: {
                            maxHeight: 320,
                            layout: "border",
                            collapsed: false,
                          },
                        }}
                      >
                        {toolSlots}
                      </ChatMessage>
                    );
                  })}
                </div>
              )}
            </div>

            <div className="composer">
              <Space direction="vertical" style={{ width: "100%" }} size="small">
                {showErrorBanner ? (
                  <Alert
                    theme="error"
                    title="运行失败"
                    message={errorSummary || "发生错误，点击“详情”查看。"}
                    closeBtn
                    onClose={() => {
                      setDismissedError(chat.lastError);
                      setErrorDrawerOpen(false);
                    }}
                    operation={(
                      <Button
                        size="small"
                        variant="outline"
                        onClick={() => setErrorDrawerOpen(true)}
                      >
                        详情
                      </Button>
                    )}
                  />
                ) : null}
                <div style={{ display: "flex", gap: 8, alignItems: "center" }}>
                  <div style={{ flex: 1 }}>
                    <Textarea
                      value={input}
                      onChange={(v) => setInput(String(v))}
                      placeholder="输入消息..."
                      autosize={{ minRows: 2, maxRows: 6 }}
                      disabled={!canSend}
                      onCompositionStart={() => setIsComposing(true)}
                      onCompositionEnd={() => setIsComposing(false)}
                      onKeydown={(_, ctx) => {
                        const e = ctx.e;
                        if (e.key !== "Enter") {
                          return;
                        }
                        if (e.shiftKey) {
                          return;
                        }
                        if (isComposing) {
                          return;
                        }
                        e.preventDefault();
                        void send();
                      }}
                    />
                  </div>
                  <Button theme="primary" onClick={send} disabled={!canSend}>
                    发送
                  </Button>
                </div>
              </Space>
            </div>
          </div>
        </div>
      </Layout.Content>

      <Drawer
        header={activeReport ? `Report · ${activeReport.title}` : "Report"}
        visible={chat.reportDrawerOpen}
        onClose={() => chat.setReportOpen(false)}
        closeBtn={false}
        footer={(
          <div className="report-drawer__footer">
            <Button theme="primary" onClick={() => chat.setReportOpen(false)}>
              关闭
            </Button>
          </div>
        )}
        size="480px"
      >
        {activeReport ? (
          <div className="report-drawer__content">
            <Space direction="vertical" style={{ width: "100%" }} size="small">
              <Space size="small" align="center">
                <Tag theme={activeReport.status === "open" ? "warning" : "success"} variant="outline">
                  {activeReport.status === "open" ? "生成中" : "已完成"}
                </Tag>
                <Tag theme="default" variant="outline">docId: {activeReport.documentId}</Tag>
                {activeReport.closedAt ? (
                  <Tag theme="default" variant="outline">closedAt: {activeReport.closedAt}</Tag>
                ) : null}
              </Space>
              {activeReport.reason ? (
                <div>
                  <Tag theme="default" variant="outline">reason</Tag>{" "}
                  <span>{activeReport.reason}</span>
                </div>
              ) : null}
              <Divider style={{ margin: "8px 0" }} />
              <ChatMarkdown content={activeReport.content || "Waiting for report content..."} />
            </Space>
          </div>
        ) : (
          <div>Waiting for report document...</div>
        )}
      </Drawer>

      <Drawer
        header="错误详情"
        visible={errorDrawerOpen}
        onClose={() => setErrorDrawerOpen(false)}
        closeBtn={false}
        footer={(
          <div className="report-drawer__footer">
            <Button theme="primary" onClick={() => setErrorDrawerOpen(false)}>
              关闭
            </Button>
          </div>
        )}
        size="520px"
      >
        <pre className="toolcall__code">{chat.lastError || "暂无错误信息。"}</pre>
      </Drawer>
    </Layout>
  );
}
