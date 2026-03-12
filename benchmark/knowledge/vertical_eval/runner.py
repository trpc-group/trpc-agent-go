#
# Tencent is pleased to support the open source community by making trpc-agent-go available.
#
# Copyright (C) 2025 Tencent.  All rights reserved.
#
# trpc-agent-go is licensed under the Apache License Version 2.0.
#
#
"""
Vertical evaluation runner for trpc-agent-go knowledge system.

Manages the lifecycle of Go service instances with different configurations,
runs evaluations, and collects results for comparison.
"""

import atexit
import json
import os
import subprocess
import sys
import time
from typing import Any, List, Optional, Tuple

import requests

# Add parent directory to path so we can import from the evaluation package
EVAL_ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
sys.path.insert(0, EVAL_ROOT)

from vertical_eval.config import ExperimentConfig

GO_SERVICE_DIR = os.path.join(
    EVAL_ROOT, "knowledge_system", "trpc_agent_go", "trpc_knowledge"
)
GO_BINARY = os.path.join(GO_SERVICE_DIR, "trpc_knowledge")


class GoServiceManager:
    """Manages the lifecycle of a Go knowledge service instance."""

    def __init__(self, config: ExperimentConfig, base_port: int = 9000, log_dir: str = "."):
        self.config = config
        self.port = config.port if config.port > 0 else base_port
        self.process: Optional[subprocess.Popen] = None
        self.service_url = f"http://localhost:{self.port}"
        self.log_dir = log_dir
        self._log_file: Optional[Any] = None

    def build(self) -> None:
        """Build the Go service binary (always rebuilds)."""
        print(f"  Building Go service...")
        result = subprocess.run(
            ["go", "build", "-o", "trpc_knowledge", "."],
            cwd=GO_SERVICE_DIR,
            capture_output=True,
            text=True,
        )
        if result.returncode != 0:
            raise RuntimeError(f"Build failed: {result.stderr}")
        print(f"  Build OK.")

    def start(self) -> None:
        """Start the Go service with the experiment config."""
        self.build()

        flags = self.config.go_flags()
        if not any(f.startswith("--port=") for f in flags):
            flags.append(f"--port={self.port}")

        cmd = [GO_BINARY] + flags
        print(f"  Starting Go service: {' '.join(cmd)}")

        # Inherit current process env so OPENAI_API_KEY, OPENAI_BASE_URL,
        # MODEL_NAME, PGVECTOR_* etc. are available to the Go service.
        env = os.environ.copy()

        log_path = os.path.join(self.log_dir, f"{self.config.name}_go.log")
        self._log_file = open(log_path, "w", encoding="utf-8")
        print(f"  Go service log: {log_path}")

        # nosemgrep: python.lang.security.audit.dangerous-subprocess-use-audit
        # cmd is a list (no shell=True), and all elements come from internal
        # ExperimentConfig dataclass — not from external/user input.
        self.process = subprocess.Popen(  # nosec B603
            cmd,
            cwd=GO_SERVICE_DIR,
            stdout=self._log_file,
            stderr=subprocess.STDOUT,
            env=env,
        )
        atexit.register(self.stop)

        # Wait for service to be ready
        for i in range(30):
            if self.process.poll() is not None:
                self._log_file.flush()
                self._log_file.close()
                self._log_file = None
                with open(log_path, "r", encoding="utf-8") as f:
                    output = f.read()
                raise RuntimeError(f"Go service exited unexpectedly:\n{output[-2000:]}")
            if self._check_health():
                print(f"  Go service ready (PID: {self.process.pid}, port: {self.port})")
                return
            time.sleep(0.5)

        self.stop()
        raise RuntimeError(f"Go service failed to start within timeout, check log: {log_path}")

    def stop(self) -> None:
        """Stop the Go service and close log file."""
        if self.process is not None:
            print(f"  Stopping Go service (PID: {self.process.pid})...")
            self.process.terminate()
            try:
                self.process.wait(timeout=5)
            except subprocess.TimeoutExpired:
                self.process.kill()
            self.process = None
        if self._log_file is not None:
            self._log_file.close()
            self._log_file = None

    def get_config(self) -> dict:
        """Fetch the running service's configuration."""
        try:
            resp = requests.get(f"{self.service_url}/config", timeout=5)
            resp.raise_for_status()
            return resp.json()
        except Exception as e:
            return {"error": str(e)}

    def _check_health(self) -> bool:
        try:
            resp = requests.get(f"{self.service_url}/health", timeout=5)
            return resp.status_code == 200
        except requests.RequestException:
            return False


def load_documents(service_url: str, file_paths: List[str], timeout: int = 1200) -> None:
    """Load documents into the Go knowledge service."""
    print(f"  Loading {len(file_paths)} documents...")
    resp = requests.post(
        f"{service_url}/load",
        json={"file_paths": file_paths},
        timeout=timeout,
    )
    resp.raise_for_status()
    result = resp.json()
    if not result.get("success"):
        raise RuntimeError(f"Load failed: {result.get('message')}")
    print(f"  Documents loaded successfully.")


