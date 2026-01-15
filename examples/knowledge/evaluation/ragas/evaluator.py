"""
RAGAS Evaluation for RAG Systems.

This module provides evaluation capabilities using RAGAS metrics
to assess the quality of RAG systems.
"""

import json
import os
import time
from datetime import datetime
from typing import Any, List, Optional
from dataclasses import dataclass

from datasets import Dataset
from ragas import evaluate
from ragas.metrics._faithfulness import Faithfulness
from ragas.metrics._answer_relevance import AnswerRelevancy
from ragas.metrics._answer_correctness import AnswerCorrectness
from ragas.metrics._answer_similarity import AnswerSimilarity
from ragas.metrics._context_precision import ContextPrecision
from ragas.metrics._context_recall import ContextRecall
from ragas.metrics._context_entities_recall import ContextEntityRecall
from ragas.run_config import RunConfig
from langchain_openai import ChatOpenAI, OpenAIEmbeddings
from pydantic import SecretStr

import sys
sys.path.append(os.path.dirname(os.path.dirname(os.path.abspath(__file__))))
from util import get_config
from knowledge_system.base import KnowledgeBase
from dataset.base import BaseDataset


@dataclass
class EvaluationSample:
    """A single evaluation sample for RAGAS."""

    question: str
    answer: str
    contexts: List[str]
    ground_truth: str


class RAGASEvaluator:
    """Evaluator using RAGAS metrics for RAG quality assessment."""

    def __init__(
        self,
        llm_model: Optional[str] = None,
        embedding_model: Optional[str] = None,
        base_url: Optional[str] = None,
        api_key: Optional[str] = None,
        max_workers: int = 4,
        timeout: int = 600,
    ):
        """
        Initialize the RAGAS evaluator.

        Args:
            llm_model: OpenAI LLM model for evaluation. Defaults to MODEL_NAME env var.
            embedding_model: OpenAI embedding model for evaluation. Defaults to EMBEDDING_MODEL env var.
            base_url: OpenAI API base URL. Defaults to OPENAI_BASE_URL env var.
            api_key: OpenAI API key. Defaults to OPENAI_API_KEY env var.
            max_workers: Maximum number of concurrent workers for evaluation.
            timeout: Timeout in seconds for each LLM call.
        """
        config = get_config()

        llm_model = llm_model or config["model_name"]
        embedding_model = embedding_model or config["embedding_model"]
        base_url = base_url or config["base_url"]
        api_key = api_key or config["api_key"]

        self.llm = ChatOpenAI(
            model=llm_model,
            temperature=0,
            api_key=SecretStr(api_key) if api_key else None,
            base_url=base_url,
        )
        self.embeddings = OpenAIEmbeddings(
            model=embedding_model,
            api_key=SecretStr(api_key) if api_key else None,
            base_url=base_url,
        )
        self.run_config = RunConfig(
            max_workers=max_workers,
            timeout=timeout,
        )

    def evaluate_samples(self, samples: List[EvaluationSample]) -> dict:
        """
        Evaluate a list of samples using RAGAS metrics.

        Args:
            samples: List of EvaluationSample objects.

        Returns:
            Dictionary containing evaluation metrics.
        """
        dataset = Dataset.from_dict(
            {
                "question": [s.question for s in samples],
                "answer": [s.answer for s in samples],
                "contexts": [s.contexts for s in samples],
                "ground_truth": [s.ground_truth for s in samples],
            }
        )

        result: Any = evaluate(
            dataset,
            metrics=[
                # Answer quality metrics
                Faithfulness(),
                AnswerRelevancy(),
                AnswerCorrectness(),
                AnswerSimilarity(),
                # Context quality metrics
                ContextPrecision(),
                ContextRecall(),
                ContextEntityRecall(),
            ],
            llm=self.llm,
            embeddings=self.embeddings,
            run_config=self.run_config,
        )

        def safe_mean(value: Any) -> float:
            """Safely compute mean from a value that might be a list or float."""
            if isinstance(value, list):
                valid = [v for v in value if v is not None and not (isinstance(v, float) and v != v)]
                return sum(valid) / len(valid) if valid else 0.0
            if value is None or (isinstance(value, float) and value != value):
                return 0.0
            return float(value)

        return {
            # Answer metrics
            "faithfulness": safe_mean(result["faithfulness"]),
            "answer_relevancy": safe_mean(result["answer_relevancy"]),
            "answer_correctness": safe_mean(result["answer_correctness"]),
            "answer_similarity": safe_mean(result["answer_similarity"]),
            # Context metrics
            "context_precision": safe_mean(result["context_precision"]),
            "context_recall": safe_mean(result["context_recall"]),
            "context_entity_recall": safe_mean(result["context_entity_recall"]),
            "detailed_results": result.to_pandas().to_dict(),
        }


