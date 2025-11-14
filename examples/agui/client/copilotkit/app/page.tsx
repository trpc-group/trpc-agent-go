//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

"use client";

import { Fragment, useEffect, useLayoutEffect, useMemo, useRef, useState } from "react";
import ReactMarkdown from "react-markdown";
import type { InputProps, RenderMessageProps } from "@copilotkit/react-ui";
import {
  AssistantMessage as DefaultAssistantMessage,
  CopilotChat,
  ImageRenderer as DefaultImageRenderer,
  UserMessage as DefaultUserMessage,
  useChatContext,
} from "@copilotkit/react-ui";
import type { MessagesProps } from "@copilotkit/react-ui/dist/components/chat/props";
import { useCopilotChatInternal as useCopilotChat } from "@copilotkit/react-core";
import type { Message } from "@copilotkit/shared";

const DEFAULT_PROMPT = "Calculate 2*(10+11), first explain the idea, then calculate, and finally give the conclusion.";

const PromptInput = ({
  inProgress,
  onSend,
  isVisible = false,
  onStop,
  hideStopButton = false,
}: InputProps) => {
  const context = useChatContext();
  const textareaRef = useRef<HTMLTextAreaElement>(null);
  const [text, setText] = useState<string>("");
  const [isComposing, setIsComposing] = useState(false);

  const adjustHeight = () => {
    const textarea = textareaRef.current;
    if (!textarea) {
      return;
    }
    const styles = window.getComputedStyle(textarea);
    const lineHeight = parseFloat(styles.lineHeight || "20");
    const paddingTop = parseFloat(styles.paddingTop || "0");
    const paddingBottom = parseFloat(styles.paddingBottom || "0");
    const baseHeight = lineHeight + paddingTop + paddingBottom;

    textarea.style.height = "auto";
    const value = textarea.value;
    if (value.trim() === "") {
      textarea.style.height = `${baseHeight}px`;
      textarea.style.overflowY = "hidden";
      return;
    }

    textarea.style.height = `${Math.max(textarea.scrollHeight, baseHeight)}px`;
    textarea.style.overflowY = "auto";
  };

  useLayoutEffect(() => {
    adjustHeight();
  }, [text]);

  useLayoutEffect(() => {
    adjustHeight();
  }, [isVisible]);

  useLayoutEffect(() => {
    if (textareaRef.current) {
      textareaRef.current.focus();
      // ensure consistent initial height after focus
      adjustHeight();
    }
  }, []);

  const handleDivClick = (event: React.MouseEvent<HTMLDivElement>) => {
    const target = event.target as HTMLElement;
    if (target.closest("button")) return;
    if (target.tagName === "TEXTAREA") return;
    textareaRef.current?.focus();
  };

  const send = () => {
    if (inProgress) {
      return;
    }
    const trimmed = text.trim();
    const payload = trimmed.length > 0 ? text : DEFAULT_PROMPT;
    onSend(payload);
    setText("");
    textareaRef.current?.focus();
  };

  const handleKeyDown = (event: React.KeyboardEvent<HTMLTextAreaElement>) => {
    if (event.key === "Enter" && !event.shiftKey && !isComposing) {
      event.preventDefault();
      if (inProgress && !hideStopButton) {
        onStop?.();
      } else {
        send();
      }
    }
  };

  return (
    <div className="copilotKitInputContainer">
      <div className="copilotKitInput" onClick={handleDivClick}>
        <textarea
          ref={textareaRef}
          placeholder={context.labels.placeholder}
          value={text}
          rows={1}
          onChange={(event) => setText(event.target.value)}
          onKeyDown={handleKeyDown}
          onCompositionStart={() => setIsComposing(true)}
          onCompositionEnd={() => setIsComposing(false)}
          style={{ overflow: "hidden", resize: "none" }}
        />
      </div>
    </div>
  );
};

function formatStructuredContent(value: unknown): string {
  if (value === null || value === undefined) {
    return "(empty)";
  }
  if (typeof value === "string") {
    const trimmed = value.trim();
    if (trimmed === "") {
      return "(empty)";
    }
    try {
      const maybeJson = JSON.parse(trimmed);
      return typeof maybeJson === "string"
        ? maybeJson
        : JSON.stringify(maybeJson, null, 2);
    } catch {
      return trimmed;
    }
  }
  try {
    return JSON.stringify(value, null, 2);
  } catch {
    return String(value);
  }
}

