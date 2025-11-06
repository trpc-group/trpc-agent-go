//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

import { HttpAgent } from "@ag-ui/client";
import { v4 as uuidv4 } from "uuid";

async function main() {
  const chatUrl = process.env.AG_UI_ENDPOINT ?? "http://127.0.0.1:8080/agui";
  const historyUrl = process.env.AG_UI_HISTORY_ENDPOINT ?? "http://127.0.0.1:8080/history";
  const userId = process.env.AG_UI_USER_ID ?? "demo-user";
  const prompt = process.env.AG_UI_PROMPT ?? "Please help me calculate 2*(10+11), explain the process, then calculate, and give the final conclusion.";
  const threadId = process.env.AG_UI_THREAD_ID ?? `thread-${Date.now()}`;

  const chatAgent = new HttpAgent({
    url: chatUrl,
    threadId,
    initialMessages: [
      {
        id: uuidv4(),
        role: "user",
        content: prompt,
      },
    ],
  });

  console.log(`⚙️ Send chat request to -> ${chatUrl}`);
  const chatResult = await chatAgent.runAgent({
    forwardedProps: {
      userId,
    },
  });

  chatResult.newMessages.forEach((message) => {
    if (message.role === "assistant") {
      console.log(`🤖 assistant: ${message.content ?? ""}`);
    }
    if (message.role === "tool") {
      console.log(
        `🛠️ tool(${message.toolCallId ?? "unknown"}): ${message.content ?? ""}`,
      );
    }
  });

  const historyAgent = new HttpAgent({
    url: historyUrl,
    threadId,
  });

  console.log(`⚙️ Load history -> ${historyUrl}`);
  await historyAgent.runAgent({
    forwardedProps: {
      userId,
    },
  });

  historyAgent.messages.forEach((message) => {
    if (message.role === "assistant") {
      console.log(`🤖 assistant: ${message.content ?? ""}`);
      return;
    }
    if (message.role === "tool") {
      console.log(
        `🛠️ tool(${message.toolCallId ?? "unknown"}): ${message.content ?? ""}`,
      );
      return;
    }
    if (message.role === "user") {
      const sender = message.name ?? userId;
      console.log(`👤 user(${sender}): ${message.content ?? ""}`);
      return;
    }
    console.log(`❓ ${message.role}: ${message.content ?? ""}`);
  });

  console.log(`threadId=${threadId}, userId=${userId}`);
}

main().catch((error) => {
  console.error("Failed to run:", error);
  process.exit(1);
});