def run_evaluation(
    kb: KnowledgeBase,
    dataset: BaseDataset,
    max_docs: Optional[int] = 100,
    max_qa_items: Optional[int] = 10,
    retrieval_k: int = 4,
    skip_load: bool = False,
    full_log: bool = False,
    ragas_timeout: int = 600,
    ragas_workers: int = 20,
    output_file: Optional[str] = None,
    force_reload: bool = False,
) -> dict:
    """
    Run RAG evaluation using provided knowledge base and dataset.

    Each question is answered with a fresh session (no conversation history).

    Args:
        kb: Knowledge base instance implementing KnowledgeBase interface.
        dataset: Dataset instance implementing BaseDataset interface.
        max_docs: Maximum documents to load into knowledge base.
        max_qa_items: Maximum QA items for evaluation.
        retrieval_k: Number of documents to retrieve per query.
        skip_load: If True, skip loading documents into knowledge base (assumes already loaded).
        full_log: If True, print full answer results and trace for each question.
        force_reload: If True, force reload documents even if already cached.
        ragas_timeout: Timeout in seconds for RAGAS evaluation.
        ragas_workers: Number of concurrent workers for RAGAS evaluation.
        output_file: Optional path to save evaluation results as JSON.

    Returns:
        Evaluation results dictionary.
    """
    print("=== RAG Evaluation ===\n")

    # Step 1: Always load QA items first to collect source documents
    print("1. Loading QA items...")
    qa_items = dataset.load_qa_items(max_qa_items)
    print(f"   Loaded {len(qa_items)} QA items.\n")

    if skip_load:
        print("2. Skipping document loading (--skip-load enabled)...\n")
    else:
        print("2. Loading documents (QA sources + random distractors)...")
        # Load QA source docs + random distractors, filter to markdown only
        doc_dir = dataset.load_documents(max_docs, filter_extensions=[".md"], force_reload=force_reload)

        file_paths = []
        for filename in os.listdir(doc_dir):
            filepath = os.path.join(doc_dir, filename)
            if os.path.isfile(filepath):
                file_paths.append(filepath)

        print(f"   Found {len(file_paths)} documents.")

        print("3. Building knowledge base...")
        kb.load(file_paths)
        print("   Knowledge base built.\n")

    print("4. Running Q&A with fresh sessions (evaluating retrieval + generation)...")
    samples = []
    errors = []
    qa_times = []
    qa_start_total = time.time()

    for i, qa in enumerate(qa_items):
        print(f"\n   [{i + 1}/{len(qa_items)}] Q: {qa.question[:80]}...")
        qa_start = time.time()

        try:
            # Each call creates a fresh session with no conversation history
            answer, search_results = kb.answer(qa.question, k=retrieval_k)
            contexts = [r.content for r in search_results]

            # Handle empty contexts - use placeholder to avoid RAGAS errors
            if not contexts:
                print("   ⚠️  No contexts retrieved, using placeholder")
                contexts = ["No relevant context found."]

            qa_elapsed = time.time() - qa_start
            qa_times.append(qa_elapsed)

            print(f"   A: {answer[:200]}{'...' if len(answer) > 200 else ''}")
            print(f"   Retrieved {len(search_results)} contexts, took {qa_elapsed:.2f}s")

            if full_log:
                print("\n   === Full Answer ===")
                print(answer)
                print("\n   === Evaluation Contexts (for RAGAS) ===")
                for j, ctx in enumerate(contexts, 1):
                    print(f"\n   [{j}] {ctx}")

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
            print(f"   ❌ Error: {e} (took {qa_elapsed:.2f}s)")
            errors.append({"question": qa.question, "error": str(e), "time": qa_elapsed})
            continue

    qa_total_time = time.time() - qa_start_total

    if not samples:
        print("\n❌ No samples collected. Cannot run evaluation.")
        return {"error": "No samples collected", "errors": errors}

    avg_time = sum(qa_times) / len(qa_times) if qa_times else 0
    print(f"\n   Collected {len(samples)} samples, {len(errors)} errors")
    print(f"   ⏱️  Q&A total time: {qa_total_time:.2f}s, avg per question: {avg_time:.2f}s")

    print("\n5. Computing RAGAS metrics...")
    ragas_start = time.time()
    try:
        evaluator = RAGASEvaluator(max_workers=ragas_workers, timeout=ragas_timeout)
        metrics = evaluator.evaluate_samples(samples)
    except Exception as e:
        print(f"\n❌ RAGAS evaluation failed: {e}")
        return {"error": str(e), "samples_count": len(samples), "errors": errors}
    ragas_time = time.time() - ragas_start

    print("\n=== Evaluation Results ===")

    # Print detailed per-sample scores if available
    if full_log and "detailed_results" in metrics:
        print("\n--- Per-Sample Scores ---")
        detailed = metrics["detailed_results"]
        num_samples = len(detailed.get("question", []))
        metric_names = ["faithfulness", "answer_relevancy", "answer_correctness",
                       "answer_similarity", "context_precision", "context_recall",
                       "context_entity_recall"]
        for i in range(num_samples):
            q = detailed.get("question", {}).get(i, "N/A")
            print(f"\n[Sample {i+1}] Q: {q[:80]}...")
            for metric in metric_names:
                val = detailed.get(metric, {}).get(i, None)
                if val is not None:
                    print(f"   {metric}: {val:.4f}")
                else:
                    print(f"   {metric}: N/A")

    print("\n--- Answer Quality (Avg) ---")
    print(f"Faithfulness: {metrics['faithfulness']:.4f}")
    print(f"Answer Relevancy: {metrics['answer_relevancy']:.4f}")
    print(f"Answer Correctness: {metrics['answer_correctness']:.4f}")
    print(f"Answer Similarity: {metrics['answer_similarity']:.4f}")
    print("--- Context Quality (Avg) ---")
    print(f"Context Precision: {metrics['context_precision']:.4f}")
    print(f"Context Recall: {metrics['context_recall']:.4f}")
    print(f"Context Entity Recall: {metrics['context_entity_recall']:.4f}")
    print("--- Timing ---")
    print(f"Q&A total time: {qa_total_time:.2f}s (avg {avg_time:.2f}s/question)")
    print(f"RAGAS evaluation time: {ragas_time:.2f}s")
    print(f"Total time: {qa_total_time + ragas_time:.2f}s")

    # Save results to file if specified
    if output_file:
        # Remove detailed_results (not JSON serializable) for file output
        output_metrics = {k: v for k, v in metrics.items() if k != "detailed_results"}
        output_data = {
            "timestamp": datetime.now().isoformat(),
            "config": {
                "max_docs": max_docs,
                "max_qa_items": max_qa_items,
                "retrieval_k": retrieval_k,
                "ragas_timeout": ragas_timeout,
                "ragas_workers": ragas_workers,
            },
            "timing": {
                "qa_total_seconds": round(qa_total_time, 2),
                "qa_avg_seconds": round(avg_time, 2),
                "ragas_seconds": round(ragas_time, 2),
                "total_seconds": round(qa_total_time + ragas_time, 2),
            },
            "samples_count": len(samples),
            "errors_count": len(errors),
            "metrics": output_metrics,
            "errors": errors,
        }
        with open(output_file, "w", encoding="utf-8") as f:
            json.dump(output_data, f, indent=2, ensure_ascii=False)
        print(f"\n📁 Results saved to: {output_file}")

    return metrics