function renderToolBlock({
  id,
  name,
  label,
  body,
  kind,
}: {
  id: string;
  name: string;
  label: string;
  body: unknown;
  kind?: "custom" | "tool-call" | "tool-result";
}) {
  const content = formatStructuredContent(body);
  return (
    <div
      key={id}
      className={`tool-message${kind ? ` tool-message--${kind}` : ""}`}
      data-message-role="tool"
      data-message-kind={kind || undefined}
    >
      <span className="tool-message__label">{label || name}</span>
      <pre className="tool-message__body">{content}</pre>
    </div>
  );
}

function isCustomIdentifier(id: unknown): boolean {
  // Detect our mirrored custom tool calls by id prefix.
  try {
    const s = String(id ?? "");
    return s.startsWith("custom-");
  } catch {
    return false;
  }
}

const ToolAwareRenderMessage = ({
  message,
  inProgress,
  index,
  isCurrentMessage,
  onRegenerate,
  onCopy,
  onThumbsUp,
  onThumbsDown,
  markdownTagRenderers,
  AssistantMessage = DefaultAssistantMessage,
  UserMessage = DefaultUserMessage,
  ImageRenderer = DefaultImageRenderer,
}: RenderMessageProps) => {
  const messageType = (message as any)?.type;
  const customEventPayload =
    messageType === "AguiCustomEventMessage" || (message as any)?.customEvent
      ? (message as any)?.customEvent ?? {
          name: (message as any)?.name,
          value: (message as any)?.content,
        }
      : undefined;

  if (customEventPayload) {
    const identifier = String(message.id ?? `${index}-custom-event`);
    const label = customEventPayload.name ? String(customEventPayload.name) : "Custom event";
    return renderToolBlock({
      id: identifier,
      name: label,
      label,
      body: customEventPayload.value,
      kind: "custom",
    });
  }

  if (messageType === "ActionExecutionMessage") {
    const actionName = (message as any)?.name ?? "Tool call";
    const args = (message as any)?.arguments ?? {};
    const maybeId = (message as any)?.id ?? (message as any)?.toolCallId;
    const kind = isCustomIdentifier(maybeId) ? "custom" : "tool-call";
    return renderToolBlock({
      id: String((message as any)?.id ?? `${index}-tool-call`),
      name: actionName,
      label: actionName,
      body: args,
      kind,
    });
  }

  if (messageType === "ResultMessage" || message.role === "tool") {
    const actionName = (message as any)?.actionName ?? (message as any)?.name ?? "Tool result";
    const body =
      (message as any)?.result !== undefined ? (message as any)?.result : (message as any)?.content;
    const maybeId = (message as any)?.id ?? (message as any)?.toolCallId ?? (message as any)?.parentId;
    const kind = isCustomIdentifier(maybeId) ? "custom" : "tool-result";
    return renderToolBlock({
      id: String((message as any)?.id ?? `${index}-tool-result`),
      name: actionName,
      label: actionName,
      body,
      kind,
    });
  }

  if (message.role === "assistant") {
    const messageId = String(message.id ?? index);
    const toolCalls = Array.isArray((message as any)?.toolCalls)
      ? ((message as any)?.toolCalls as any[])
      : [];

    return (
      <Fragment key={messageId}>
        <AssistantMessage
          data-message-role="assistant"
          subComponent={(message as any)?.generativeUI?.()}
          rawData={message}
          message={message as any}
          isLoading={inProgress && isCurrentMessage && !message.content}
          isGenerating={inProgress && isCurrentMessage && !!message.content}
          isCurrentMessage={isCurrentMessage}
          onRegenerate={message.id ? () => onRegenerate?.(String(message.id)) : undefined}
          onCopy={onCopy}
          onThumbsUp={onThumbsUp}
          onThumbsDown={onThumbsDown}
          markdownTagRenderers={markdownTagRenderers}
          ImageRenderer={ImageRenderer}
        />
        {toolCalls.map((call, callIndex) => {
          const identifier = String(call?.id ?? `${messageId}-call-${callIndex}`);
          const callName = call?.function?.name ?? call?.name ?? "Tool call";
          const callArgs = call?.function?.arguments ?? call?.arguments ?? {};
          const callKind = isCustomIdentifier(call?.id) ? "custom" : "tool-call";
          return renderToolBlock({
            id: identifier,
            name: callName,
            label: callName,
            body: callArgs,
            kind: callKind,
          });
        })}
      </Fragment>
    );
  }

  if (message.role === "user") {
    return (
      <UserMessage
        key={message.id ?? index}
        data-message-role="user"
        rawData={message}
        message={message as any}
        ImageRenderer={ImageRenderer}
      />
    );
  }

  return null;
};

