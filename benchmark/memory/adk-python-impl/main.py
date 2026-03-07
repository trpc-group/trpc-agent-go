#!/usr/bin/env python3
"""
Memory evaluation benchmark for Google ADK Python.
Evaluates long-term conversational memory using the LoCoMo dataset.

Evaluation Scenarios:
  - baseline: Full conversation as context (no memory system).
  - memory: ADK InMemoryMemoryService + LoadMemoryTool.

Metrics (aligned with LoCoMo paper):
  - F1 Score: Token-level F1.
  - BLEU Score: N-gram overlap.
  - LLM-score: LLM-as-Judge evaluation.
"""

from __future__ import annotations

import argparse
import asyncio
import json
import logging
import os
import sys
import time
from dataclasses import asdict, dataclass, field
from pathlib import Path

import litellm
from openai import OpenAI

import dataset
import metrics

# ---------------------------------------------------------------------------
# ADK imports.
# ---------------------------------------------------------------------------
from google.adk.agents import LlmAgent
from google.adk.events import Event
from google.adk.memory import InMemoryMemoryService
from google.adk.models.lite_llm import LiteLlm
from google.adk.runners import Runner
from google.adk.sessions import InMemorySessionService, Session
from google.adk.tools.load_memory_tool import LoadMemoryTool
from google.genai import types

logging.basicConfig(
    level=logging.INFO,
    format="%(asctime)s %(levelname)s %(message)s",
)
log = logging.getLogger(__name__)

# Suppress noisy litellm and pydantic warnings.
import warnings
warnings.filterwarnings(
    "ignore",
    message="Pydantic serializer warnings",
    category=UserWarning,
)
logging.getLogger("LiteLLM").setLevel(logging.WARNING)
logging.getLogger("litellm").setLevel(logging.WARNING)


# ---------------------------------------------------------------------------
# LiteLLM callback tracker.
# ADK's Runner.run_async() returns ev.usage=None for memory-
# augmented calls.  We work around this by hooking litellm's
# CustomLogger callback to capture token usage from the
# underlying async LLM calls.
# ---------------------------------------------------------------------------
import threading

from litellm.integrations.custom_logger import CustomLogger


class _LiteLLMUsageTracker(CustomLogger):
    """Thread-safe accumulator reset before each QA question."""

    def __init__(self) -> None:
        super().__init__()
        self._lock = threading.Lock()
        self.prompt_tokens = 0
        self.completion_tokens = 0
        self.total_tokens = 0
        self.llm_calls = 0

    def reset(self) -> None:
        with self._lock:
            self.prompt_tokens = 0
            self.completion_tokens = 0
            self.total_tokens = 0
            self.llm_calls = 0

    def snapshot(self) -> "TokenUsage":
        with self._lock:
            return TokenUsage(
                prompt_tokens=self.prompt_tokens,
                completion_tokens=self.completion_tokens,
                total_tokens=self.total_tokens,
                llm_calls=self.llm_calls,
            )

    async def async_log_success_event(
        self, kwargs, response_obj, start_time, end_time,
    ):
        """Async callback fired by litellm after acompletion."""
        usage = getattr(response_obj, "usage", None)
        if usage is None:
            return
        pt = getattr(usage, "prompt_tokens", 0) or 0
        ct = getattr(usage, "completion_tokens", 0) or 0
        tt = getattr(usage, "total_tokens", 0) or 0
        with self._lock:
            self.prompt_tokens += pt
            self.completion_tokens += ct
            self.total_tokens += tt
            self.llm_calls += 1


_usage_tracker = _LiteLLMUsageTracker()
litellm.callbacks = [_usage_tracker]


# ---------------------------------------------------------------------------
# Constants.
# ---------------------------------------------------------------------------
NOT_AVAILABLE = "The information is not available."
APP_NAME = "memory_eval"

