import { useCallback, useRef, useState } from "react";
import { formatStructured, safeJsonParse } from "../agui/format";
import { streamAguiSse, type AguiSseEvent } from "../agui/sse";

export type UiMessageRole = "user" | "assistant" | "tool" | "system";

export type UiMessageKind =
  | "text"
  | "thinking"
  | "tool-call"
  | "tool-result"
  | "custom"
  | "system";

export type UiMessage = {
  id: string;
  role: UiMessageRole;
  kind: UiMessageKind;
  title?: string;
  content: string;
  status?: "pending" | "streaming" | "complete" | "stop" | "error";
  toolCall?: {
    toolCallId: string;
    toolCallName: string;
    parentMessageId?: string;
    args?: string;
    result?: string;
  };
  timestamp: number;
};

export type RawAguiEvent = {
  id: string;
  kind: "event" | "request";
  type: string;
  timestamp: number;
  payload: unknown;
};

type GraphInterrupt = {
  key: string;
  prompt: string;
  checkpointId?: string;
  lineageId?: string;
};

export type ReportSession = {
  status: "open" | "closed";
  title: string;
  documentId: string;
  createdAt?: string;
  closedAt?: string;
  reason?: string;
  content: string;
};

export type AguiChatConfig = {
  endpoint: string;
  threadId: string;
  forwardedProps?: Record<string, unknown>;
};

export type HistoryLoadResult =
  | { ok: true; count: number }
  | { ok: false; message: string };

const REPORT_OPEN_TOOL_NAME = "open_report_sidebar";
const GRAPH_APPROVAL_TOOL_NAME = "graph_interrupt_approval";

function randomId(prefix: string) {
  const now = Date.now();
  const rand = Math.random().toString(16).slice(2);
  return `${prefix}_${now}_${rand}`;
}

function sanitizeIdPart(value: string, maxLength = 96): string {
  return value.replace(/[^a-zA-Z0-9_-]/g, "_").slice(0, maxLength);
}

function graphInterruptIdentity(interrupt: GraphInterrupt): string {
  return [interrupt.checkpointId ?? "", interrupt.lineageId ?? "", interrupt.key ?? ""].filter(Boolean).join("|");
}

function looksLikeToolCallId(prompt: string): boolean {
  const normalized = prompt.trim();
  if (!normalized) {
    return false;
  }
  return /^call_[a-zA-Z0-9_-]+$/.test(normalized);
}

function shouldSuppressGraphApproval(interrupt: GraphInterrupt, toolCallNameById: Map<string, string>): boolean {
  if (interrupt.key === "external_tool") {
    return true;
  }
  const normalized = interrupt.prompt.trim();
  if (!normalized) {
    return false;
  }
  if (toolCallNameById.has(normalized)) {
    return true;
  }
  return looksLikeToolCallId(normalized);
}

function isSessionNotFoundError(message: string): boolean {
  return /session not found/i.test(message);
}

function normalizeRole(value: unknown): UiMessageRole {
  if (value === "user" || value === "assistant" || value === "tool" || value === "system") {
    return value;
  }
  return "assistant";
}

function extractActivityValue(value: unknown): Record<string, any> | null {
  if (!value || typeof value !== "object") {
    return null;
  }
  return value as Record<string, any>;
}

function applyJsonPatch(root: Record<string, any>, ops: any[]) {
  for (const op of ops) {
    const operation = op?.op;
    const path = typeof op?.path === "string" ? op.path : "";
    const value = op?.value;
    if (!path.startsWith("/")) {
      continue;
    }
    const segments = path
      .split("/")
      .slice(1)
      .filter(Boolean)
      .map((seg: string) => seg.replace(/~1/g, "/").replace(/~0/g, "~"));
    if (segments.length === 0) {
      continue;
    }
    let target: any = root;
    for (let i = 0; i < segments.length - 1; i += 1) {
      const key = segments[i];
      if (!target[key] || typeof target[key] !== "object") {
        target[key] = {};
      }
      target = target[key];
    }
    const last = segments[segments.length - 1];
    if (operation === "remove") {
      delete target[last];
      continue;
    }
    if (operation === "add" || operation === "replace") {
      target[last] = value;
    }
  }
}

function normalizeJsonString(raw: string): string | undefined {
  const trimmed = raw.trim();
  if (!trimmed) {
    return undefined;
  }
  try {
    JSON.parse(trimmed);
    return trimmed;
  } catch {
    return JSON.stringify(trimmed);
  }
}

