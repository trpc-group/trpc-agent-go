//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

import { randomUUID } from "crypto";
import { NextRequest } from "next/server";
import {
  CopilotRuntime,
  ExperimentalEmptyAdapter,
  copilotRuntimeNextJSAppRouterEndpoint,
} from "@copilotkit/runtime";
import { HttpAgent } from "@ag-ui/client";
import type { BaseEvent, CustomEvent, Message, RunAgentInput } from "@ag-ui/client";
// Do not import Observable from rxjs here to avoid multi-version typing conflicts.

const CUSTOM_EVENT_MESSAGE_TYPE = "AguiCustomEventMessage";

function safeStringify(value: unknown) {
  try {
    return JSON.stringify(value);
  } catch {
    return undefined;
  }
}

function buildCustomEventMessage(event: CustomEvent, messages: Message[]) {
  const nextValueSignature = safeStringify(event.value);
  const existing = messages.find((message: any) => {
    const payload = (message as any)?.customEvent;
    const currentValueSignature = safeStringify(payload?.value);
    return (
      (message as any)?.type === CUSTOM_EVENT_MESSAGE_TYPE &&
      payload?.name === event.name &&
      payload?.timestamp === event.timestamp &&
      currentValueSignature === nextValueSignature
    );
  });
  if (existing) {
    return undefined;
  }
  const baseId = event.timestamp ? `${event.name}-${event.timestamp}` : `${event.name}-${Date.now()}`;
  const messageId = `custom-${baseId}-${randomUUID()}`;
  return {
    id: messageId,
    role: "assistant",
    name: event.name,
    content: safeStringify(event.value) ?? "",
    type: CUSTOM_EVENT_MESSAGE_TYPE,
    customEvent: {
      name: event.name,
      value: event.value,
      timestamp: event.timestamp,
    },
  } as Message & {
    type: string;
    customEvent: {
      name: string;
      value: unknown;
      timestamp?: number;
    };
  };
}

function createCustomEventSubscriber(): any {
  let pending: Message[] = [];
  let seenIds = new Set<string>();

  const reset = () => {
    pending = [];
    seenIds = new Set<string>();
  };

  const remember = (message: Message) => {
    if (seenIds.has(message.id)) {
      return false;
    }
    seenIds.add(message.id);
    pending = [...pending, message];
    return true;
  };

  return {
    onRunInitialized: () => {
      console.log("[agui] subscriber reset");
      reset();
    },
    onCustomEvent: ({ event, messages }: { event: CustomEvent; messages: Message[] }) => {
      console.log("[agui] onCustomEvent", { name: event.name });
      const baseline = messages.concat(pending);
      const customMessage = buildCustomEventMessage(event, baseline);
      if (!customMessage) {
        return;
      }
      if (!remember(customMessage)) {
        return;
      }
      return {
        messages: [...messages, customMessage],
      };
    },
    onRunFinalized: ({ messages }: { messages: Message[] }) => {
      console.log("[agui] onRunFinalized", { pending: pending.length, messages: messages.length });
      if (pending.length === 0) {
        return;
      }
      const existingIds = new Set(messages.map((message) => message.id));
      const merged = messages.slice();
      for (const message of pending) {
        if (!existingIds.has(message.id)) {
          merged.push(message);
        }
      }
      console.log("[agui] merging custom events", { added: merged.length - messages.length });
      reset();
      return { messages: merged };
    },
  };
}

class CustomEventMirroringAgent extends HttpAgent {
  // Use ReturnType<HttpAgent["run"]> to align with the exact Observable type of the base class.
  run(input: RunAgentInput): ReturnType<HttpAgent["run"]> {
    const upstream = super.run(input) as ReturnType<HttpAgent["run"]>;
    // Construct a new Observable using the same constructor as upstream to ensure runtime compatibility.
    return new (upstream as any).constructor((subscriber: any) => {
      const sub = (upstream as any).subscribe({
        next: (event: any) => {
          if (event && event.type === "CUSTOM") {
            try {
              const name = event.name || "custom";
              const baseId = event.timestamp ? `${name}-${event.timestamp}` : `${name}-${Date.now()}`;
              const toolCallId = `custom-${baseId}`;
              const parentMessageId = (event.value && (event.value as any).messageId) || undefined;
              const args = JSON.stringify((event.value ?? {}), null, 0);
              const now = Date.now();
              const startEvt: any = {
                type: "TOOL_CALL_START",
                timestamp: now,
                toolCallId,
                toolCallName: name,
                ...(parentMessageId ? { parentMessageId } : {}),
              };
              const argsEvt: any = {
                type: "TOOL_CALL_ARGS",
                timestamp: now,
                toolCallId,
                delta: args,
              };
              const endEvt: any = {
                type: "TOOL_CALL_END",
                timestamp: now,
                toolCallId,
              };
              subscriber.next(startEvt as BaseEvent);
              subscriber.next(argsEvt as BaseEvent);
              subscriber.next(endEvt as BaseEvent);
            } catch {}
          }
          subscriber.next(event as BaseEvent);
        },
        error: (err: any) => subscriber.error(err),
        complete: () => subscriber.complete(),
      });
      return () => sub.unsubscribe();
    });
  }
}

const aguiAgent = new CustomEventMirroringAgent({
  agentId: "agui-demo",
  description: "AG-UI agent hosted by the Go evaluation server",
  threadId: "demo-thread",
  url: process.env.AG_UI_ENDPOINT ?? "http://127.0.0.1:8080/agui",
  headers: process.env.AG_UI_TOKEN ? { Authorization: `Bearer ${process.env.AG_UI_TOKEN}` } : undefined,
});

const runtime = new CopilotRuntime({
  agents: {
    "agui-demo": aguiAgent,
  },
});

const { handleRequest } = copilotRuntimeNextJSAppRouterEndpoint({
  runtime,
  serviceAdapter: new ExperimentalEmptyAdapter(),
  endpoint: "/api/copilotkit",
});

export async function POST(request: NextRequest) {
  return handleRequest(request);
}