if __name__ == "__main__":
    import argparse

    parser = argparse.ArgumentParser(description="RAG Evaluation with RAGAS")
    parser.add_argument(
        "--kb",
        choices=["langchain", "trpc-agent-go"],
        default="langchain",
        help="Knowledge base implementation to use",
    )
    parser.add_argument(
        "--max-docs",
        type=int,
        default=None,
        help="Maximum documents to load (default: all documents)",
    )
    parser.add_argument(
        "--max-qa",
        type=int,
        default=10,
        help="Maximum QA items for evaluation",
    )
    parser.add_argument(
        "--k",
        type=int,
        default=4,
        help="Number of documents to retrieve per query",
    )
    parser.add_argument(
        "--skip-load",
        action="store_true",
        help="Skip loading documents into knowledge base (assumes already loaded)",
    )
    parser.add_argument(
        "--full-log",
        action="store_true",
        help="Print full answer results and trace for each question",
    )
    parser.add_argument(
        "--timeout",
        type=int,
        default=600,
        help="Timeout in seconds for RAGAS evaluation (default: 600)",
    )
    parser.add_argument(
        "--workers",
        type=int,
        default=1,
        help="Number of concurrent workers for RAGAS evaluation (default: 1)",
    )
    parser.add_argument(
        "--output",
        type=str,
        default=None,
        help="Output file path to save evaluation results as JSON",
    )
    parser.add_argument(
        "--repull-document",
        action="store_true",
        help="Re-pull documents from HuggingFace even if hf_docs directory exists (default: reuse existing)",
    )
    args = parser.parse_args()

    from dataset.huggingface.loader import HuggingFaceDocDataset

    dataset = HuggingFaceDocDataset()

    if args.kb == "trpc-agent-go":
        from knowledge_system.trpc_agent_go.knowledge_base import TRPCAgentGoKnowledgeBase
        kb = TRPCAgentGoKnowledgeBase(timeout=300, auto_start=False)  # Manual start
        print("Using tRPC-Agent-Go knowledge base")
    else:
        from knowledge_system.langchain.knowledge_base import LangChainKnowledgeBase
        kb = LangChainKnowledgeBase()
        print("Using LangChain knowledge base")

    run_evaluation(
        kb,
        dataset,
        max_docs=args.max_docs,
        max_qa_items=args.max_qa,
        retrieval_k=args.k,
        skip_load=args.skip_load,
        full_log=args.full_log,
        ragas_timeout=args.timeout,
        ragas_workers=args.workers,
        output_file=args.output,
        force_reload=args.repull_document,
    )

