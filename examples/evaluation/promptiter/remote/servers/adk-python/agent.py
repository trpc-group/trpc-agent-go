"""ADK sports recap workflow used by the remote PromptIter example."""

from __future__ import annotations

import asyncio
import inspect
import os
import threading
import uuid
from concurrent.futures import ThreadPoolExecutor

from google.adk.agents.llm_agent import LlmAgent
from google.adk.models.lite_llm import LiteLlm
from google.adk.runners import Runner
from google.adk.sessions import InMemorySessionService
from google.genai import types


APP_NAME = "promptiter-sports-recap-agent"
MODEL_NAME = os.getenv("ADK_MODEL", "deepseek-v3.2")
MAX_OUTPUT_TOKENS = 32768
STRUCTURE_ID = f"{APP_NAME}:adk-python:v1"
SESSION_SERVICE = InMemorySessionService()
SESSION_LOCK = threading.Lock()
KNOWN_SESSIONS: set[tuple[str, str]] = set()

HEADLINE_AGENT = "headline_agent"
HIGHLIGHTS_AGENT = "highlights_agent"
STATS_ANGLE_AGENT = "stats_angle_agent"
RECAP_WRITER_AGENT = "recap_writer"
SPORTS_EDITOR_AGENT = "sports_editor"
NODE_PREPARE_GAME_INPUT = "prepare_game_input"
NODE_JOIN_RECAP_PARTS = "join_recap_parts"

INSTRUCTIONS = {
    HEADLINE_AGENT: "生成标题。",
    HIGHLIGHTS_AGENT: "提取比赛高光。",
    STATS_ANGLE_AGENT: "选择数据角度。",
    RECAP_WRITER_AGENT: "生成中文战报。",
    SPORTS_EDITOR_AGENT: "润色中文战报。",
}

STAGE_AGENTS = [
    HEADLINE_AGENT,
    HIGHLIGHTS_AGENT,
    STATS_ANGLE_AGENT,
    RECAP_WRITER_AGENT,
    SPORTS_EDITOR_AGENT,
]


def node_id(name: str) -> str:
    return f"{APP_NAME}/{name}"


def surface_id(name: str) -> str:
    return f"{node_id(name)}#instruction"


def model_name() -> str:
    if "/" in MODEL_NAME:
        return MODEL_NAME
    return f"openai/{MODEL_NAME}"


def configure_openai_base() -> None:
    base_url = os.getenv("OPENAI_BASE_URL")
    if base_url and not os.getenv("OPENAI_API_BASE"):
        os.environ["OPENAI_API_BASE"] = base_url


def maybe_await(value):
    if inspect.isawaitable(value):
        return asyncio.run(value)
    return value


def ensure_session(user_id: str, session_id: str) -> None:
    key = (user_id, session_id)
    with SESSION_LOCK:
        if key in KNOWN_SESSIONS:
            return
        try:
            maybe_await(SESSION_SERVICE.create_session(app_name=APP_NAME, user_id=user_id, session_id=session_id))
        except Exception as exc:
            if "already" not in str(exc).lower() and "exist" not in str(exc).lower():
                raise
        KNOWN_SESSIONS.add(key)


def instruction_for(agent_name: str, profile: dict | None) -> str:
    if not profile:
        return INSTRUCTIONS[agent_name]
    expected_surface_id = surface_id(agent_name)
    for override in profile.get("overrides") or []:
        if override.get("surfaceID") != expected_surface_id:
            continue
        value = override.get("value") or override.get("Value") or {}
        text = value.get("Text") or value.get("text")
        if isinstance(text, str) and text.strip():
            return text
    return INSTRUCTIONS[agent_name]


def input_text(message: dict) -> str:
    if isinstance(message.get("content"), str):
        return message["content"]
    parts = message.get("content_parts") or message.get("ContentParts") or []
    texts = []
    for part in parts:
        text = part.get("text") or part.get("Text")
        if text:
            texts.append(text)
    return "\n".join(texts)