export default function Home() {
  return (
    <main className="agui-chat">
      <CopilotChat
        className="agui-chat__panel"
        Messages={DocumentAwareMessages}
        RenderMessage={ToolAwareRenderMessage}
        Input={PromptInput}
        labels={{
          placeholder: DEFAULT_PROMPT,
        }}
      />
    </main>
  );
}

const DocumentAwareMessages = ({
  messages: _messages,
  inProgress,
  children,
  RenderMessage,
  AssistantMessage,
  UserMessage,
  ErrorMessage,
  ImageRenderer,
  onRegenerate,
  onCopy,
  onThumbsUp,
  onThumbsDown,
  markdownTagRenderers,
  chatError,
  RenderTextMessage,
  RenderActionExecutionMessage,
  RenderAgentStateMessage,
  RenderResultMessage,
  RenderImageMessage,
}: MessagesProps) => {
  void _messages;
  const { labels } = useChatContext();
  const { messages: visibleMessages, interrupt } = useCopilotChat();
  const initialMessages = useMemo(() => makeInitialMessages(labels.initial), [labels.initial]);
  const combinedMessages = useMemo<Message[]>(
    () => [...initialMessages, ...visibleMessages],
    [initialMessages, visibleMessages],
  );
  const { instances, hiddenMessages } = useMemo(() => deriveDocumentState(visibleMessages), [visibleMessages]);
  const { messagesContainerRef, messagesEndRef } = useScrollToBottom(combinedMessages);
  const [expandedDocumentId, setExpandedDocumentId] = useState<string | null>(null);

  useEffect(() => {
    if (instances.length === 0) {
      setExpandedDocumentId(null);
      return;
    }
    if (expandedDocumentId) {
      const stillPresent = instances.some((inst) => inst.session.documentId === expandedDocumentId);
      if (!stillPresent) {
        setExpandedDocumentId(null);
      }
    }
  }, [instances, expandedDocumentId]);

  void RenderTextMessage;
  void RenderActionExecutionMessage;
  void RenderAgentStateMessage;
  void RenderResultMessage;
  void RenderImageMessage;

  const MessageRenderer = RenderMessage;

  const panelInstance =
    expandedDocumentId && instances.find((inst) => inst.session.documentId === expandedDocumentId);
  const panelSession = panelInstance?.session ?? null;
  const panelOpen = !!panelSession;

  return (
    <div className="agui-chat-layout">
      <div className="agui-chat-layout__messages" ref={messagesContainerRef}>
        <div className="copilotKitMessages">
          <div className="copilotKitMessagesContainer">
            {combinedMessages.map((message, index) => {
            if (hiddenMessages.has(message)) {
              return null;
            }
            const messageKey = message.id ?? index;
            const isCurrentMessage = index === combinedMessages.length - 1;
            const instanceForAnchor = instances.find((inst) => inst.anchor === message);

              return (
                <Fragment key={messageKey}>
                  <MessageRenderer
                    message={message}
                    inProgress={inProgress}
                    index={index}
                    isCurrentMessage={isCurrentMessage}
                    AssistantMessage={AssistantMessage}
                    UserMessage={UserMessage}
                    ImageRenderer={ImageRenderer}
                    onRegenerate={onRegenerate}
                    onCopy={onCopy}
                    onThumbsUp={onThumbsUp}
                    onThumbsDown={onThumbsDown}
                    markdownTagRenderers={markdownTagRenderers}
                  />
              {instanceForAnchor ? (
                <DocumentAttachment
                  session={instanceForAnchor.session}
                  isOpen={
                    panelOpen && panelSession?.documentId === instanceForAnchor.session.documentId
                  }
                  onToggle={() =>
                    setExpandedDocumentId((current) =>
                      current === instanceForAnchor.session.documentId
                        ? null
                        : instanceForAnchor.session.documentId,
                    )
                  }
                />
              ) : null}
                </Fragment>
              );
            })}
            {interrupt}
            {chatError && ErrorMessage && <ErrorMessage error={chatError} isCurrentMessage />}
          </div>
          <footer className="copilotKitMessagesFooter" ref={messagesEndRef}>
            {children}
          </footer>
        </div>
      </div>
      <DocumentSidePanel session={panelSession} isOpen={panelOpen} onClose={() => setExpandedDocumentId(null)} />
    </div>
  );
};

function makeInitialMessages(initial: string | string[] | undefined): Message[] {
  if (!initial) return [];

  if (Array.isArray(initial)) {
    return initial.map((message) => {
      return {
        id: message,
        role: "assistant",
        content: message,
      };
    });
  }

  return [
    {
      id: initial,
      role: "assistant",
      content: initial,
    },
  ];
}