# Long-context prompt (aligned with Go implementation).
_LONG_CONTEXT_PROMPT = """\
You are an intelligent memory assistant tasked with retrieving \
accurate information from a conversation transcript.

# CONTEXT:
You have access to the full conversation transcript between speakers.
The transcript contains timestamped sessions that may be relevant \
to answering the question.

# INSTRUCTIONS:
1. Carefully analyze the entire conversation transcript.
2. Pay special attention to the SessionDate lines to determine \
when events occurred.
3. If the question asks about a specific event or fact, look for \
direct evidence in the transcript.
4. If the transcript contains contradictory information, prioritize \
the most recent information.
5. If there is a question about time references (like "last year", \
"two months ago", etc.), calculate the actual date based on the \
SessionDate. For example, if a session from 4 May 2022 mentions \
"went to India last year", then the trip occurred in 2021.
6. CRITICAL: Always convert relative time references to ABSOLUTE \
dates, months, or years.
   - "last year" -> "2022" (not "Last year")
   - "this month" -> "July 2023" (not "This month")
   - "next month" -> "August 2023" (not "Next month")
   NEVER output relative time words as the answer.
7. Focus only on the content of the transcript. Do not confuse \
character names mentioned in the transcript with real-world \
individuals.
8. The answer should be less than 5-6 words.
9. If the answer cannot be found in the transcript, reply with \
"{not_available}" exactly.

# APPROACH (Think step by step):
1. Examine all parts of the transcript related to the question.
2. Examine the SessionDate and content carefully.
3. Look for explicit mentions of dates, times, locations, events.
4. If the answer requires calculation, show your work.
5. Formulate a precise, concise answer based solely on the evidence.
6. Double-check that your answer directly addresses the question.
7. Ensure your final answer uses ABSOLUTE dates/years, never \
relative words like "last year" or "this month".

# TRANSCRIPT:

{transcript}

Question: {question}
Answer:"""

# QA prompt for memory-search scenarios (aligned with Go implementation).
_QA_SYSTEM_PROMPT = """\
You are a memory retrieval assistant. Use the load_memory \
tool to search your memory, then output a short factual answer.

ANSWERING PRIORITY - ALWAYS try to answer first:
If ANY memory is topically related to the question, you \
MUST provide an answer.
Only say "{not_available}" when ZERO memories relate to \
the question topic.
When in doubt between answering and saying "not \
available", ALWAYS answer.

ANSWER STRATEGY:

A) FACTUAL questions (Who/What/Where/When/How many):
   Answer using the exact words from a relevant memory.
   For "When" questions, look for dates in the memory.
   For "How many" questions, output the NUMBER.

B) HYPOTHETICAL/INFERENCE questions \
(Would/Could/Is it likely/What might):
   MUST reason and infer from available evidence. \
NEVER say "not available" for these.

C) TEMPORAL CALCULATION questions \
(How long/What happened first):
   Combine dates from multiple memories to calculate.

D) OPEN-DOMAIN questions \
(What does X feel/think/enjoy/value):
   Copy the most relevant phrase from memory text.
   NEVER say "not available" if ANY related memory exists.

RULES:
1. Always search memory before answering.
2. Convert relative time references to ABSOLUTE dates.
3. Maximum 1-8 words. Output ONLY the answer fragment.
4. For "When" questions: natural language date format \
like "7 May 2023". NEVER use ISO format.
5. Do NOT rephrase. Use exact words from memory.
6. NEVER start answer with a person's name or pronoun.
"""


# ---------------------------------------------------------------------------
# Data structures.
# ---------------------------------------------------------------------------
@dataclass
class QAResult:
    question_id: str
    category: str
    question: str
    reference: str
    prediction: str
    f1: float = 0.0
    bleu: float = 0.0
    llm_score: float = 0.0
    prompt_tokens: int = 0
    completion_tokens: int = 0
    total_tokens: int = 0
    llm_calls: int = 0


@dataclass
class TokenUsage:
    prompt_tokens: int = 0
    completion_tokens: int = 0
    total_tokens: int = 0
    llm_calls: int = 0


@dataclass
class SampleResult:
    sample_id: str
    qa_results: list[QAResult] = field(default_factory=list)
    overall_f1: float = 0.0
    overall_bleu: float = 0.0
    overall_llm: float = 0.0
    total_time_ms: int = 0
    token_usage: TokenUsage | None = None


