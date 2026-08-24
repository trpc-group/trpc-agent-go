"""ADK travel agent used by the tRPC-Agent evaluation example."""

from __future__ import annotations

import asyncio
import os
import threading
import uuid
from typing import Any

from google.adk import Agent
from google.adk.models.lite_llm import LiteLlm
from google.adk.runners import Runner
from google.adk.sessions import InMemorySessionService
from google.genai import types


APP_NAME = "trpcagent-travel-agent"
ADK_AGENT_NAME = "trpcagent_travel_agent"
MODEL_NAME = os.getenv("ADK_MODEL", "openai/gpt-5.2")
MAX_OUTPUT_TOKENS = 512
STRUCTURE_ID = f"{APP_NAME}:adk:v1"
INSTRUCTION = """You are a concise travel assistant.
Use this scenario when answering: Shanghai is sunny at 26C, festival events will be held downtown with likely traffic disruptions, and museum tickets are available with 25 seats left.
Answer the user's travel question with weather, alert, ticket availability, and practical advice."""
SESSION_SERVICE = InMemorySessionService()
SESSION_LOCK = threading.Lock()
KNOWN_SESSIONS: set[tuple[str, str]] = set()
TRAVEL_AGENT = Agent(
    name=ADK_AGENT_NAME,
    description="Travel agent used by the tRPC-Agent evaluation example.",
    instruction=INSTRUCTION,
    model=LiteLlm(model=MODEL_NAME),
    generate_content_config=types.GenerateContentConfig(
        max_output_tokens=MAX_OUTPUT_TOKENS,
        temperature=0.0,
    ),
)


def run_agent(request: dict) -> tuple[str, dict | None]:
    user_id = (request.get("session") or {}).get("userId") or "trpcagent"
    session_id = session_id_from(request)
    ensure_session(user_id, session_id)
    runner = Runner(agent=TRAVEL_AGENT, app_name=APP_NAME, session_service=SESSION_SERVICE)
    message = types.Content(role="user", parts=[types.Part(text=input_text(request))])
    final_text = ""
    usage = None
    for event in runner.run(user_id=user_id, session_id=session_id, new_message=message):
        if current_usage := event_usage(event):
            usage = current_usage
        if event.is_final_response() and event.content and event.content.parts:
            final_text = parts_text(event.content.parts).strip()
    if not final_text:
        raise RuntimeError("agent did not return a final response")
    return final_text, usage


def request_id_from(request: dict) -> str:
    run_options = request.get("runOptions") or {}
    return run_options.get("requestID") or run_options.get("requestId") or str(uuid.uuid4())


def session_id_from(request: dict) -> str:
    session = request.get("session") or {}
    return session.get("sessionId") or request_id_from(request)


def ensure_session(user_id: str, session_id: str) -> None:
    key = (user_id, session_id)
    with SESSION_LOCK:
        if key in KNOWN_SESSIONS:
            return
        asyncio.run(SESSION_SERVICE.create_session(app_name=APP_NAME, user_id=user_id, session_id=session_id))
        KNOWN_SESSIONS.add(key)


def input_text(request: dict) -> str:
    return ((request.get("input") or {}).get("content") or "").strip()


def parts_text(parts: list[Any]) -> str:
    return "\n".join(getattr(part, "text", "") or "" for part in parts)


def event_usage(event: Any) -> dict | None:
    return usage_from_metadata(getattr(event, "usage_metadata", None))


def usage_from_metadata(metadata: Any) -> dict | None:
    if metadata is None:
        return None
    prompt_tokens = token_count(metadata, "prompt_token_count", "input_tokens", "prompt_tokens")
    completion_tokens = token_count(
        metadata,
        "candidates_token_count",
        "output_tokens",
        "completion_tokens",
    )
    total_tokens = token_count(metadata, "total_token_count", "total_tokens")
    if total_tokens == 0 and (prompt_tokens or completion_tokens):
        total_tokens = prompt_tokens + completion_tokens
    if prompt_tokens == 0 and completion_tokens == 0 and total_tokens == 0:
        return None
    return {
        "prompt_tokens": prompt_tokens,
        "completion_tokens": completion_tokens,
        "total_tokens": total_tokens,
        "prompt_tokens_details": {
            "cached_tokens": token_count(metadata, "cached_content_token_count", "cached_tokens"),
        },
        "completion_tokens_details": {},
    }


def token_count(source: Any, *names: str) -> int:
    for name in names:
        value = source.get(name) if isinstance(source, dict) else getattr(source, name, None)
        if value is None:
            continue
        try:
            return int(value)
        except (TypeError, ValueError):
            continue
    return 0