function restoreFromMessagesSnapshot(evt: AguiSseEvent): {
  messages: UiMessage[];
  reportSessions: ReportSession[];
  activeReportId: string | null;
  graphNodeId?: string;
  graphInterrupt: GraphInterrupt | null;
} {
  const now = Date.now();
  const timestamp = typeof evt.timestamp === "number" ? evt.timestamp : now;
  const snapshotMessages = Array.isArray((evt as any).messages) ? (evt as any).messages : [];

  const uiMessages: UiMessage[] = [];
  const messageIndexById = new Map<string, number>();
  const toolCallIndexById = new Map<string, number>();
  const toolCallNameById = new Map<string, string>();
  const toolCallArgsById = new Map<string, string>();

  const activityState: Record<string, any> = {};
  let graphNodeId: string | undefined;
  let graphInterrupt: GraphInterrupt | null = null;

  let activeThinkingIndex: number | null = null;
  const reportSessions: ReportSession[] = [];
  let activeReportId: string | null = null;

  const upsertUiMessage = (id: string, updater: (prev: UiMessage | null) => UiMessage) => {
    const index = messageIndexById.get(id);
    if (index === undefined) {
      const created = updater(null);
      messageIndexById.set(created.id, uiMessages.length);
      uiMessages.push(created);
      return;
    }
    const prev = uiMessages[index] ?? null;
    uiMessages[index] = updater(prev);
  };

  const upsertThinking = (updater: (prev: UiMessage | null) => UiMessage) => {
    if (activeThinkingIndex === null) {
      const created = updater(null);
      activeThinkingIndex = uiMessages.length;
      messageIndexById.set(created.id, activeThinkingIndex);
      uiMessages.push(created);
      return;
    }
    const prev = uiMessages[activeThinkingIndex] ?? null;
    uiMessages[activeThinkingIndex] = updater(prev);
  };

  const getActiveReportSession = () => {
    if (!activeReportId) {
      return null;
    }
    return reportSessions.find((session) => session.documentId === activeReportId) ?? null;
  };

  const upsertReportSession = (next: ReportSession) => {
    const index = reportSessions.findIndex((session) => session.documentId === next.documentId);
    if (index === -1) {
      reportSessions.push(next);
      return;
    }
    reportSessions[index] = { ...reportSessions[index], ...next };
  };

  const appendReportContent = (text: string) => {
    const active = getActiveReportSession();
    if (!active || active.status !== "open") {
      return;
    }
    const needsGap = active.content.trim().length > 0;
    active.content = needsGap ? `${active.content}\n\n${text}` : `${active.content}${text}`;
  };

  for (const msg of snapshotMessages) {
    const role = typeof msg?.role === "string" ? msg.role : "";

    if (role === "user") {
      const userContent = typeof msg?.content === "string" ? msg.content : formatStructured(msg?.content);
      if (!userContent.trim()) {
        activeThinkingIndex = null;
        continue;
      }

      uiMessages.push({
        id: typeof msg?.id === "string" ? msg.id : randomId("user_snapshot"),
        role: "user",
        kind: "text",
        title: typeof msg?.name === "string" ? msg.name : "You",
        content: userContent,
        timestamp,
      });
      messageIndexById.set(uiMessages[uiMessages.length - 1].id, uiMessages.length - 1);
      activeThinkingIndex = null;
      continue;
    }

    if (role === "assistant") {
      const content = typeof msg?.content === "string" ? msg.content : formatStructured(msg?.content);
      if (getActiveReportSession()?.status === "open") {
        if (content.trim()) {
          appendReportContent(content);
        }
      } else if (content.trim()) {
        uiMessages.push({
          id: typeof msg?.id === "string" ? msg.id : randomId("assistant_snapshot"),
          role: "assistant",
          kind: "text",
          title: typeof msg?.name === "string" ? msg.name : "Assistant",
          content,
          timestamp,
        });
        messageIndexById.set(uiMessages[uiMessages.length - 1].id, uiMessages.length - 1);
      }

      const toolCalls = Array.isArray(msg?.toolCalls) ? msg.toolCalls : [];
      for (const toolCall of toolCalls) {
        const toolCallId = typeof toolCall?.id === "string" ? toolCall.id : randomId("toolcall_snapshot");
        const toolCallName = typeof toolCall?.function?.name === "string"
          ? toolCall.function.name
          : typeof toolCall?.name === "string"
            ? toolCall.name
            : "tool";
        const args = typeof toolCall?.function?.arguments === "string"
          ? toolCall.function.arguments
          : typeof toolCall?.arguments === "string"
            ? toolCall.arguments
            : "";

        toolCallNameById.set(toolCallId, toolCallName);
        toolCallArgsById.set(toolCallId, args);

        const next: UiMessage = {
          id: toolCallId,
          role: "assistant",
          kind: "tool-call",
          title: `Tool call: ${toolCallName}`,
          content: args,
          status: "complete",
          toolCall: {
            toolCallId,
            toolCallName,
            parentMessageId: typeof msg?.id === "string" ? msg.id : undefined,
            args,
          },
          timestamp,
        };
        toolCallIndexById.set(toolCallId, uiMessages.length);
        messageIndexById.set(toolCallId, uiMessages.length);
        uiMessages.push(next);
      }
      continue;
    }

    if (role === "tool") {
      const toolCallId = typeof msg?.toolCallId === "string" ? msg.toolCallId : "";
      const raw = typeof msg?.content === "string" ? msg.content : formatStructured(msg?.content);
      const normalized = raw ? normalizeJsonString(raw) : undefined;
      const toolCallName = toolCallId ? toolCallNameById.get(toolCallId) : undefined;

      if (toolCallName === "open_report_document") {
        const payload = raw ? safeJsonParse(raw) : {};
        const title = typeof (payload as any)?.title === "string" ? (payload as any).title : "Report";
        const documentId = typeof (payload as any)?.documentId === "string"
          ? (payload as any).documentId
          : toolCallId || randomId("doc");
        const createdAt = typeof (payload as any)?.createdAt === "string" ? (payload as any).createdAt : undefined;
        upsertReportSession({ status: "open", title, documentId, createdAt, content: "" });
        activeReportId = documentId;
        const actionPayload: Record<string, any> = { title, documentId, status: "open" };
        if (createdAt) {
          actionPayload.createdAt = createdAt;
        }
        uiMessages.push({
          id: `report_open_${documentId}`,
          role: "assistant",
          kind: "tool-call",
          title: "Open report",
          content: "",
          status: "complete",
          toolCall: {
            toolCallId: `report_open_${documentId}`,
            toolCallName: REPORT_OPEN_TOOL_NAME,
            args: "",
            result: JSON.stringify(actionPayload, null, 2),
          },
          timestamp,
        });
      }

      if (toolCallName === "close_report_document" && activeReportId) {
        const payload = raw ? safeJsonParse(raw) : {};
        const closedAt = typeof (payload as any)?.closedAt === "string" ? (payload as any).closedAt : undefined;
        const reason = typeof (payload as any)?.reason === "string"
          ? (payload as any).reason
          : typeof (payload as any)?.message === "string"
            ? (payload as any).message
            : undefined;
        const active = getActiveReportSession();
        upsertReportSession({
          status: "closed",
          title: active?.title ?? "Report",
          documentId: activeReportId,
          createdAt: active?.createdAt,
          content: active?.content ?? "",
          closedAt,
          reason,
        });

        const actionMessageId = `report_open_${activeReportId}`;
        const actionIndex = uiMessages.findIndex((entry) => entry.id === actionMessageId);
        if (actionIndex !== -1) {
          const payload: Record<string, any> = {
            title: active?.title ?? "Report",
            documentId: activeReportId,
            status: "closed",
          };
          if (active?.createdAt) {
            payload.createdAt = active.createdAt;
          }
          if (closedAt) {
            payload.closedAt = closedAt;
          }
          if (reason) {
            payload.reason = reason;
          }
          const prev = uiMessages[actionIndex];
          uiMessages[actionIndex] = {
            ...prev,
            toolCall: prev.toolCall
              ? { ...prev.toolCall, result: JSON.stringify(payload, null, 2) }
              : { toolCallId: actionMessageId, toolCallName: REPORT_OPEN_TOOL_NAME, args: "", result: JSON.stringify(payload, null, 2) },
          };
        }
      }

      if (toolCallId && toolCallIndexById.has(toolCallId)) {
        const index = toolCallIndexById.get(toolCallId)!;
        const prev = uiMessages[index];
        const name = prev?.toolCall?.toolCallName ?? toolCallNameById.get(toolCallId) ?? "tool";
        const args = prev?.toolCall?.args ?? toolCallArgsById.get(toolCallId) ?? prev?.content ?? "";
        uiMessages[index] = {
          id: toolCallId,
          role: "assistant",
          kind: "tool-call",
          title: `Tool call: ${name}`,
          content: args,
          status: "complete",
          toolCall: {
            toolCallId,
            toolCallName: name,
            parentMessageId: prev?.toolCall?.parentMessageId,
            args,
            result: normalized ?? prev?.toolCall?.result,
          },
          timestamp: prev?.timestamp ?? timestamp,
        };
        messageIndexById.set(toolCallId, index);
      } else if (toolCallId && raw.trim()) {
        const name = toolCallNameById.get(toolCallId) ?? "tool";
        const args = toolCallArgsById.get(toolCallId) ?? "";
        toolCallIndexById.set(toolCallId, uiMessages.length);
        messageIndexById.set(toolCallId, uiMessages.length);
        uiMessages.push({
          id: toolCallId,
          role: "assistant",
          kind: "tool-call",
          title: `Tool call: ${name}`,
          content: args,
          status: "complete",
          toolCall: {
            toolCallId,
            toolCallName: name,
            args,
            result: normalized,
          },
          timestamp,
        });
      } else if (raw.trim()) {
        const toolCallIdFallback = typeof msg?.id === "string" ? msg.id : randomId("tool_result");
        const name = typeof msg?.name === "string" ? msg.name : "tool";
        toolCallNameById.set(toolCallIdFallback, name);
        toolCallArgsById.set(toolCallIdFallback, "");
        toolCallIndexById.set(toolCallIdFallback, uiMessages.length);
        messageIndexById.set(toolCallIdFallback, uiMessages.length);
        uiMessages.push({
          id: toolCallIdFallback,
          role: "assistant",
          kind: "tool-call",
          title: `Tool result: ${name}`,
          content: "",
          status: "complete",
          toolCall: {
            toolCallId: toolCallIdFallback,
            toolCallName: name,
            args: "",
            result: normalized,
          },
          timestamp,
        });
      }
      activeThinkingIndex = null;
      continue;
    }

    if (role === "activity") {
      const activityType = typeof msg?.activityType === "string" ? msg.activityType : "";
      if (activityType === "ACTIVITY_DELTA") {
        const content = msg?.content as any;
        const patch = Array.isArray(content?.patch) ? content.patch : [];
        applyJsonPatch(activityState, patch);

        const node = activityState.node;
        if (node && typeof node.nodeId === "string") {
          graphNodeId = node.nodeId;
        }

        if (Object.prototype.hasOwnProperty.call(activityState, "interrupt")) {
          const interrupt = activityState.interrupt;
          if (!interrupt) {
            graphInterrupt = null;
          } else if (typeof interrupt === "object") {
            const key = typeof interrupt.key === "string" ? interrupt.key : "";
            const prompt = typeof interrupt.prompt === "string" ? interrupt.prompt : "Interrupt received.";
            graphInterrupt = {
              key,
              prompt,
              checkpointId: typeof interrupt.checkpointId === "string" ? interrupt.checkpointId : undefined,
              lineageId: typeof interrupt.lineageId === "string" ? interrupt.lineageId : undefined,
            };

            if (shouldSuppressGraphApproval(graphInterrupt, toolCallNameById)) {
              continue;
            }

            const identity = graphInterruptIdentity(graphInterrupt);
            const messageId = identity ? `graph_interrupt_${sanitizeIdPart(identity) || "interrupt"}` : randomId("graph_interrupt");
            upsertUiMessage(messageId, (prev) => {
              const prevArgs = prev?.toolCall?.args ?? "";
              const parsed = prevArgs ? safeJsonParse(prevArgs) : null;
              const prevDecision = parsed && typeof parsed === "object" && typeof (parsed as any).decision === "string"
                ? String((parsed as any).decision)
                : "pending";
              const decision = prevDecision === "approve" || prevDecision === "dismiss" ? prevDecision : "pending";
              const args = JSON.stringify({ prompt, decision }, null, 2);
              const nextTimestamp = prev?.timestamp ?? timestamp;
              return {
                id: messageId,
                role: "assistant",
                kind: "tool-call",
                title: "Graph interrupt approval",
                content: "",
                status: "complete",
                toolCall: {
                  toolCallId: messageId,
                  toolCallName: GRAPH_APPROVAL_TOOL_NAME,
                  parentMessageId: prev?.toolCall?.parentMessageId,
                  args,
                  result: prev?.toolCall?.result,
                },
                timestamp: nextTimestamp,
              };
            });
          }
        }

        if (activityState.resume && typeof activityState.resume === "object") {
          const resume = activityState.resume as any;
          const checkpointId = typeof resume.checkpointId === "string" ? resume.checkpointId : "";
          const lineageId = typeof resume.lineageId === "string" ? resume.lineageId : "";
          const resumeMap = resume.resumeMap && typeof resume.resumeMap === "object" ? resume.resumeMap as Record<string, any> : {};
          const resumeKey = Object.keys(resumeMap)[0] ?? "";
          const resumeValue = resumeKey ? resumeMap[resumeKey] : undefined;
          const decision = resumeKey && resumeValue ? "approve" : "dismiss";

          const identity = [checkpointId, lineageId, resumeKey].filter(Boolean).join("|");
          if (identity) {
            const messageId = `graph_interrupt_${sanitizeIdPart(identity) || "interrupt"}`;
            upsertUiMessage(messageId, (prev) => {
              const prevArgs = prev?.toolCall?.args ?? "";
              const parsed = prevArgs ? safeJsonParse(prevArgs) : null;
              const prompt = parsed && typeof parsed === "object" ? String((parsed as any).prompt ?? "") : "";
              const args = JSON.stringify({ prompt, decision }, null, 2);
              const nextTimestamp = prev?.timestamp ?? timestamp;
              return {
                id: messageId,
                role: "assistant",
                kind: "tool-call",
                title: "Graph interrupt approval",
                content: "",
                status: "complete",
                toolCall: {
                  toolCallId: messageId,
                  toolCallName: GRAPH_APPROVAL_TOOL_NAME,
                  parentMessageId: prev?.toolCall?.parentMessageId,
                  args,
                  result: prev?.toolCall?.result,
                },
                timestamp: nextTimestamp,
              };
            });
          }
        }

        delete activityState.resume;
        continue;
      }

      if (activityType !== "CUSTOM") {
        continue;
      }
      const content = msg?.content;
      const name = typeof (content as any)?.name === "string" ? (content as any).name : "";
      const value = (content as any)?.value;

      if (name === "think_start") {
        upsertThinking(() => ({
          id: typeof msg?.id === "string" ? msg.id : randomId("thinking_snapshot"),
          role: "assistant",
          kind: "thinking",
          title: "Thinking",
          content: "",
          status: "streaming",
          timestamp,
        }));
        continue;
      }

      if (name === "think_content") {
        const chunk = typeof value === "string" ? value : formatStructured(value);
        if (!chunk) {
          continue;
        }
        upsertThinking((prev) => {
          const previous = prev?.content ?? "";
          return {
            id: prev?.id ?? randomId("thinking_snapshot"),
            role: "assistant",
            kind: "thinking",
            title: prev?.title ?? "Thinking",
            content: previous + chunk,
            status: "streaming",
            timestamp: prev?.timestamp ?? timestamp,
          };
        });
        continue;
      }

      if (name === "think_end") {
        if (activeThinkingIndex !== null) {
          const prev = uiMessages[activeThinkingIndex] ?? null;
          uiMessages[activeThinkingIndex] = {
            id: prev?.id ?? (typeof msg?.id === "string" ? msg.id : randomId("thinking_snapshot")),
            role: "assistant",
            kind: "thinking",
            title: prev?.title ?? "Thinking",
            content: prev?.content ?? "",
            status: "complete",
            timestamp: prev?.timestamp ?? timestamp,
          };
        }
        activeThinkingIndex = null;
        continue;
      }

      if (name === "node.progress") {
        continue;
      }

      if (name.startsWith("react.")) {
        const suffix = name.slice("react.".length);
        const title = suffix ? `React ${suffix}` : "React event";
        const text = formatStructured(value);
        if (!text) {
          continue;
        }
        uiMessages.push({
          id: typeof msg?.id === "string" ? msg.id : randomId("react_snapshot"),
          role: "assistant",
          kind: "thinking",
          title,
          content: text,
          status: "complete",
          timestamp,
        });
        activeThinkingIndex = null;
        continue;
      }

      const text = formatStructured(value);
      if (!text) {
        continue;
      }
      uiMessages.push({
        id: typeof msg?.id === "string" ? msg.id : randomId("custom_snapshot"),
        role: "assistant",
        kind: "thinking",
        title: name ? `Custom ${name}` : "Custom event",
        content: text,
        status: "complete",
        timestamp,
      });
      activeThinkingIndex = null;
      continue;
    }
  }

  if (activeThinkingIndex !== null) {
    const prev = uiMessages[activeThinkingIndex] ?? null;
    uiMessages[activeThinkingIndex] = {
      id: prev?.id ?? randomId("thinking_snapshot"),
      role: "assistant",
      kind: "thinking",
      title: prev?.title ?? "Thinking",
      content: prev?.content ?? "",
      status: prev?.status ?? "complete",
      timestamp: prev?.timestamp ?? timestamp,
    };
  }

  return {
    messages: uiMessages,
    reportSessions,
    activeReportId,
    graphNodeId,
    graphInterrupt,
  };
}