# ---------------------------------------------------------------------------
# OpenAI client for direct LLM calls.
# ---------------------------------------------------------------------------
def create_openai_client() -> OpenAI:
    return OpenAI()


def call_openai(
    client: OpenAI,
    model: str,
    prompt: str,
    max_tokens: int = 500,
    temperature: float = 0.0,
) -> tuple[str, TokenUsage]:
    """Call OpenAI and return (response_text, token_usage)."""
    resp = client.chat.completions.create(
        model=model,
        messages=[{"role": "user", "content": prompt}],
        max_tokens=max_tokens,
        temperature=temperature,
    )
    text = resp.choices[0].message.content.strip()
    usage = TokenUsage(
        prompt_tokens=resp.usage.prompt_tokens,
        completion_tokens=resp.usage.completion_tokens,
        total_tokens=resp.usage.total_tokens,
        llm_calls=1,
    )
    return text, usage


# ---------------------------------------------------------------------------
# Scenario: Baseline (full conversation as context).
# ---------------------------------------------------------------------------
def evaluate_baseline(
    sample: dataset.LoCoMoSample,
    client: OpenAI,
    model: str,
    eval_client: OpenAI | None,
    eval_model: str,
    enable_llm_judge: bool,
) -> SampleResult:
    """Evaluate using full conversation as context (no memory)."""
    transcript = sample.build_full_conversation()
    qa_results: list[QAResult] = []
    total_usage = TokenUsage()

    for qa in sample.qa:
        prompt = _LONG_CONTEXT_PROMPT.format(
            not_available=NOT_AVAILABLE,
            transcript=transcript,
            question=qa.question,
        )
        prediction, usage = call_openai(
            client, model, prompt, max_tokens=500,
        )
        total_usage.prompt_tokens += usage.prompt_tokens
        total_usage.completion_tokens += usage.completion_tokens
        total_usage.total_tokens += usage.total_tokens
        total_usage.llm_calls += usage.llm_calls

        m = metrics.compute_f1(prediction, qa.answer)
        bleu = metrics.compute_bleu1(prediction, qa.answer)

        llm_score = 0.0
        if enable_llm_judge and eval_client:
            judge_prompt = metrics.build_llm_judge_prompt(
                qa.question, qa.answer, prediction,
            )
            judge_resp, judge_usage = call_openai(
                eval_client, eval_model, judge_prompt,
                max_tokens=200,
            )
            llm_score = metrics.parse_llm_judge_response(judge_resp)

        pred_short = prediction[:120].replace("\n", " ")
        log.info(
            "    %s: F1=%.3f BLEU=%.3f LLM=%.1f "
            "pt=%d ct=%d",
            qa.question_id, m.f1, bleu, llm_score,
            usage.prompt_tokens,
            usage.completion_tokens,
        )
        log.info(
            "      pred: %s", pred_short,
        )
        log.info(
            "      ref:  %s", qa.answer[:120],
        )
        qa_results.append(QAResult(
            question_id=qa.question_id,
            category=qa.category,
            question=qa.question,
            reference=qa.answer,
            prediction=prediction,
            f1=m.f1,
            bleu=bleu,
            llm_score=llm_score,
            prompt_tokens=usage.prompt_tokens,
            completion_tokens=usage.completion_tokens,
            total_tokens=usage.total_tokens,
            llm_calls=usage.llm_calls,
        ))
    return SampleResult(
        sample_id=sample.sample_id,
        qa_results=qa_results,
        token_usage=total_usage,
    )


