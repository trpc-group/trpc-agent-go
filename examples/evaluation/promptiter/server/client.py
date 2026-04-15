#!/usr/bin/env python3

import argparse
import json
import sys
import time
import urllib.error
import urllib.request


def log(message: str) -> None:
    print(message, flush=True)


def parse_args() -> argparse.Namespace:
    parser = argparse.ArgumentParser(description="Minimal PromptIter server example client.")
    parser.add_argument(
        "--base-url",
        default="http://127.0.0.1:8080/promptiter/v1/apps/promptiter-nba-commentary-app",
        help="PromptIter app base URL.",
    )
    parser.add_argument(
        "--mode",
        choices=("async", "blocking"),
        default="async",
        help="Execution mode.",
    )
    parser.add_argument(
        "--poll-interval",
        type=float,
        default=1.0,
        help="Polling interval in seconds for async mode.",
    )
    parser.add_argument(
        "--max-rounds",
        type=int,
        default=4,
        help="Maximum PromptIter optimization rounds.",
    )
    parser.add_argument(
        "--min-score-gain",
        type=float,
        default=0.005,
        help="Minimum validation score gain required to accept a patch.",
    )
    parser.add_argument(
        "--max-rounds-without-acceptance",
        type=int,
        default=5,
        help="Maximum consecutive rejected rounds before stopping.",
    )
    parser.add_argument(
        "--target-score",
        type=float,
        default=1.0,
        help="Target validation score that stops optimization when reached.",
    )
    parser.add_argument(
        "--eval-case-parallelism",
        type=int,
        default=8,
        help="Maximum number of eval cases processed in parallel.",
    )
    parser.add_argument(
        "--parallel-inference",
        dest="parallel_inference",
        action="store_true",
        default=True,
        help="Enable parallel inference across eval cases.",
    )
    parser.add_argument(
        "--no-parallel-inference",
        dest="parallel_inference",
        action="store_false",
        help="Disable parallel inference across eval cases.",
    )
    parser.add_argument(
        "--parallel-evaluation",
        dest="parallel_evaluation",
        action="store_true",
        default=True,
        help="Enable parallel evaluation across eval cases.",
    )
    parser.add_argument(
        "--no-parallel-evaluation",
        dest="parallel_evaluation",
        action="store_false",
        help="Disable parallel evaluation across eval cases.",
    )
    args = parser.parse_args()
    if args.poll_interval <= 0:
        parser.error("--poll-interval must be > 0")
    return args


def request_json(method: str, url: str, payload: dict | None = None) -> dict:
    data = None
    headers = {"Accept": "application/json"}
    if payload is not None:
        data = json.dumps(payload).encode("utf-8")
        headers["Content-Type"] = "application/json"
    request = urllib.request.Request(url, data=data, headers=headers, method=method)
    try:
        with urllib.request.urlopen(request) as response:
            return json.load(response)
    except urllib.error.HTTPError as exc:
        body = exc.read().decode("utf-8", errors="replace")
        raise RuntimeError(f"{method} {url} failed with status {exc.code}: {body}") from exc


def get_object_field(payload: dict, *names: str) -> dict:
    for name in names:
        value = payload.get(name)
        if isinstance(value, dict):
            return value
    raise RuntimeError(f"Response payload did not contain any of the expected object fields: {names}")


def resolve_target_surface_id(structure: dict) -> str:
    surfaces = structure.get("Surfaces") or []
    for surface in surfaces:
        if surface.get("SurfaceID") == "candidate#instruction":
            return "candidate#instruction"
    candidate_node_ids = {
        node.get("NodeID")
        for node in (structure.get("Nodes") or [])
        if node.get("Name") == "candidate"
    }
    for surface in surfaces:
        if surface.get("Type") == "instruction" and surface.get("NodeID") in candidate_node_ids:
            return surface["SurfaceID"]
    for surface in surfaces:
        if surface.get("Type") == "instruction":
            return surface["SurfaceID"]
    raise RuntimeError("No instruction surface was found in the structure.")


def build_run_request(args: argparse.Namespace, target_surface_id: str) -> dict:
    return {
        "run": {
            "TrainEvalSetIDs": ["nba-commentary-train"],
            "ValidationEvalSetIDs": ["nba-commentary-validation"],
            "TargetSurfaceIDs": [target_surface_id],
            "EvaluationOptions": {
                "EvalCaseParallelism": args.eval_case_parallelism,
                "EvalCaseParallelInferenceEnabled": args.parallel_inference,
                "EvalCaseParallelEvaluationEnabled": args.parallel_evaluation,
            },
            "AcceptancePolicy": {
                "MinScoreGain": args.min_score_gain,
            },
            "StopPolicy": {
                "MaxRoundsWithoutAcceptance": args.max_rounds_without_acceptance,
                "TargetScore": args.target_score,
            },
            "MaxRounds": args.max_rounds,
        }
    }


def is_terminal_status(status: str) -> bool:
    return status in {"succeeded", "failed", "canceled"}


def describe_run(run: dict) -> str:
    status = run.get("Status", "")
    current_round = run.get("CurrentRound", 0)
    return f"status={status}, current_round={current_round}"


