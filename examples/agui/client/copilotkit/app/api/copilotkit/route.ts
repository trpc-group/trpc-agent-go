//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

import { NextRequest } from "next/server";
import {
  CopilotRuntime,
  ExperimentalEmptyAdapter,
  copilotRuntimeNextJSAppRouterEndpoint,
} from "@copilotkit/runtime";
import { HttpAgent } from "@ag-ui/client";
import type { BaseEvent, RunAgentInput } from "@ag-ui/client";

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
