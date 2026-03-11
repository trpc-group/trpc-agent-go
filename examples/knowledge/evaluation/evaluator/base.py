#
# Tencent is pleased to support the open source community by making trpc-agent-go available.
#
# Copyright (C) 2025 Tencent.  All rights reserved.
#
# trpc-agent-go is licensed under the Apache License Version 2.0.
#
#
"""
Base evaluator interface for RAG system evaluation.
"""

from abc import ABC, abstractmethod
from typing import List
from dataclasses import dataclass


@dataclass
class EvaluationSample:
    """A single evaluation sample."""

    question: str
    answer: str
    contexts: List[str]
    ground_truth: str


class Evaluator(ABC):
    """Abstract base class for RAG evaluators."""

    @abstractmethod
    def evaluate(self, samples: List[EvaluationSample]) -> str:
        """
        Evaluate a list of samples and return formatted results.

        Args:
            samples: List of EvaluationSample objects.

        Returns:
            Formatted evaluation result as string.
        """
        pass