def describe_run_progress(run: dict) -> str:
    status = run.get("Status", "")
    if status == "queued":
        return "queued"
    if status != "running":
        return status
    if run.get("BaselineValidation") is None:
        return "baseline validation"
    current_round = run.get("CurrentRound", 0)
    if current_round == 0:
        return "waiting to start round 1"
    round_result = current_round_result(run, current_round)
    if round_result is None:
        return f"round {current_round} started"
    if round_result.get("Train") is None:
        return f"round {current_round} train evaluation"
    if round_result.get("Losses") is None:
        return f"round {current_round} terminal loss extraction"
    if round_result.get("Backward") is None:
        return f"round {current_round} backward pass"
    if round_result.get("Aggregation") is None:
        return f"round {current_round} gradient aggregation"
    if round_result.get("Patches") is None:
        return f"round {current_round} optimizer"
    if round_result.get("OutputProfile") is None:
        return f"round {current_round} applying patch set"
    if round_result.get("Validation") is None:
        return f"round {current_round} validation evaluation"
    if round_result.get("Acceptance") is None or round_result.get("Stop") is None:
        return f"round {current_round} acceptance and stop checks"
    if (round_result.get("Acceptance") or {}).get("Accepted"):
        return f"round {current_round} completed and accepted"
    return f"round {current_round} completed and rejected"


def current_round_result(run: dict, round_number: int) -> dict | None:
    for round_result in run.get("Rounds") or []:
        if round_result.get("Round") == round_number:
            return round_result
    return None


def final_validation_score(run: dict) -> float | None:
    baseline = ((run.get("BaselineValidation") or {}).get("OverallScore"))
    score = baseline
    for round_result in run.get("Rounds") or []:
        acceptance = round_result.get("Acceptance") or {}
        validation = round_result.get("Validation") or {}
        if acceptance.get("Accepted") and validation.get("OverallScore") is not None:
            score = validation["OverallScore"]
    return score


def accepted_instruction(run: dict, target_surface_id: str) -> str | None:
    profile = run.get("AcceptedProfile") or {}
    for override in profile.get("Overrides") or []:
        if override.get("SurfaceID") != target_surface_id:
            continue
        value = override.get("Value") or {}
        if value.get("Text"):
            return value["Text"]
    return None


def print_run_summary(run: dict, target_surface_id: str) -> None:
    log("Run summary:")
    log(f"  ID: {run.get('ID', '')}")
    log(f"  Status: {run.get('Status', '')}")
    baseline = (run.get("BaselineValidation") or {}).get("OverallScore")
    if baseline is not None:
        log(f"  Baseline validation score: {baseline:.2f}")
    final_score = final_validation_score(run)
    if final_score is not None:
        log(f"  Final accepted validation score: {final_score:.2f}")
    instruction = accepted_instruction(run, target_surface_id)
    if instruction:
        log(f"  Accepted instruction: {instruction}")
    for round_result in run.get("Rounds") or []:
        train = (round_result.get("Train") or {}).get("OverallScore")
        validation = (round_result.get("Validation") or {}).get("OverallScore")
        acceptance = round_result.get("Acceptance") or {}
        train_text = f"{train:.2f}" if train is not None else "n/a"
        validation_text = f"{validation:.2f}" if validation is not None else "n/a"
        log(
            "  Round {round} -> train {train}, validation {validation}, accepted {accepted}, delta {delta:.2f}".format(
                round=round_result.get("Round", 0),
                train=train_text,
                validation=validation_text,
                accepted=acceptance.get("Accepted", False),
                delta=acceptance.get("ScoreDelta", 0.0),
            )
        )


def run_blocking(base_url: str, payload: dict, target_surface_id: str) -> None:
    response = request_json("POST", f"{base_url}/runs", payload)
    print_run_summary(get_object_field(response, "result", "Result"), target_surface_id)


def run_async(base_url: str, payload: dict, target_surface_id: str, poll_interval: float) -> None:
    response = request_json("POST", f"{base_url}/async-runs", payload)
    run = get_object_field(response, "result", "Result")
    run_id = run["ID"]
    log(f"Started async run: {run_id}")
    last_progress = ""
    reported_baseline = False
    reported_validation_rounds: set[int] = set()
    while True:
        response = request_json("GET", f"{base_url}/async-runs/{run_id}")
        run = get_object_field(response, "result", "Result")
        progress = describe_run_progress(run)
        if progress != last_progress:
            log(f"Progress: {progress}")
            last_progress = progress
        baseline = run.get("BaselineValidation") or {}
        baseline_score = baseline.get("OverallScore")
        if baseline_score is not None and not reported_baseline:
            log(f"Baseline validation score: {baseline_score:.2f}")
            reported_baseline = True
        for round_result in run.get("Rounds") or []:
            round_number = round_result.get("Round")
            validation = round_result.get("Validation") or {}
            validation_score = validation.get("OverallScore")
            if validation_score is None or round_number in reported_validation_rounds:
                continue
            log(f"Round {round_number} validation score: {validation_score:.2f}")
            reported_validation_rounds.add(round_number)
        if is_terminal_status(run.get("Status", "")):
            print_run_summary(run, target_surface_id)
            return
        time.sleep(poll_interval)


def main() -> int:
    args = parse_args()
    base_url = args.base_url.rstrip("/")
    structure_response = request_json("GET", f"{base_url}/structure")
    structure = get_object_field(structure_response, "structure", "Structure")
    target_surface_id = resolve_target_surface_id(structure)
    log(f"Resolved target surface: {target_surface_id}")
    payload = build_run_request(args, target_surface_id)
    if args.mode == "blocking":
        run_blocking(base_url, payload, target_surface_id)
        return 0
    run_async(base_url, payload, target_surface_id, args.poll_interval)
    return 0


if __name__ == "__main__":
    sys.exit(main())