function useScrollToBottom(messages: Message[]) {
  const messagesEndRef = useRef<HTMLDivElement>(null);
  const messagesContainerRef = useRef<HTMLDivElement | null>(null);
  const isProgrammaticScrollRef = useRef(false);
  const isUserScrollUpRef = useRef(false);

  const scrollToBottom = () => {
    if (messagesContainerRef.current && messagesEndRef.current) {
      isProgrammaticScrollRef.current = true;
      messagesContainerRef.current.scrollTop = messagesContainerRef.current.scrollHeight;
    }
  };

  const handleScroll = () => {
    if (isProgrammaticScrollRef.current) {
      isProgrammaticScrollRef.current = false;
      return;
    }

    if (messagesContainerRef.current) {
      const { scrollTop, scrollHeight, clientHeight } = messagesContainerRef.current;
      isUserScrollUpRef.current = scrollTop + clientHeight < scrollHeight;
    }
  };

  useEffect(() => {
    const container = messagesContainerRef.current;
    if (container) {
      container.addEventListener("scroll", handleScroll);
    }
    return () => {
      if (container) {
        container.removeEventListener("scroll", handleScroll);
      }
    };
  }, []);

  useEffect(() => {
    const container = messagesContainerRef.current;
    if (!container) {
      return;
    }

    const mutationObserver = new MutationObserver(() => {
      if (!isUserScrollUpRef.current) {
        scrollToBottom();
      }
    });

    mutationObserver.observe(container, {
      childList: true,
      subtree: true,
      characterData: true,
    });

    return () => {
      mutationObserver.disconnect();
    };
  }, []);

  useEffect(() => {
    isUserScrollUpRef.current = false;
    scrollToBottom();
  }, [messages.length]);

  return { messagesEndRef, messagesContainerRef };
}

type ReportSession = {
  status: "open" | "closed";
  title: string;
  documentId: string;
  createdAt?: string;
  closedAt?: string;
  reason?: string;
  content: string;
};

type ReportInstance = {
  session: ReportSession;
  anchor: Message;
};

type DocumentComputation = {
  instances: ReportInstance[];
  hiddenMessages: Set<Message>;
};

function deriveDocumentState(messages: Message[]): DocumentComputation {
  const hiddenMessages = new Set<Message>();
  const toolCallNameById = new Map<string, string>();
  const instances: ReportInstance[] = [];
  let activeInstance: ReportInstance | null = null;

  for (const message of messages) {
    if (message.role === "assistant") {
      const toolCalls = getToolCalls(message);
      toolCalls.forEach((call: any) => {
        if (call?.id) {
          const fnName = call?.function?.name || call?.name;
          if (fnName) {
            toolCallNameById.set(call.id, fnName);
          }
        }
      });
      if (activeInstance && activeInstance.session.status === "open" && toolCalls.length === 0) {
        const text = extractTextFromMessage(message);
        if (text && text.trim().length > 0) {
          hiddenMessages.add(message);
          const current = activeInstance.session;
          current.content = current.content ? `${current.content}\n${text}` : text;
        }
      }
      continue;
    }

    if (message.role === "tool") {
      const callId = (message as any)?.toolCallId ?? (message as any)?.parentId;
      if (!callId) {
        continue;
      }
      const toolName = toolCallNameById.get(callId);
      if (!toolName) {
        continue;
      }
      if (toolName === "open_report_document") {
        const payload = parseReportPayload((message as any)?.content);
        const session: ReportSession = {
          status: "open",
          title: payload.title || "Report",
          documentId: payload.documentId || callId,
          createdAt: payload.createdAt || payload.openedAt || new Date().toISOString(),
          content: "",
        };
        activeInstance = { session, anchor: message };
        instances.push(activeInstance);
      } else if (toolName === "close_report_document" && instances.length > 0) {
        const payload = parseReportPayload((message as any)?.content);
        const target = activeInstance ?? instances[instances.length - 1];
        target.session.status = "closed";
        target.session.closedAt = payload.closedAt || payload.timestamp || new Date().toISOString();
        if (payload.message || payload.reason) {
          target.session.reason = payload.message || payload.reason;
        }
        activeInstance = null;
      }
    }
  }

  return { instances, hiddenMessages };
}