# ---------------------------------------------------------------------------
# Scenario: Memory (ADK InMemoryMemoryService).
# ---------------------------------------------------------------------------
async def evaluate_memory(
    sample: dataset.LoCoMoSample,
    model_name: str,
    eval_client: OpenAI | None,
    eval_model: str,
    enable_llm_judge: bool,
) -> SampleResult:
    """Evaluate using ADK InMemoryMemoryService.

    Phase 1: Feed each session into ADK via add_session_to_memory.
    Phase 2: Use LlmAgent with LoadMemoryTool to answer questions.
    """
    memory_service = InMemoryMemoryService()
    session_service = InMemorySessionService()
    user_id = f"user_{sample.sample_id}"

    # Phase 1: Ingest conversations into memory.
    for sess in sample.conversation:
        adk_session = await session_service.create_session(
            app_name=APP_NAME,
            user_id=user_id,
        )
        # Build events from conversation turns.
        events: list[Event] = []
        for turn in sess.turns:
            role = "user" if turn.speaker == sample.speakers[0] else (
                "model"
            )
            ev = Event(
                author=role,
                content=types.Content(
                    role=role,
                    parts=[types.Part(text=(
                        f"[SessionDate: {sess.session_date}] "
                        f"{turn.speaker}: {turn.text}"
                    ))],
                ),
            )
            events.append(ev)

        # Populate session events.
        adk_session.events = events
        # Add session to memory.
        await memory_service.add_session_to_memory(adk_session)

    # Phase 2: QA using LlmAgent with LoadMemoryTool.
    # Use LiteLlm to route through OpenAI-compatible API.
    lite_model = LiteLlm(model=f"openai/{model_name}")
    qa_agent = LlmAgent(
        name="qa_agent",
        model=lite_model,
        instruction=_QA_SYSTEM_PROMPT.format(
            not_available=NOT_AVAILABLE,
        ),
        tools=[LoadMemoryTool()],
    )

    qa_results: list[QAResult] = []
    total_usage = TokenUsage()

    for qa in sample.qa:
        qa_session = await session_service.create_session(
            app_name=APP_NAME,
            user_id=user_id,
        )
        runner = Runner(
            agent=qa_agent,
            app_name=APP_NAME,
            session_service=session_service,
            memory_service=memory_service,
        )

        user_content = types.Content(
            role="user",
            parts=[types.Part(text=qa.question)],
        )

        prediction = ""
        _usage_tracker.reset()
        try:
            async for ev in runner.run_async(
                user_id=user_id,
                session_id=qa_session.id,
                new_message=user_content,
            ):
                if (
                    ev.content
                    and ev.content.parts
                    and ev.is_final_response()
                ):
                    for part in ev.content.parts:
                        if part.text:
                            prediction += part.text
        except Exception as e:
            log.warning(
                "Error evaluating QA %s: %s",
                qa.question_id, e,
            )
            prediction = ""
            try:
                await litellm.close_litellm_async_clients()
            except Exception as close_err:
                log.debug(
                    "Failed to close litellm async clients: %s",
                    close_err,
                )

        # Snapshot QA inference tokens from litellm callback.
        qa_usage = _usage_tracker.snapshot()

        # Accumulate only QA inference tokens.
        total_usage.prompt_tokens += qa_usage.prompt_tokens
        total_usage.completion_tokens += (
            qa_usage.completion_tokens
        )
        total_usage.total_tokens += qa_usage.total_tokens
        total_usage.llm_calls += qa_usage.llm_calls

        prediction = prediction.strip()
        m = metrics.compute_f1(prediction, qa.answer)
        bleu = metrics.compute_bleu1(prediction, qa.answer)

        llm_score = 0.0
        if enable_llm_judge and eval_client:
            judge_prompt = metrics.build_llm_judge_prompt(
                qa.question, qa.answer, prediction,
            )
            judge_resp, judge_usage = call_openai(
                eval_client, eval_model, judge_prompt,
                max_tokens=200,
            )
            llm_score = metrics.parse_llm_judge_response(
                judge_resp,
            )

        # Truncate prediction for logging; flag suspicious
        # outputs (system prompt leakage, too long, etc.).
        pred_short = prediction[:120].replace("\n", " ")
        flag = ""
        if len(prediction) > 200:
            flag = " [WARN:long]"
        elif "memory assistant" in prediction.lower():
            flag = " [WARN:prompt-leak]"

        log.info(
            "    %s: F1=%.3f BLEU=%.3f LLM=%.1f "
            "pt=%d ct=%d%s",
            qa.question_id, m.f1, bleu, llm_score,
            qa_usage.prompt_tokens,
            qa_usage.completion_tokens, flag,
        )
        log.info(
            "      pred: %s", pred_short,
        )
        log.info(
            "      ref:  %s", qa.answer[:120],
        )
        qa_results.append(QAResult(
            question_id=qa.question_id,
            category=qa.category,
            question=qa.question,
            reference=qa.answer,
            prediction=prediction,
            f1=m.f1,
            bleu=bleu,
            llm_score=llm_score,
            prompt_tokens=qa_usage.prompt_tokens,
            completion_tokens=qa_usage.completion_tokens,
            total_tokens=qa_usage.total_tokens,
            llm_calls=qa_usage.llm_calls,
        ))

    await litellm.close_litellm_async_clients()
    return SampleResult(
        sample_id=sample.sample_id,
        qa_results=qa_results,
        token_usage=total_usage,
    )