export function useAguiChat(config: AguiChatConfig) {
  const [messages, setMessages] = useState<UiMessage[]>([]);
  const [rawEvents, setRawEvents] = useState<RawAguiEvent[]>([]);
  const [inProgress, setInProgress] = useState(false);
  const [finishReason, setFinishReason] = useState<string | undefined>();
  const [lastError, setLastError] = useState<string | null>(null);
  const [graphNodeId, setGraphNodeId] = useState<string | undefined>();
  const [graphInterrupt, setGraphInterrupt] = useState<GraphInterrupt | null>(null);
  const [progress, setProgress] = useState<{ percent: number; label?: string } | null>(null);
  const [reportSessions, setReportSessions] = useState<ReportSession[]>([]);
  const [activeReportId, setActiveReportId] = useState<string | null>(null);
  const [reportDrawerOpen, setReportDrawerOpen] = useState(false);

  const abortRef = useRef<AbortController | null>(null);
  const messageIndexByIdRef = useRef<Map<string, number>>(new Map());
  const toolCallNameByIdRef = useRef<Map<string, string>>(new Map());
  const toolCallArgsByIdRef = useRef<Map<string, string>>(new Map());
  const activityStateRef = useRef<Record<string, any>>({});
  const activeThinkingRef = useRef<{ id: string; active: boolean } | null>(null);
  const reportSessionsRef = useRef<ReportSession[]>([]);
  const writingReportIdRef = useRef<string | null>(null);
  const graphInterruptIdentityRef = useRef<string | null>(null);
  const graphApprovalMessageIdRef = useRef<string | null>(null);
  const rawEventsRef = useRef<RawAguiEvent[]>([]);
  const rawEventsFlushRef = useRef<number | null>(null);

  reportSessionsRef.current = reportSessions;

  const addMessage = useCallback((message: UiMessage) => {
    setMessages((prev) => {
      const next = [...prev, message];
      messageIndexByIdRef.current.set(message.id, next.length - 1);
      return next;
    });
  }, []);

  const replaceMessages = useCallback((next: UiMessage[]) => {
    messageIndexByIdRef.current.clear();
    toolCallNameByIdRef.current.clear();
    toolCallArgsByIdRef.current.clear();
    activeThinkingRef.current = null;

    next.forEach((msg, index) => {
      messageIndexByIdRef.current.set(msg.id, index);
      if (msg.kind === "tool-call" && msg.toolCall?.toolCallId) {
        toolCallNameByIdRef.current.set(msg.toolCall.toolCallId, msg.toolCall.toolCallName);
        if (typeof msg.toolCall.args === "string") {
          toolCallArgsByIdRef.current.set(msg.toolCall.toolCallId, msg.toolCall.args);
        }
      }
    });

    setMessages(next);
  }, []);

  const upsertMessage = useCallback((id: string, updater: (msg: UiMessage | null) => UiMessage) => {
    setMessages((prev) => {
      const index = messageIndexByIdRef.current.get(id);
      if (index === undefined) {
        const created = updater(null);
        const next = [...prev, created];
        messageIndexByIdRef.current.set(created.id, next.length - 1);
        return next;
      }
      const existing = prev[index] ?? null;
      const updated = updater(existing);
      const next = prev.slice();
      next[index] = updated;
      return next;
    });
  }, []);

  const flushRawEvents = useCallback(() => {
    rawEventsFlushRef.current = null;
    setRawEvents([...rawEventsRef.current]);
  }, []);

  const setReportOpen = useCallback((open: boolean) => {
    setReportDrawerOpen(open);
  }, []);

  const openReport = useCallback((documentId: string) => {
    if (!documentId) {
      return;
    }
    setActiveReportId(documentId);
    setReportDrawerOpen(true);
  }, []);

  const updateReportSessions = useCallback((updater: (prev: ReportSession[]) => ReportSession[]) => {
    const next = updater(reportSessionsRef.current);
    reportSessionsRef.current = next;
    setReportSessions(next);
  }, []);

  const appendRawEvent = useCallback(
    (evt: AguiSseEvent) => {
      const timestamp = typeof evt.timestamp === "number" ? evt.timestamp : Date.now();
      const type = typeof evt.type === "string" ? evt.type : "UNKNOWN";
      const next: RawAguiEvent = {
        id: randomId("raw"),
        kind: "event",
        type,
        timestamp,
        payload: evt,
      };
      const limit = 800;
      const prev = rawEventsRef.current;
      rawEventsRef.current = prev.length >= limit ? [...prev.slice(prev.length - limit + 1), next] : [...prev, next];
      if (rawEventsFlushRef.current === null) {
        rawEventsFlushRef.current = window.requestAnimationFrame(flushRawEvents);
      }
    },
    [flushRawEvents],
  );

  const appendRequest = useCallback(
    (request: { endpoint: string; payload: Record<string, any> }) => {
      const next: RawAguiEvent = {
        id: randomId("raw_request"),
        kind: "request",
        type: "RunAgentInput",
        timestamp: Date.now(),
        payload: request,
      };
      const limit = 800;
      const prev = rawEventsRef.current;
      rawEventsRef.current = prev.length >= limit ? [...prev.slice(prev.length - limit + 1), next] : [...prev, next];
      if (rawEventsFlushRef.current === null) {
        rawEventsFlushRef.current = window.requestAnimationFrame(flushRawEvents);
      }
    },
    [flushRawEvents],
  );

  const clearRawEvents = useCallback(() => {
    rawEventsRef.current = [];
    flushRawEvents();
  }, [flushRawEvents]);

  const abortActiveRun = useCallback(() => {
    const controller = abortRef.current;
    if (!controller) {
      return;
    }
    controller.abort();
    abortRef.current = null;
  }, []);

  const handleCustomEvent = useCallback(
    (evt: AguiSseEvent) => {
      const name = typeof evt.name === "string" ? evt.name : "custom";
      const value = evt.value;

      if (name === "think_start") {
        const id = randomId("thinking");
        activeThinkingRef.current = { id, active: true };
        addMessage({
          id,
          role: "assistant",
          kind: "thinking",
          title: "Thinking",
          content: "",
          status: "streaming",
          timestamp: evt.timestamp ?? Date.now(),
        });
        return;
      }

      if (name === "think_content") {
        const chunk = typeof value === "string" ? value : formatStructured(value);
        const active = activeThinkingRef.current;
        if (!active?.id) {
          const id = randomId("thinking");
          activeThinkingRef.current = { id, active: true };
          addMessage({
            id,
            role: "assistant",
            kind: "thinking",
            title: "Thinking",
            content: chunk,
            status: "streaming",
            timestamp: evt.timestamp ?? Date.now(),
          });
          return;
        }
        upsertMessage(active.id, (msg) => {
          const prev = msg?.content ?? "";
          return {
            id: active.id,
            role: "assistant",
            kind: "thinking",
            title: "Thinking",
            content: prev + chunk,
            status: "streaming",
            timestamp: msg?.timestamp ?? evt.timestamp ?? Date.now(),
          };
        });
        return;
      }

      if (name === "think_end") {
        const active = activeThinkingRef.current;
        if (active?.id) {
          upsertMessage(active.id, (msg) => {
            return {
              id: active.id,
              role: "assistant",
              kind: "thinking",
              title: msg?.title ?? "Thinking",
              content: msg?.content ?? "",
              status: "complete",
              timestamp: msg?.timestamp ?? evt.timestamp ?? Date.now(),
            };
          });
        }
        activeThinkingRef.current = null;
        return;
      }

      if (name === "node.progress") {
        const parsed = extractActivityValue(value) ?? {};
        const percent = Number(parsed.progress ?? parsed.percent ?? parsed.value ?? 0);
        const label = typeof parsed.message === "string" ? parsed.message : undefined;
        if (!Number.isNaN(percent)) {
          setProgress({ percent, label });
        }
        return;
      }

      if (name.startsWith("react.")) {
        const content = formatStructured(value);
        if (!content) {
          return;
        }
        const suffix = name.slice("react.".length);
        const title = suffix ? `React ${suffix}` : "React event";
        addMessage({
          id: randomId("react"),
          role: "assistant",
          kind: "thinking",
          title,
          content,
          status: "complete",
          timestamp: evt.timestamp ?? Date.now(),
        });
        return;
      }
    },
    [addMessage, upsertMessage],
  );

  const handleActivityDelta = useCallback(
    (evt: AguiSseEvent) => {
      const ops = Array.isArray(evt.patch) ? evt.patch : [];
      const root = activityStateRef.current;
      applyJsonPatch(root, ops);

      const node = root.node;
      if (node && typeof node.nodeId === "string") {
        setGraphNodeId(node.nodeId);
      }

      if (Object.prototype.hasOwnProperty.call(root, "interrupt")) {
        const interrupt = root.interrupt;
        if (!interrupt) {
          setGraphInterrupt(null);
          graphInterruptIdentityRef.current = null;
          graphApprovalMessageIdRef.current = null;
        } else if (typeof interrupt === "object") {
          const key = typeof interrupt.key === "string" ? interrupt.key : "";
          const prompt = typeof interrupt.prompt === "string" ? interrupt.prompt : "Interrupt received.";
          const nextInterrupt: GraphInterrupt = {
            key,
            prompt,
            checkpointId: typeof interrupt.checkpointId === "string" ? interrupt.checkpointId : undefined,
            lineageId: typeof interrupt.lineageId === "string" ? interrupt.lineageId : undefined,
          };
          setGraphInterrupt(nextInterrupt);

          if (shouldSuppressGraphApproval(nextInterrupt, toolCallNameByIdRef.current)) {
            graphInterruptIdentityRef.current = null;
            graphApprovalMessageIdRef.current = null;
            delete root.resume;
            return;
          }

          const identity = graphInterruptIdentity(nextInterrupt);
          if (identity && graphInterruptIdentityRef.current !== identity) {
            graphInterruptIdentityRef.current = identity;
            graphApprovalMessageIdRef.current = `graph_interrupt_${sanitizeIdPart(identity) || "interrupt"}`;
          }
          if (!graphApprovalMessageIdRef.current) {
            graphApprovalMessageIdRef.current = randomId("graph_interrupt");
          }

          const messageId = graphApprovalMessageIdRef.current;
          upsertMessage(messageId, (msg) => {
            const prevArgs = msg?.toolCall?.args ?? "";
            const parsed = prevArgs ? safeJsonParse(prevArgs) : null;
            const prevDecision = parsed && typeof parsed === "object" && typeof (parsed as any).decision === "string"
              ? String((parsed as any).decision)
              : "pending";
            const decision = prevDecision === "approve" || prevDecision === "dismiss" ? prevDecision : "pending";
            const args = JSON.stringify({ prompt, decision }, null, 2);
            const timestamp = msg?.timestamp ?? evt.timestamp ?? Date.now();
            return {
              id: messageId,
              role: "assistant",
              kind: "tool-call",
              title: "Graph interrupt approval",
              content: "",
              status: "complete",
              toolCall: {
                toolCallId: messageId,
                toolCallName: GRAPH_APPROVAL_TOOL_NAME,
                parentMessageId: msg?.toolCall?.parentMessageId,
                args,
                result: msg?.toolCall?.result,
              },
              timestamp,
            };
          });
        }
      }

      delete root.resume;
    },
    [upsertMessage],
  );

  const handleToolCallResult = useCallback(
    (evt: AguiSseEvent) => {
      const toolCallId = typeof evt.toolCallId === "string" ? evt.toolCallId : "";
      const toolName = toolCallNameByIdRef.current.get(toolCallId) ?? "";
      const raw = typeof evt.content === "string" ? evt.content : "";
      const structured = raw ? formatStructured(safeJsonParse(raw)) : "";
      const normalized = structured ? normalizeJsonString(structured) : undefined;

      if (toolName === "open_report_document") {
        const payload = (typeof raw === "string" && raw ? safeJsonParse(raw) : {}) as any;
        const title = typeof payload?.title === "string" ? payload.title : "Report";
        const documentId = typeof payload?.documentId === "string" ? payload.documentId : toolCallId || randomId("doc");
        const createdAt = typeof payload?.createdAt === "string" ? payload.createdAt : undefined;
        const next: ReportSession = { status: "open", title, documentId, createdAt, content: "" };
        writingReportIdRef.current = documentId;
        updateReportSessions((prev) => {
          const index = prev.findIndex((session) => session.documentId === documentId);
          if (index === -1) {
            return [...prev, next];
          }
          const copy = prev.slice();
          copy[index] = { ...copy[index], ...next };
          return copy;
        });
        setActiveReportId(documentId);
        setReportDrawerOpen(true);

        const actionMessageId = `report_open_${documentId}`;
        const actionPayload: Record<string, any> = { title, documentId, status: "open" };
        if (createdAt) {
          actionPayload.createdAt = createdAt;
        }
        upsertMessage(actionMessageId, (msg) => {
          const timestamp = msg?.timestamp ?? evt.timestamp ?? Date.now();
          return {
            id: actionMessageId,
            role: "assistant",
            kind: "tool-call",
            title: "Open report",
            content: "",
            status: "complete",
            toolCall: {
              toolCallId: actionMessageId,
              toolCallName: REPORT_OPEN_TOOL_NAME,
              parentMessageId: typeof evt.parentMessageId === "string" ? evt.parentMessageId : msg?.toolCall?.parentMessageId,
              args: "",
              result: JSON.stringify(actionPayload, null, 2),
            },
            timestamp,
          };
        });
      }

      if (toolName === "close_report_document") {
        const payload = (typeof raw === "string" && raw ? safeJsonParse(raw) : {}) as any;
        const closedAt = typeof payload?.closedAt === "string" ? payload.closedAt : undefined;
        const reason = typeof payload?.reason === "string"
          ? payload.reason
          : typeof payload?.message === "string"
            ? payload.message
            : undefined;
        const payloadDocumentId = typeof payload?.documentId === "string" ? payload.documentId : "";
        const targetId = payloadDocumentId.trim()
          || writingReportIdRef.current
          || reportSessionsRef.current.slice().reverse().find((session) => session.status === "open")?.documentId
          || "";
        if (targetId) {
          const session = reportSessionsRef.current.find((entry) => entry.documentId === targetId) ?? null;
          updateReportSessions((prev) => {
            const index = prev.findIndex((session) => session.documentId === targetId);
            if (index === -1) {
              return prev;
            }
            const copy = prev.slice();
            copy[index] = { ...copy[index], status: "closed", closedAt, reason };
            return copy;
          });
          const actionMessageId = `report_open_${targetId}`;
          const actionPayload: Record<string, any> = {
            title: session?.title ?? "Report",
            documentId: targetId,
            status: "closed",
          };
          if (session?.createdAt) {
            actionPayload.createdAt = session.createdAt;
          }
          if (closedAt) {
            actionPayload.closedAt = closedAt;
          }
          if (reason) {
            actionPayload.reason = reason;
          }
          upsertMessage(actionMessageId, (msg) => {
            const timestamp = msg?.timestamp ?? evt.timestamp ?? Date.now();
            return {
              id: actionMessageId,
              role: "assistant",
              kind: "tool-call",
              title: "Open report",
              content: "",
              status: "complete",
              toolCall: {
                toolCallId: actionMessageId,
                toolCallName: REPORT_OPEN_TOOL_NAME,
                parentMessageId: msg?.toolCall?.parentMessageId,
                args: "",
                result: JSON.stringify(actionPayload, null, 2),
              },
              timestamp,
            };
          });
        }
        if (writingReportIdRef.current && writingReportIdRef.current === targetId) {
          writingReportIdRef.current = null;
        }
      }

      if (toolCallId) {
        upsertMessage(toolCallId, (msg) => {
          const args = msg?.toolCall?.args ?? toolCallArgsByIdRef.current.get(toolCallId) ?? msg?.content ?? "";
          const name = msg?.toolCall?.toolCallName ?? (toolName || "tool");
          return {
            id: toolCallId,
            role: "assistant",
            kind: "tool-call",
            title: `Tool call: ${name}`,
            content: args,
            status: "complete",
            toolCall: {
              toolCallId,
              toolCallName: name,
              parentMessageId: typeof evt.parentMessageId === "string" ? evt.parentMessageId : msg?.toolCall?.parentMessageId,
              args,
              result: normalized ?? msg?.toolCall?.result,
            },
            timestamp: msg?.timestamp ?? evt.timestamp ?? Date.now(),
          };
        });
      }
    },
    [updateReportSessions, upsertMessage],
  );

  const handleEvent = useCallback(
    (evt: AguiSseEvent) => {
      const type = typeof evt.type === "string" ? evt.type : "UNKNOWN";

      if (type === "RUN_STARTED") {
        appendRawEvent(evt);
        setInProgress(true);
        setFinishReason(undefined);
        setLastError(null);
        setGraphNodeId(undefined);
        setGraphInterrupt(null);
        setProgress(null);
        return;
      }

      if (type === "RUN_FINISHED") {
        appendRawEvent(evt);
        setInProgress(false);
        abortRef.current = null;
        const result = typeof evt.result === "string" ? evt.result : undefined;
        setFinishReason(result);
        return;
      }

      if (type === "RUN_ERROR") {
        appendRawEvent(evt);
        setInProgress(false);
        abortRef.current = null;
        const msg = typeof evt.message === "string" ? evt.message : "Run error.";
        setLastError(msg);
        return;
      }

      if (type === "TEXT_MESSAGE_START") {
        appendRawEvent(evt);
        const writingId = writingReportIdRef.current;
        if (writingId) {
          const active = reportSessionsRef.current.find((session) => session.documentId === writingId) ?? null;
          if (active?.status === "open") {
            updateReportSessions((prev) => {
              const index = prev.findIndex((session) => session.documentId === writingId);
              if (index === -1) {
                return prev;
              }
              const current = prev[index];
              const needsGap = current.content.trim().length > 0;
              if (!needsGap) {
                return prev;
              }
              const copy = prev.slice();
              copy[index] = { ...current, content: `${current.content}\n\n` };
              return copy;
            });
            return;
          }
        }
        const messageId = typeof evt.messageId === "string" ? evt.messageId : randomId("assistant");
        addMessage({
          id: messageId,
          role: normalizeRole(evt.role),
          kind: "text",
          title: "Assistant",
          content: "",
          timestamp: evt.timestamp ?? Date.now(),
        });
        return;
      }

      if (type === "TEXT_MESSAGE_CONTENT") {
        appendRawEvent(evt);
        const delta = typeof evt.delta === "string" ? evt.delta : "";
        if (!delta) {
          return;
        }
        const writingId = writingReportIdRef.current;
        if (writingId) {
          const active = reportSessionsRef.current.find((session) => session.documentId === writingId) ?? null;
          if (active?.status === "open") {
            updateReportSessions((prev) => {
              const index = prev.findIndex((session) => session.documentId === writingId);
              if (index === -1) {
                return prev;
              }
              const current = prev[index];
              const copy = prev.slice();
              copy[index] = { ...current, content: current.content + delta };
              return copy;
            });
            return;
          }
        }
        const messageId = typeof evt.messageId === "string" ? evt.messageId : "";
        if (!messageId) {
          return;
        }
        upsertMessage(messageId, (msg) => {
          const prev = msg?.content ?? "";
          return {
            id: messageId,
            role: "assistant",
            kind: "text",
            title: msg?.title ?? "Assistant",
            content: prev + delta,
            timestamp: msg?.timestamp ?? evt.timestamp ?? Date.now(),
          };
        });
        return;
      }

      if (type === "TOOL_CALL_START") {
        appendRawEvent(evt);
        const toolCallId = typeof evt.toolCallId === "string" ? evt.toolCallId : randomId("tool_call");
        const toolCallName = typeof evt.toolCallName === "string" ? evt.toolCallName : "tool";
        toolCallNameByIdRef.current.set(toolCallId, toolCallName);
        toolCallArgsByIdRef.current.set(toolCallId, "");

        addMessage({
          id: toolCallId,
          role: "assistant",
          kind: "tool-call",
          title: `Tool call: ${toolCallName}`,
          content: "",
          status: "streaming",
          toolCall: {
            toolCallId,
            toolCallName,
            parentMessageId: typeof evt.parentMessageId === "string" ? evt.parentMessageId : undefined,
            args: "",
          },
          timestamp: evt.timestamp ?? Date.now(),
        });
        return;
      }

      if (type === "TOOL_CALL_ARGS") {
        appendRawEvent(evt);
        const toolCallId = typeof evt.toolCallId === "string" ? evt.toolCallId : "";
        const delta = typeof evt.delta === "string" ? evt.delta : "";
        if (!toolCallId || !delta) {
          return;
        }
        const prev = toolCallArgsByIdRef.current.get(toolCallId) ?? "";
        toolCallArgsByIdRef.current.set(toolCallId, prev + delta);
        upsertMessage(toolCallId, (msg) => {
          const next = (msg?.content ?? "") + delta;
          const toolCallName = msg?.toolCall?.toolCallName ?? toolCallNameByIdRef.current.get(toolCallId) ?? "tool";
          return {
            id: toolCallId,
            role: "assistant",
            kind: "tool-call",
            title: msg?.title ?? "Tool call",
            content: next,
            status: msg?.status ?? "streaming",
            toolCall: {
              toolCallId,
              toolCallName,
              parentMessageId: msg?.toolCall?.parentMessageId,
              args: next,
              result: msg?.toolCall?.result,
            },
            timestamp: msg?.timestamp ?? evt.timestamp ?? Date.now(),
          };
        });
        return;
      }

      if (type === "TOOL_CALL_RESULT") {
        appendRawEvent(evt);
        handleToolCallResult(evt);
        return;
      }

      if (type === "CUSTOM") {
        appendRawEvent(evt);
        handleCustomEvent(evt);
        return;
      }

      if (type === "ACTIVITY_DELTA") {
        appendRawEvent(evt);
        handleActivityDelta(evt);
        return;
      }
      appendRawEvent(evt);
    },
    [addMessage, appendRawEvent, handleActivityDelta, handleCustomEvent, handleToolCallResult, updateReportSessions, upsertMessage],
  );

  const stop = useCallback(() => {
    abortActiveRun();
    setInProgress(false);
  }, [abortActiveRun]);

  const run = useCallback(
    async (payload: Record<string, any>) => {
      abortActiveRun();
      const controller = new AbortController();
      abortRef.current = controller;
      setInProgress(true);
      appendRequest({ endpoint: config.endpoint, payload });

      try {
        await streamAguiSse(config.endpoint, payload, {
          signal: controller.signal,
          onEvent: handleEvent,
        });
      } catch (error: any) {
        if (controller.signal.aborted) {
          return;
        }
        setInProgress(false);
        abortRef.current = null;
        setLastError(String(error?.message ?? error));
      }
    },
    [abortActiveRun, appendRequest, config.endpoint, handleEvent],
  );

  const loadHistory = useCallback(
    async (options?: { endpoint?: string; threadId?: string; forwardedProps?: Record<string, unknown> }): Promise<HistoryLoadResult> => {
      const historyEndpoint = options?.endpoint ?? "";
      if (!historyEndpoint) {
        const message = "history endpoint is empty";
        setLastError(message);
        return { ok: false, message };
      }

      abortActiveRun();
      setLastError(null);
      const controller = new AbortController();
      abortRef.current = controller;
      setInProgress(true);

      let snapshotEvent: AguiSseEvent | null = null;
      let runError: string | null = null;

      const threadId = options?.threadId ?? config.threadId;
      const forwardedProps = options?.forwardedProps ?? config.forwardedProps;

      const payload: Record<string, any> = {
        threadId,
        runId: randomId("history"),
        messages: [{ role: "user", content: "" }],
      };
      if (forwardedProps && Object.keys(forwardedProps).length > 0) {
        payload.forwardedProps = forwardedProps;
      }
      appendRequest({ endpoint: historyEndpoint, payload });

      return await new Promise<HistoryLoadResult>((resolve) => {
        let settled = false;
        const settle = (result: HistoryLoadResult) => {
          if (settled) {
            return;
          }
          settled = true;
          resolve(result);
        };

        const applySnapshot = (evt: AguiSseEvent) => {
          snapshotEvent = evt;
          const restored = restoreFromMessagesSnapshot(evt);
          replaceMessages(restored.messages);
          reportSessionsRef.current = restored.reportSessions;
          setReportSessions(restored.reportSessions);
          const openReport = restored.reportSessions.slice().reverse().find((session) => session.status === "open") ?? null;
          writingReportIdRef.current = openReport ? openReport.documentId : null;
          const nextActiveReportId = openReport?.documentId ?? restored.activeReportId;
          setActiveReportId(nextActiveReportId);
          setReportDrawerOpen(Boolean(openReport));
          setGraphNodeId(restored.graphNodeId);
          setGraphInterrupt(restored.graphInterrupt);
          settle({ ok: true, count: restored.messages.length });
        };

        streamAguiSse(historyEndpoint, payload, {
          signal: controller.signal,
          onEvent: (evt) => {
            const type = typeof evt.type === "string" ? evt.type : "";
            if (type === "RUN_ERROR") {
              appendRawEvent(evt);
              const message = typeof (evt as any).message === "string" ? (evt as any).message : "Run error.";
              runError = message;
              setInProgress(false);
              if (abortRef.current === controller) {
                abortRef.current = null;
              }
              if (!isSessionNotFoundError(message)) {
                setLastError(message);
              }
              if (!snapshotEvent) {
                settle({ ok: false, message });
              }
              return;
            }
            if (type === "MESSAGES_SNAPSHOT") {
              appendRawEvent(evt);
              applySnapshot(evt);
              return;
            }
            handleEvent(evt);
          },
        }).then(() => {
          if (controller.signal.aborted) {
            settle({ ok: false, message: "aborted" });
            return;
          }
          if (!snapshotEvent) {
            const message = runError ?? "history snapshot not found";
            if (!isSessionNotFoundError(message)) {
              setLastError(message);
            }
            settle({ ok: false, message });
            if (abortRef.current === controller) {
              abortRef.current = null;
              setInProgress(false);
            }
            return;
          }
          if (abortRef.current === controller) {
            abortRef.current = null;
            setInProgress(false);
          }
        }).catch((error: any) => {
          if (controller.signal.aborted) {
            settle({ ok: false, message: "aborted" });
            if (abortRef.current === controller) {
              abortRef.current = null;
              setInProgress(false);
            }
            return;
          }
          const message = String(error?.message ?? error);
          if (!isSessionNotFoundError(message)) {
            setLastError(message);
          }
          settle({ ok: false, message });
          if (abortRef.current === controller) {
            abortRef.current = null;
            setInProgress(false);
          }
        });
      });
    },
    [
      abortActiveRun,
      appendRawEvent,
      appendRequest,
      config.forwardedProps,
      config.threadId,
      handleEvent,
      replaceMessages,
      setLastError,
    ],
  );

  const send = useCallback(
    async (text: string, options?: { forwardedProps?: Record<string, unknown> }) => {
      const trimmed = text.trim();
      if (!trimmed) {
        return;
      }

      addMessage({
        id: randomId("user"),
        role: "user",
        kind: "text",
        title: "You",
        content: trimmed,
        timestamp: Date.now(),
      });

      const payload: Record<string, any> = {
        threadId: config.threadId,
        runId: randomId("run"),
        messages: [{ role: "user", content: trimmed }],
      };
      const mergedForwardedProps = {
        ...(config.forwardedProps && Object.keys(config.forwardedProps).length > 0 ? config.forwardedProps : {}),
        ...(options?.forwardedProps && Object.keys(options.forwardedProps).length > 0 ? options.forwardedProps : {}),
      };
      if (Object.keys(mergedForwardedProps).length > 0) {
        payload.forwardedProps = mergedForwardedProps;
      }

      await run(payload);
    },
    [addMessage, config.forwardedProps, config.threadId, run],
  );

  const sendToolResult = useCallback(
    async (args: {
      toolCallId: string;
      toolCallName: string;
      content: string;
      messageId?: string;
      forwardedProps?: Record<string, unknown>;
    }) => {
      if (inProgress) {
        return;
      }

      const toolCallId = (args.toolCallId || "").trim();
      const toolCallName = (args.toolCallName || "").trim();
      const content = (args.content || "").trim();
      if (!toolCallId || !content) {
        return;
      }

      const payload: Record<string, any> = {
        threadId: config.threadId,
        runId: randomId("run"),
        messages: [{
          id: (args.messageId || `tool-result-${toolCallId}`).trim(),
          role: "tool",
          toolCallId,
          name: toolCallName || "tool",
          content,
        }],
      };
      const mergedForwardedProps = {
        ...(config.forwardedProps && Object.keys(config.forwardedProps).length > 0 ? config.forwardedProps : {}),
        ...(args.forwardedProps && Object.keys(args.forwardedProps).length > 0 ? args.forwardedProps : {}),
      };
      if (Object.keys(mergedForwardedProps).length > 0) {
        payload.forwardedProps = mergedForwardedProps;
      }

      await run(payload);
    },
    [config.forwardedProps, config.threadId, inProgress, run],
  );

  const approveGraphInterrupt = useCallback(async () => {
    if (!graphInterrupt) {
      return;
    }

    const identity = graphInterruptIdentity(graphInterrupt);
    const derivedMessageId = identity ? `graph_interrupt_${sanitizeIdPart(identity) || "interrupt"}` : "";
    const messageId = graphApprovalMessageIdRef.current || derivedMessageId || randomId("graph_interrupt");
    graphApprovalMessageIdRef.current = messageId;

    upsertMessage(messageId, (msg) => {
      const prevArgs = msg?.toolCall?.args ?? "";
      const parsed = prevArgs ? safeJsonParse(prevArgs) : null;
      const prompt = graphInterrupt.prompt || (parsed && typeof parsed === "object" ? String((parsed as any).prompt ?? "") : "");
      const args = JSON.stringify({ prompt, decision: "approve" }, null, 2);
      return {
        id: msg?.id ?? messageId,
        role: "assistant",
        kind: "tool-call",
        title: msg?.title ?? "Graph interrupt approval",
        content: msg?.content ?? "",
        status: msg?.status ?? "complete",
        toolCall: {
          toolCallId: messageId,
          toolCallName: GRAPH_APPROVAL_TOOL_NAME,
          parentMessageId: msg?.toolCall?.parentMessageId,
          args,
          result: msg?.toolCall?.result,
        },
        timestamp: msg?.timestamp ?? Date.now(),
      };
    });

    const resumeMap: Record<string, any> = {};
    if (graphInterrupt.key) {
      resumeMap[graphInterrupt.key] = true;
    }

    const state: Record<string, any> = {};
    if (graphInterrupt.lineageId) {
      state.lineage_id = graphInterrupt.lineageId;
    }
    if (graphInterrupt.checkpointId) {
      state.checkpoint_id = graphInterrupt.checkpointId;
    }
    state.resume_map = resumeMap;

    const payload: Record<string, any> = {
      threadId: config.threadId,
      runId: randomId("run"),
      state,
      messages: [{ role: "user", content: "" }],
    };
    const mergedForwardedProps = {
      ...(config.forwardedProps && Object.keys(config.forwardedProps).length > 0 ? config.forwardedProps : {}),
      ...(graphInterrupt.lineageId ? { lineage_id: graphInterrupt.lineageId } : {}),
    };
    if (Object.keys(mergedForwardedProps).length > 0) {
      payload.forwardedProps = mergedForwardedProps;
    }

    setGraphInterrupt(null);
    graphInterruptIdentityRef.current = null;
    graphApprovalMessageIdRef.current = null;
    await run(payload);
  }, [config.forwardedProps, config.threadId, graphInterrupt, run, upsertMessage]);

  const dismissGraphInterrupt = useCallback(async () => {
    const identity = graphInterrupt ? graphInterruptIdentity(graphInterrupt) : "";
    const derivedMessageId = identity ? `graph_interrupt_${sanitizeIdPart(identity) || "interrupt"}` : "";
    const messageId = graphApprovalMessageIdRef.current || derivedMessageId || (graphInterrupt ? randomId("graph_interrupt") : "");
    if (messageId) {
      graphApprovalMessageIdRef.current = messageId;
      upsertMessage(messageId, (msg) => {
        const prevArgs = msg?.toolCall?.args ?? "";
        const parsed = prevArgs ? safeJsonParse(prevArgs) : null;
        const prompt = graphInterrupt?.prompt || (parsed && typeof parsed === "object" ? String((parsed as any).prompt ?? "") : "");
        const args = JSON.stringify({ prompt, decision: "dismiss" }, null, 2);
        return {
          id: msg?.id ?? messageId,
          role: "assistant",
          kind: "tool-call",
          title: msg?.title ?? "Graph interrupt approval",
          content: msg?.content ?? "",
          status: msg?.status ?? "complete",
          toolCall: {
            toolCallId: messageId,
            toolCallName: GRAPH_APPROVAL_TOOL_NAME,
            parentMessageId: msg?.toolCall?.parentMessageId,
            args,
            result: msg?.toolCall?.result,
          },
          timestamp: msg?.timestamp ?? Date.now(),
        };
      });
    }

    if (!graphInterrupt) {
      setGraphInterrupt(null);
      graphInterruptIdentityRef.current = null;
      graphApprovalMessageIdRef.current = null;
      return;
    }

    const resumeMap: Record<string, any> = {};
    if (graphInterrupt.key) {
      resumeMap[graphInterrupt.key] = false;
    }

    const state: Record<string, any> = {};
    if (graphInterrupt.lineageId) {
      state.lineage_id = graphInterrupt.lineageId;
    }
    if (graphInterrupt.checkpointId) {
      state.checkpoint_id = graphInterrupt.checkpointId;
    }
    state.resume_map = resumeMap;

    const payload: Record<string, any> = {
      threadId: config.threadId,
      runId: randomId("run"),
      state,
      messages: [{ role: "user", content: "" }],
    };
    const mergedForwardedProps = {
      ...(config.forwardedProps && Object.keys(config.forwardedProps).length > 0 ? config.forwardedProps : {}),
      ...(graphInterrupt?.lineageId ? { lineage_id: graphInterrupt.lineageId } : {}),
    };
    if (Object.keys(mergedForwardedProps).length > 0) {
      payload.forwardedProps = mergedForwardedProps;
    }

    setGraphInterrupt(null);
    graphInterruptIdentityRef.current = null;
    graphApprovalMessageIdRef.current = null;
    await run(payload);
  }, [config.forwardedProps, config.threadId, graphInterrupt, run, upsertMessage]);

  const reset = useCallback(() => {
    abortActiveRun();
    setMessages([]);
    messageIndexByIdRef.current.clear();
    toolCallNameByIdRef.current.clear();
    toolCallArgsByIdRef.current.clear();
    activityStateRef.current = {};
    activeThinkingRef.current = null;
    reportSessionsRef.current = [];
    writingReportIdRef.current = null;
    clearRawEvents();
    setFinishReason(undefined);
    setLastError(null);
    setGraphNodeId(undefined);
    setGraphInterrupt(null);
    graphInterruptIdentityRef.current = null;
    graphApprovalMessageIdRef.current = null;
    setProgress(null);
    setReportSessions([]);
    setActiveReportId(null);
    setReportDrawerOpen(false);
  }, [abortActiveRun, clearRawEvents]);

  return {
    messages,
    rawEvents,
    inProgress,
    finishReason,
    lastError,
    graphNodeId,
    graphInterrupt,
    progress,
    reportSessions,
    activeReportId,
    reportDrawerOpen,
    setReportOpen,
    setReportDrawerOpen,
    openReport,
    loadHistory,
    send,
    stop,
    reset,
    approveGraphInterrupt,
    dismissGraphInterrupt,
    sendToolResult,
    clearRawEvents,
  };
}