def run_stage(agent_name: str, instruction: str, text: str, user_id: str, session_id: str) -> str:
    stage_session_id = f"{session_id}:{agent_name}"
    ensure_session(user_id, stage_session_id)
    configure_openai_base()
    agent = LlmAgent(
        name=agent_name,
        model=LiteLlm(model=model_name()),
        instruction=instruction,
        generate_content_config=types.GenerateContentConfig(
            max_output_tokens=MAX_OUTPUT_TOKENS,
            temperature=0.0,
        ),
    )
    runner = Runner(agent=agent, app_name=APP_NAME, session_service=SESSION_SERVICE)
    content = types.Content(role="user", parts=[types.Part(text=text)])
    final_text = ""
    for event in runner.run(user_id=user_id, session_id=stage_session_id, new_message=content):
        if event.is_final_response() and event.content and event.content.parts:
            final_text = (event.content.parts[0].text or "").strip()
    return final_text


def run_agent(request: dict) -> tuple[str, list[dict]]:
    session = request.get("session") or {}
    user_id = session.get("userId") or "promptiter"
    session_id = session.get("sessionId") or str(uuid.uuid4())
    profile = request.get("profile")
    game_json = input_text(request.get("input") or {})
    if not game_json.strip():
        raise ValueError("game input is empty")
    with ThreadPoolExecutor(max_workers=3) as executor:
        headline_future = executor.submit(
            run_stage,
            HEADLINE_AGENT,
            instruction_for(HEADLINE_AGENT, profile),
            game_json,
            user_id,
            session_id,
        )
        highlights_future = executor.submit(
            run_stage,
            HIGHLIGHTS_AGENT,
            instruction_for(HIGHLIGHTS_AGENT, profile),
            game_json,
            user_id,
            session_id,
        )
        stats_angle_future = executor.submit(
            run_stage,
            STATS_ANGLE_AGENT,
            instruction_for(STATS_ANGLE_AGENT, profile),
            game_json,
            user_id,
            session_id,
        )
        headline = headline_future.result()
        highlights = highlights_future.result()
        stats_angle = stats_angle_future.result()
    writer_input = "\n\n".join(
        [
            "GAME_JSON:",
            game_json,
            "HEADLINE:",
            headline,
            "HIGHLIGHTS:",
            highlights,
            "STATS_ANGLE:",
            stats_angle,
        ]
    )
    draft = run_stage(RECAP_WRITER_AGENT, instruction_for(RECAP_WRITER_AGENT, profile), writer_input, user_id, session_id)
    editor_input = "\n\n".join(
        [
            "GAME_JSON:",
            game_json,
            "HEADLINE:",
            headline,
            "HIGHLIGHTS:",
            highlights,
            "STATS_ANGLE:",
            stats_angle,
            "DRAFT:",
            draft,
        ]
    )
    final = run_stage(SPORTS_EDITOR_AGENT, instruction_for(SPORTS_EDITOR_AGENT, profile), editor_input, user_id, session_id)
    return final, [
        function_step(NODE_PREPARE_GAME_INPUT, game_json, game_json, []),
        stage_step(HEADLINE_AGENT, game_json, headline, [NODE_PREPARE_GAME_INPUT]),
        stage_step(HIGHLIGHTS_AGENT, game_json, highlights, [NODE_PREPARE_GAME_INPUT]),
        stage_step(STATS_ANGLE_AGENT, game_json, stats_angle, [NODE_PREPARE_GAME_INPUT]),
        function_step(NODE_JOIN_RECAP_PARTS, writer_input, writer_input, [HEADLINE_AGENT, HIGHLIGHTS_AGENT, STATS_ANGLE_AGENT]),
        stage_step(RECAP_WRITER_AGENT, writer_input, draft, [NODE_JOIN_RECAP_PARTS]),
        stage_step(SPORTS_EDITOR_AGENT, editor_input, final, [RECAP_WRITER_AGENT]),
    ]


def function_step(node_name: str, input_value: str, output_value: str, predecessors: list[str]) -> dict:
    return {
        "StepID": node_name,
        "AgentName": node_name,
        "NodeID": node_id(node_name),
        "PredecessorStepIDs": predecessors,
        "Input": {"Text": input_value},
        "Output": {"Text": output_value},
    }


def stage_step(agent_name: str, input_value: str, output_value: str, predecessors: list[str]) -> dict:
    return {
        "StepID": agent_name,
        "AgentName": agent_name,
        "NodeID": node_id(agent_name),
        "PredecessorStepIDs": predecessors,
        "AppliedSurfaceIDs": [surface_id(agent_name)],
        "Input": {"Text": input_value},
        "Output": {"Text": output_value},
    }