# ---------------------------------------------------------------------------
# Aggregation and output.
# ---------------------------------------------------------------------------
def aggregate_results(
    sample_results: list[SampleResult],
    scenario: str,
    model: str,
    eval_model: str,
    enable_llm_judge: bool,
) -> dict:
    """Aggregate sample results into final output."""
    cat_agg = metrics.CategoryAggregator()
    total_questions = 0
    total_usage = TokenUsage()

    for sr in sample_results:
        for qa in sr.qa_results:
            cat_agg.add(qa.category, qa.f1, qa.bleu, qa.llm_score)
            total_questions += 1
        if sr.token_usage:
            total_usage.prompt_tokens += sr.token_usage.prompt_tokens
            total_usage.completion_tokens += (
                sr.token_usage.completion_tokens
            )
            total_usage.total_tokens += sr.token_usage.total_tokens
            total_usage.llm_calls += sr.token_usage.llm_calls

    overall = cat_agg.get_overall()
    by_cat = cat_agg.get_category_metrics()

    return {
        "metadata": {
            "framework": "adk-python",
            "model": model,
            "eval_model": eval_model,
            "scenario": scenario,
            "memory_backend": (
                "inmemory" if scenario == "memory" else "none"
            ),
            "llm_judge": enable_llm_judge,
        },
        "summary": {
            "total_samples": len(sample_results),
            "total_questions": total_questions,
            "overall_f1": overall.f1,
            "overall_bleu": overall.bleu,
            "overall_llm_score": overall.llm_score,
            "total_prompt_tokens": total_usage.prompt_tokens,
            "total_completion_tokens": total_usage.completion_tokens,
            "total_tokens": total_usage.total_tokens,
            "total_llm_calls": total_usage.llm_calls,
        },
        "by_category": {
            cat: asdict(m) for cat, m in by_cat.items()
        },
        "sample_results": [
            {
                "sample_id": sr.sample_id,
                "qa_results": [asdict(qa) for qa in sr.qa_results],
            }
            for sr in sample_results
        ],
    }