def answer_question(
    service_url: str, question: str, k: int = 4, timeout: int = 120
) -> Tuple[str, List[dict]]:
    """Ask a question and get answer + contexts from the Go service."""
    resp = requests.post(
        f"{service_url}/answer",
        json={"question": question, "k": k},
        timeout=timeout,
    )
    resp.raise_for_status()
    result = resp.json()
    answer = result.get("answer", "")
    documents = result.get("documents") or []
    return answer, documents


def run_single_experiment(
    config: ExperimentConfig,
    qa_items: list,
    doc_dir: str,
    evaluator,
    base_port: int = 9000,
    skip_load: bool = False,
    output_dir: str = ".",
) -> dict:
    """
    Run a single experiment: start Go service, load docs, run QA, evaluate.

    Returns:
        Dict with experiment results.
    """
    from evaluator.base import EvaluationSample

    print(f"\n{'='*70}")
    print(f"Experiment: {config.name}")
    print(f"Description: {config.description}")
    print(f"{'='*70}")

    mgr = GoServiceManager(config, base_port=base_port, log_dir=output_dir)

    try:
        mgr.start()

        # Log the actual service config
        svc_config = mgr.get_config()
        print(f"  Service config: {json.dumps(svc_config, indent=2)}")

        # Load documents if needed
        if not skip_load:
            file_paths = []
            for filename in sorted(os.listdir(doc_dir)):
                filepath = os.path.join(doc_dir, filename)
                if os.path.isfile(filepath):
                    file_paths.append(filepath)
            load_documents(mgr.service_url, file_paths)

        # Run Q&A
        print(f"  Running Q&A ({len(qa_items)} questions, k={config.retrieval_k})...")
        samples = []
        errors = []
        qa_times = []
        qa_start_total = time.time()

        for i, qa in enumerate(qa_items):
            print(f"    [{i+1}/{len(qa_items)}] Q: {qa.question[:80]}...")
            qa_start = time.time()
            try:
                answer, documents = answer_question(
                    mgr.service_url, qa.question, k=config.retrieval_k
                )
                contexts = [d["text"] for d in documents if d.get("text")]
                if not contexts:
                    contexts = ["No relevant context found."]

                qa_elapsed = time.time() - qa_start
                qa_times.append(qa_elapsed)
                print(f"    A: {answer[:120]}{'...' if len(answer) > 120 else ''} ({qa_elapsed:.1f}s)")

                samples.append(
                    EvaluationSample(
                        question=qa.question,
                        answer=answer,
                        contexts=contexts,
                        ground_truth=qa.answer,
                    )
                )
            except Exception as e:
                qa_elapsed = time.time() - qa_start
                print(f"    Error: {e} ({qa_elapsed:.1f}s)")
                errors.append({"question": qa.question, "error": str(e)})
                samples.append(
                    EvaluationSample(
                        question=qa.question,
                        answer="Error: failed to generate answer.",
                        contexts=["No relevant context found."],
                        ground_truth=qa.answer,
                    )
                )

        qa_total_time = time.time() - qa_start_total
        avg_time = sum(qa_times) / len(qa_times) if qa_times else 0

        print(f"\n  Q&A complete: {len(samples)} samples, {len(errors)} errors")
        print(f"  Q&A time: {qa_total_time:.1f}s total, {avg_time:.1f}s avg")

        # Run evaluation
        if not samples:
            return {"name": config.name, "error": "No samples collected"}

        print(f"  Running evaluation...")
        eval_start = time.time()
        try:
            eval_result = evaluator.evaluate(samples)
        except Exception as e:
            eval_result = f"Evaluation failed: {e}"
        eval_time = time.time() - eval_start

        print(f"\n  {eval_result}")
        print(f"  Eval time: {eval_time:.1f}s")

        result = {
            "name": config.name,
            "description": config.description,
            "config": {
                "hybrid_vector_weight": config.hybrid_vector_weight,
                "hybrid_text_weight": config.hybrid_text_weight,
                "retrieval_k": config.retrieval_k,
                "pg_table": config.pg_table,
            },
            "service_config": svc_config,
            "timing": {
                "qa_total_seconds": round(qa_total_time, 2),
                "qa_avg_seconds": round(avg_time, 2),
                "eval_seconds": round(eval_time, 2),
            },
            "samples_count": len(samples),
            "errors_count": len(errors),
            "result": eval_result,
            "errors": errors,
        }

        # Save individual result
        output_file = os.path.join(output_dir, f"{config.name}.json")
        with open(output_file, "w", encoding="utf-8") as f:
            json.dump(result, f, indent=2, ensure_ascii=False)
        print(f"  Saved: {output_file}")

        return result

    finally:
        mgr.stop()