const DocumentAttachment = ({
  session,
  isOpen,
  onToggle,
}: {
  session: ReportSession;
  isOpen: boolean;
  onToggle: () => void;
}) => {
  const timestampValue = session.createdAt
    ? formatTimestamp(session.createdAt)
    : formatTimestamp(new Date().toISOString());
  const isGenerating = session.status === "open";
  const stateLabel = isGenerating ? "Generating…" : "Report ready";
  const stateClass = isGenerating
    ? "report-document-attachment__state report-document-attachment__state--pending"
    : "report-document-attachment__state report-document-attachment__state--ready";
  return (
    <section className="report-document-attachment">
      <button className="report-document-attachment__button" type="button" onClick={onToggle}>
        <span className="report-document-attachment__icon" aria-hidden>
          📄
        </span>
        <div className="report-document-attachment__details">
          <p className="report-document-attachment__label">Generated document</p>
          <p className="report-document-attachment__title">{session.title}</p>
          <p className="report-document-attachment__meta">
            {timestampValue} · {session.documentId}
          </p>
          <span className={stateClass}>{stateLabel}</span>
        </div>
        <span className="report-document-attachment__action">{isOpen ? "Hide" : "Open"}</span>
      </button>
      {isOpen ? <span className="sr-only">Document preview open</span> : null}
    </section>
  );
};

const DocumentSidePanel = ({
  session,
  isOpen,
  onClose,
}: {
  session: ReportSession | null;
  isOpen: boolean;
  onClose: () => void;
}) => {
  return (
    <aside className={`document-side-panel${isOpen ? " document-side-panel--open" : ""}`}>
      {session && isOpen ? (
        <div className="document-side-panel__content">
          <DocumentBox session={session} onClose={onClose} />
        </div>
      ) : null}
    </aside>
  );
};

const DocumentBox = ({ session, onClose }: { session: ReportSession; onClose?: () => void }) => {
  if (!session) {
    return null;
  }
  const isOpen = session.status === "open";
  const statusLabel = isOpen ? "Live" : "Finished";
  const timestampLabel = isOpen ? "Opened" : "Finished";
  const timestampValue = isOpen ? session.createdAt : session.closedAt ?? session.createdAt;
  const content =
    session.content && session.content.trim().length > 0
      ? session.content
      : isOpen
        ? "Waiting for report content..."
        : "Report completed.";

  return (
    <section className={`report-document report-document--${session.status}`}>
      <header className="report-document__header">
        <div>
          <p className="report-document__label">Document</p>
          <h2 className="report-document__title">{session.title}</h2>
        </div>
        {onClose ? (
          <button className="report-document__close" type="button" onClick={onClose}>
            ×
          </button>
        ) : null}
        <span className="report-document__status" data-open={isOpen}>
          {statusLabel}
        </span>
      </header>
      <div className="report-document__meta">
        <span>Doc ID: {session.documentId}</span>
        {timestampValue && <span>{timestampLabel}: {formatTimestamp(timestampValue)}</span>}
        {!isOpen && session.reason && <span>Reason: {session.reason}</span>}
      </div>
      <div className="report-document__content">
        <ReactMarkdown>{content}</ReactMarkdown>
      </div>
    </section>
  );
};

function formatTimestamp(value: string) {
  try {
    const date = new Date(value);
    if (Number.isNaN(date.getTime())) {
      return value;
    }
    return new Intl.DateTimeFormat(undefined, {
      dateStyle: "medium",
      timeStyle: "short",
    }).format(date);
  } catch {
    return value;
  }
}

function parseReportPayload(value: unknown): Record<string, any> {
  if (!value) {
    return {};
  }
  if (typeof value === "string") {
    try {
      return JSON.parse(value);
    } catch {
      return {};
    }
  }
  if (typeof value === "object") {
    return value as Record<string, any>;
  }
  return {};
}

function getToolCalls(message: Message): any[] {
  const raw = (message as any)?.toolCalls;
  return Array.isArray(raw) ? raw : [];
}

function extractTextFromMessage(message: Message): string {
  const rawContent = (message as any)?.content;
  if (typeof rawContent === "string") {
    return rawContent;
  }
  if (Array.isArray(rawContent)) {
    return rawContent
      .map((part) => {
        if (typeof part === "string") {
          return part;
        }
        if (typeof part?.text === "string") {
          return part.text;
        }
        if (typeof part?.content === "string") {
          return part.content;
        }
        if (typeof part?.text?.value === "string") {
          return part.text.value;
        }
        return "";
      })
      .join("");
  }
  if (rawContent && typeof rawContent === "object" && typeof rawContent.text === "string") {
    return rawContent.text;
  }
  return "";
}