def print_summary(result: dict) -> None:
    """Print evaluation summary to console."""
    meta = result["metadata"]
    summary = result["summary"]
    by_cat = result["by_category"]

    print()
    print("=" * 60)
    print(f"Memory Evaluation Results - {meta['scenario']}")
    print("=" * 60)
    print(f"\nFramework: {meta['framework']}")
    print(f"Model: {meta['model']}")
    print(f"Scenario: {meta['scenario']}")
    print(f"Memory Backend: {meta['memory_backend']}")
    print(
        f"Samples: {summary['total_samples']}"
        f" | Questions: {summary['total_questions']}"
    )

    print("\n--- Overall Metrics ---")
    print(f"F1 Score:   {summary['overall_f1']:.4f}")
    print(f"BLEU Score: {summary['overall_bleu']:.4f}")
    if summary["overall_llm_score"] > 0:
        print(f"LLM Score:  {summary['overall_llm_score']:.4f}")

    if summary["total_llm_calls"] > 0:
        qc = max(summary["total_questions"], 1)
        print("\n--- Token Usage ---")
        print(
            f"Prompt Tokens:     {summary['total_prompt_tokens']}"
            f" (avg {summary['total_prompt_tokens'] / qc:.0f}/QA)"
        )
        print(
            f"Completion Tokens: "
            f"{summary['total_completion_tokens']}"
            f" (avg "
            f"{summary['total_completion_tokens'] / qc:.0f}/QA)"
        )
        print(f"Total Tokens:      {summary['total_tokens']}")
        print(
            f"LLM Calls:         {summary['total_llm_calls']}"
            f" (avg {summary['total_llm_calls'] / qc:.1f}/QA)"
        )

    print("\n--- By Category ---")
    header = f"{'Category':<15} {'Count':>8} {'F1':>8} {'BLEU':>8}"
    if any(
        v.get("llm_score", 0) > 0 for v in by_cat.values()
    ):
        header += f" {'LLM':>8}"
    print(header)
    print("-" * len(header))

    categories = [
        "single-hop", "multi-hop", "temporal",
        "open-domain", "adversarial",
    ]
    for cat in categories:
        if cat in by_cat:
            m = by_cat[cat]
            line = (
                f"{cat:<15} {m['count']:>8}"
                f" {m['f1']:>8.3f} {m['bleu']:>8.3f}"
            )
            if m.get("llm_score", 0) > 0:
                line += f" {m['llm_score']:>8.3f}"
            else:
                line += f" {'-':>8}"
            print(line)

    print("=" * 60)


# ---------------------------------------------------------------------------
# Main entry point.
# ---------------------------------------------------------------------------
def parse_args() -> argparse.Namespace:
    parser = argparse.ArgumentParser(
        description="ADK Python Memory Evaluation Benchmark",
    )
    parser.add_argument(
        "--model", default="",
        help="Model name (env MODEL_NAME or gpt-4o-mini)",
    )
    parser.add_argument(
        "--eval-model", default="",
        help="Evaluation model for LLM judge",
    )
    parser.add_argument(
        "--dataset", default="../data",
        help="Dataset directory",
    )
    parser.add_argument(
        "--data-file", default="locomo10.json",
        help="Dataset file name",
    )
    parser.add_argument(
        "--output", default="../results/adk_python",
        help="Output directory",
    )
    parser.add_argument(
        "--scenario", default="baseline",
        help=(
            "Evaluation scenario (comma-separated): "
            "baseline, memory, all"
        ),
    )
    parser.add_argument(
        "--sample-id", default="",
        help="Filter by sample ID",
    )
    parser.add_argument(
        "--max-tasks", type=int, default=0,
        help="Maximum tasks (0=all)",
    )
    parser.add_argument(
        "--llm-judge", action="store_true",
        help="Enable LLM-as-Judge evaluation",
    )
    parser.add_argument(
        "--verbose", action="store_true",
        help="Verbose output",
    )
    return parser.parse_args()


def get_model_name(args: argparse.Namespace) -> str:
    if args.model:
        return args.model
    env = os.environ.get("MODEL_NAME", "")
    return env if env else "gpt-4o-mini"


def get_eval_model_name(
    args: argparse.Namespace,
    model: str,
) -> str:
    if args.eval_model:
        return args.eval_model
    env = os.environ.get("EVAL_MODEL_NAME", "")
    return env if env else model


def get_scenarios(scenario_str: str) -> list[str]:
    if scenario_str == "all":
        return ["baseline", "memory"]
    result: list[str] = []
    seen: set[str] = set()
    valid = {"baseline", "memory"}
    for s in scenario_str.split(","):
        s = s.strip()
        if s not in valid:
            log.error("Invalid scenario: %s", s)
            sys.exit(1)
        if s not in seen:
            seen.add(s)
            result.append(s)
    return result


