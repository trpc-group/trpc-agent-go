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
sys.path.append(os.path.dirname(os.path.dirname(os.path.dirname(os.path.abspath(__file__)))))
from util import get_config
from knowledge_system.base import KnowledgeBase
from dataset.base import BaseDataset
from evaluator.base import Evaluator, EvaluationSample


class RAGASEvaluator(Evaluator):
    """Evaluator using RAGAS metrics for RAG quality assessment."""

    def __init__(
        self,
        llm_model: Optional[str] = None,
        embedding_model: Optional[str] = None,
        base_url: Optional[str] = None,
        api_key: Optional[str] = None,
        max_workers: int = 4,
        timeout: int = 6000,
    ):
        """
        Initialize the RAGAS evaluator.

        Args:
            llm_model: OpenAI LLM model for evaluation. Defaults to EVAL_MODEL_NAME env var.
            embedding_model: OpenAI embedding model for evaluation. Defaults to EMBEDDING_MODEL env var.
            base_url: OpenAI API base URL. Defaults to EVAL_BASE_URL env var.
            api_key: OpenAI API key. Defaults to EVAL_API_KEY env var.
            max_workers: Maximum number of concurrent workers for evaluation.
            timeout: Timeout in seconds for each LLM call.
        """
        config = get_config()

        # Use evaluation-specific config (can be different from knowledge model)
        llm_model = llm_model or config["eval_model_name"]
        embedding_model = embedding_model or config["embedding_model"]
        base_url = base_url or config["eval_base_url"]
        api_key = api_key or config["eval_api_key"]

        self.llm = ChatOpenAI(
            model=llm_model,
            temperature=0,
            api_key=SecretStr(api_key) if api_key else None,
            base_url=base_url,
            max_tokens=40960,
        )
        self.embeddings = OpenAIEmbeddings(
            model=embedding_model,
            api_key=SecretStr(api_key) if api_key else None,
            base_url=base_url,
            tiktoken_enabled=False,  # Disable tiktoken for non-OpenAI models
            check_embedding_ctx_length=False,  # Skip context length check
        )
        self.run_config = RunConfig(
            max_workers=max_workers,
            timeout=timeout,
        )

    def evaluate(self, samples: List[EvaluationSample]) -> str:
        """
        Evaluate a list of samples using RAGAS metrics.

        Args:
            samples: List of EvaluationSample objects.

        Returns:
            Formatted evaluation result as string.
        """
        metrics = self._compute_metrics(samples)
        return self._format_results(metrics)

    def _compute_metrics(self, samples: List[EvaluationSample]) -> dict:
        """Compute RAGAS metrics for samples."""
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

    def _format_results(self, metrics: dict) -> str:
        """Format metrics into a readable string."""
        result = []
        result.append("=== RAGAS Evaluation Results ===\n")
        result.append("--- Answer Quality ---")
        result.append(f"Faithfulness:        {metrics['faithfulness']:.4f}")
        result.append(f"Answer Relevancy:    {metrics['answer_relevancy']:.4f}")
        result.append(f"Answer Correctness:  {metrics['answer_correctness']:.4f}")
        result.append(f"Answer Similarity:   {metrics['answer_similarity']:.4f}")
        result.append("\n--- Context Quality ---")
        result.append(f"Context Precision:   {metrics['context_precision']:.4f}")
        result.append(f"Context Recall:      {metrics['context_recall']:.4f}")
        result.append(f"Context Entity Recall: {metrics['context_entity_recall']:.4f}")
        return "\n".join(result)

    def get_metrics_dict(self, samples: List[EvaluationSample]) -> dict:
        """
        Get raw metrics dictionary (for backward compatibility).

        Args:
            samples: List of EvaluationSample objects.

        Returns:
            Dictionary containing evaluation metrics.
        """
        return self._compute_metrics(samples)
