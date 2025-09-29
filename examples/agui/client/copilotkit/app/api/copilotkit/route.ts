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

const runtime = new CopilotRuntime({
  agents: {
    "agui-demo": new HttpAgent({
      agentId: "agui-demo",
      description: "AG-UI agent hosted by the Go evaluation server",
      threadId: "demo-thread",
      url: process.env.AG_UI_ENDPOINT ?? "http://127.0.0.1:8080/agui",
      headers: process.env.AG_UI_TOKEN
        ? { Authorization: `Bearer ${process.env.AG_UI_TOKEN}` }
        : undefined,
    }),
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
