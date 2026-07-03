"""LangGraph sports recap workflow used by the remote PromptIter example."""

from __future__ import annotations

import os

from langchain_core.messages import AnyMessage, HumanMessage, SystemMessage
from langchain_openai import ChatOpenAI
from langgraph.graph import END, START, StateGraph
from typing_extensions import TypedDict


APP_NAME = "promptiter-sports-recap-agent"
MODEL_NAME = os.getenv("LANGGRAPH_MODEL", "deepseek-v3.2")
MAX_OUTPUT_TOKENS = 32768
STRUCTURE_ID = f"{APP_NAME}:langgraph:v1"

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


class SportsRecapState(TypedDict, total=False):
    game_json: str
    headline: str
    highlights: str
    stats_angle: str
    writer_input: str
    draft: str
    editor_input: str
    final_recap: str


def node_id(name: str) -> str:
    return f"{APP_NAME}/{name}"


def surface_id(name: str) -> str:
    return f"{node_id(name)}#instruction"


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


def message_content(message: AnyMessage) -> str:
    content = getattr(message, "content", "")
    if isinstance(content, str):
        return content
    if isinstance(content, list):
        return "\n".join(part.get("text", "") for part in content if isinstance(part, dict))
    return str(content)


def new_model() -> ChatOpenAI:
    return ChatOpenAI(
        model=MODEL_NAME,
        temperature=0.0,
        max_completion_tokens=MAX_OUTPUT_TOKENS,
        api_key=os.getenv("OPENAI_API_KEY"),
        base_url=os.getenv("OPENAI_BASE_URL") or None,
    )


def build_graph(profile: dict | None):
    model = new_model()

    def stage(agent_name: str, text: str) -> str:
        response = model.invoke(
            [
                SystemMessage(content=instruction_for(agent_name, profile)),
                HumanMessage(content=text),
            ]
        )
        return message_content(response).strip()

    def prepare_game_input(state: SportsRecapState):
        game_json = state.get("game_json", "")
        if not game_json.strip():
            raise ValueError("game input is empty")
        return {"game_json": game_json}

    def headline_agent(state: SportsRecapState):
        return {"headline": stage(HEADLINE_AGENT, state["game_json"])}

    def highlights_agent(state: SportsRecapState):
        return {"highlights": stage(HIGHLIGHTS_AGENT, state["game_json"])}

    def stats_angle_agent(state: SportsRecapState):
        return {"stats_angle": stage(STATS_ANGLE_AGENT, state["game_json"])}

    def join_recap_parts(state: SportsRecapState):
        writer_input = "\n\n".join(
            [
                "GAME_JSON:",
                state["game_json"],
                "HEADLINE:",
                state["headline"],
                "HIGHLIGHTS:",
                state["highlights"],
                "STATS_ANGLE:",
                state["stats_angle"],
            ]
        )
        return {"writer_input": writer_input}

    def recap_writer(state: SportsRecapState):
        return {"draft": stage(RECAP_WRITER_AGENT, state["writer_input"])}

    def sports_editor(state: SportsRecapState):
        editor_input = "\n\n".join(
            [
                "GAME_JSON:",
                state["game_json"],
                "HEADLINE:",
                state["headline"],
                "HIGHLIGHTS:",
                state["highlights"],
                "STATS_ANGLE:",
                state["stats_angle"],
                "DRAFT:",
                state["draft"],
            ]
        )
        final_recap = stage(SPORTS_EDITOR_AGENT, editor_input)
        return {"editor_input": editor_input, "final_recap": final_recap}

    graph = StateGraph(SportsRecapState)
    graph.add_node(NODE_PREPARE_GAME_INPUT, prepare_game_input)
    graph.add_node(HEADLINE_AGENT, headline_agent)
    graph.add_node(HIGHLIGHTS_AGENT, highlights_agent)
    graph.add_node(STATS_ANGLE_AGENT, stats_angle_agent)
    graph.add_node(NODE_JOIN_RECAP_PARTS, join_recap_parts)
    graph.add_node(RECAP_WRITER_AGENT, recap_writer)
    graph.add_node(SPORTS_EDITOR_AGENT, sports_editor)
    graph.add_edge(START, NODE_PREPARE_GAME_INPUT)
    graph.add_edge(NODE_PREPARE_GAME_INPUT, HEADLINE_AGENT)
    graph.add_edge(NODE_PREPARE_GAME_INPUT, HIGHLIGHTS_AGENT)
    graph.add_edge(NODE_PREPARE_GAME_INPUT, STATS_ANGLE_AGENT)
    graph.add_edge([HEADLINE_AGENT, HIGHLIGHTS_AGENT, STATS_ANGLE_AGENT], NODE_JOIN_RECAP_PARTS)
    graph.add_edge(NODE_JOIN_RECAP_PARTS, RECAP_WRITER_AGENT)
    graph.add_edge(RECAP_WRITER_AGENT, SPORTS_EDITOR_AGENT)
    graph.add_edge(SPORTS_EDITOR_AGENT, END)
    return graph.compile()


def run_agent(request: dict) -> tuple[str, list[dict]]:
    graph = build_graph(request.get("profile"))
    result = graph.invoke({"game_json": input_text(request.get("input") or {})})
    final = result["final_recap"].strip()
    return final, [
        function_step(NODE_PREPARE_GAME_INPUT, result["game_json"], result["game_json"], []),
        stage_step(HEADLINE_AGENT, result["game_json"], result["headline"], [NODE_PREPARE_GAME_INPUT]),
        stage_step(HIGHLIGHTS_AGENT, result["game_json"], result["highlights"], [NODE_PREPARE_GAME_INPUT]),
        stage_step(STATS_ANGLE_AGENT, result["game_json"], result["stats_angle"], [NODE_PREPARE_GAME_INPUT]),
        function_step(NODE_JOIN_RECAP_PARTS, result["writer_input"], result["writer_input"], [HEADLINE_AGENT, HIGHLIGHTS_AGENT, STATS_ANGLE_AGENT]),
        stage_step(RECAP_WRITER_AGENT, result["writer_input"], result["draft"], [NODE_JOIN_RECAP_PARTS]),
        stage_step(SPORTS_EDITOR_AGENT, result["editor_input"], final, [RECAP_WRITER_AGENT]),
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
