//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

"use client";

import { Fragment, useLayoutEffect, useRef, useState } from "react";
import type { InputProps, RenderMessageProps } from "@copilotkit/react-ui";
import {
  AssistantMessage as DefaultAssistantMessage,
  CopilotChat,
  ImageRenderer as DefaultImageRenderer,
  UserMessage as DefaultUserMessage,
  useChatContext,
} from "@copilotkit/react-ui";

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
        RenderMessage={ToolAwareRenderMessage}
        Input={PromptInput}
        labels={{
          placeholder: DEFAULT_PROMPT,
        }}
      />
    </main>
  );
}
