"""LangGraph travel agent used by the tRPC-Agent evaluation example."""

from __future__ import annotations

import os
from typing import Any

from langchain_core.messages import HumanMessage, SystemMessage
from langchain_openai import ChatOpenAI
from langgraph.graph import END, START, StateGraph
from typing_extensions import TypedDict


APP_NAME = "trpcagent-travel-agent"
MODEL_NAME = os.getenv("LANGGRAPH_MODEL", "gpt-5.2")
MAX_OUTPUT_TOKENS = 512
STRUCTURE_ID = f"{APP_NAME}:langgraph:v1"
INSTRUCTION = """You are a concise travel assistant.
Use this scenario when answering: Shanghai is sunny at 26C, festival events will be held downtown with likely traffic disruptions, and museum tickets are available with 25 seats left.
Answer the user's travel question with weather, alert, ticket availability, and practical advice."""


class TravelState(TypedDict, total=False):
    user_input: str
    final_response: str
    usage: dict


def build_graph():
    model = ChatOpenAI(
        model=MODEL_NAME,
        temperature=0.0,
        max_completion_tokens=MAX_OUTPUT_TOKENS,
    )

    def answer(state: TravelState) -> dict:
        response = model.invoke(
            [
                SystemMessage(content=INSTRUCTION),
                HumanMessage(content=state.get("user_input", "")),
            ]
        )
        return {"final_response": str(response.content).strip(), "usage": usage_from_message(response)}

    graph = StateGraph(TravelState)
    graph.add_node("answer", answer)
    graph.add_edge(START, "answer")
    graph.add_edge("answer", END)
    return graph.compile()


def run_agent(request: dict) -> tuple[str, dict | None]:
    result = build_graph().invoke({"user_input": input_text(request)})
    final = result.get("final_response", "").strip()
    if not final:
        raise RuntimeError("agent did not return a final response")
    return final, result.get("usage")


def input_text(request: dict) -> str:
    return ((request.get("input") or {}).get("content") or "").strip()


def usage_from_message(message: Any) -> dict | None:
    metadata = getattr(message, "usage_metadata", None) or {}
    response_metadata = getattr(message, "response_metadata", None) or {}
    token_usage = response_metadata.get("token_usage") or response_metadata.get("usage") or {}
    prompt_tokens = token_count(metadata, "input_tokens", "prompt_tokens") or token_count(token_usage, "prompt_tokens")
    completion_tokens = token_count(metadata, "output_tokens", "completion_tokens") or token_count(
        token_usage,
        "completion_tokens",
    )
    total_tokens = token_count(metadata, "total_tokens") or token_count(token_usage, "total_tokens")
    if total_tokens == 0 and (prompt_tokens or completion_tokens):
        total_tokens = prompt_tokens + completion_tokens
    if prompt_tokens == 0 and completion_tokens == 0 and total_tokens == 0:
        return None
    return {
        "prompt_tokens": prompt_tokens,
        "completion_tokens": completion_tokens,
        "total_tokens": total_tokens,
        "prompt_tokens_details": {
            "cached_tokens": 0,
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