async def main_async() -> None:
    args = parse_args()
    if args.verbose:
        # Only set our own logger to DEBUG; leave third-party
        # loggers (httpx, openai, litellm, etc.) at WARNING
        # to avoid flooding the log.
        log.setLevel(logging.DEBUG)
        for noisy in (
            "httpx", "httpcore", "openai", "litellm",
            "LiteLLM", "google", "google.adk",
            "urllib3", "chromadb",
        ):
            logging.getLogger(noisy).setLevel(
                logging.WARNING
            )

    openai_client = None
    try:
        # Sync OPENAI_API_BASE from OPENAI_BASE_URL for litellm.
        base_url = os.environ.get("OPENAI_BASE_URL", "")
        if base_url and not os.environ.get("OPENAI_API_BASE"):
            os.environ["OPENAI_API_BASE"] = base_url

        model_name = get_model_name(args)
        eval_model_name = get_eval_model_name(args, model_name)
        output_dir = args.output
        Path(output_dir).mkdir(parents=True, exist_ok=True)

        log.info("=== ADK Python Memory Evaluation (LoCoMo) ===")
        log.info("Model: %s", model_name)
        log.info("Eval Model: %s", eval_model_name)
        log.info("Scenario: %s", args.scenario)
        log.info("LLM Judge: %s", args.llm_judge)
        log.info("Output: %s", output_dir)

        # Load dataset.
        samples = dataset.load_samples(args.dataset, args.data_file)
        log.info("Loaded %d samples", len(samples))

        # Filter.
        if args.sample_id:
            samples = [
                s for s in samples if s.sample_id == args.sample_id
            ]
            log.info(
                "Filtered to %d samples (sample_id=%s)",
                len(samples), args.sample_id,
            )
        if not samples:
            log.error("No samples to evaluate")
            sys.exit(1)
        if args.max_tasks > 0:
            samples = samples[:args.max_tasks]
            log.info("Limited to %d samples", len(samples))

        # Prepare clients.
        openai_client = create_openai_client()
        eval_client = openai_client if args.llm_judge else None

        scenarios = get_scenarios(args.scenario)

        for scenario in scenarios:
            log.info("")
            log.info("=== Running: %s ===", scenario)
            start_time = time.time()
            sample_results: list[SampleResult] = []

            for i, sample in enumerate(samples):
                log.info(
                    "[%d/%d] Evaluating sample: %s (%d QA)",
                    i + 1, len(samples),
                    sample.sample_id, len(sample.qa),
                )
                sample_start = time.time()

                if scenario == "baseline":
                    sr = evaluate_baseline(
                        sample, openai_client, model_name,
                        eval_client, eval_model_name,
                        args.llm_judge,
                    )
                elif scenario == "memory":
                    sr = await evaluate_memory(
                        sample, model_name,
                        eval_client, eval_model_name,
                        args.llm_judge,
                    )
                else:
                    continue

                # Compute per-sample aggregates.
                if sr.qa_results:
                    sr.overall_f1 = sum(
                        q.f1 for q in sr.qa_results
                    ) / len(sr.qa_results)
                    sr.overall_bleu = sum(
                        q.bleu for q in sr.qa_results
                    ) / len(sr.qa_results)
                sr.total_time_ms = int(
                    (time.time() - sample_start) * 1000
                )
                sample_results.append(sr)
                log.info(
                    "  Completed in %dms | F1=%.3f BLEU=%.3f",
                    sr.total_time_ms, sr.overall_f1, sr.overall_bleu,
                )

            # Aggregate and save.
            result = aggregate_results(
                sample_results, scenario,
                model_name, eval_model_name,
                args.llm_judge,
            )
            total_time = time.time() - start_time
            result["summary"]["total_time_ms"] = int(total_time * 1000)

            # Save JSON.
            scenario_dir = Path(output_dir) / scenario
            scenario_dir.mkdir(parents=True, exist_ok=True)
            result_path = scenario_dir / "results.json"
            with open(result_path, "w") as f:
                json.dump(result, f, indent=2)
            log.info("Results saved to: %s", result_path)

            print_summary(result)
    finally:
        try:
            await litellm.close_litellm_async_clients()
        except Exception as close_err:
            log.debug(
                "Failed to close litellm async clients: %s",
                close_err,
            )
        if openai_client is not None:
            try:
                openai_client.close()
            except Exception as close_err:
                log.debug(
                    "Failed to close OpenAI client: %s",
                    close_err,
                )


def main() -> None:
    asyncio.run(main_async())


if __name__ == "__main__":
    main()
