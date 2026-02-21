"""
Abstract Knowledge Base Interface for RAG Evaluation.
"""

from abc import ABC, abstractmethod
from typing import List, Optional, Tuple
from dataclasses import dataclass


@dataclass
class SearchResult:
    """A single search result."""

    content: str
    score: float
    metadata: dict
    trace: Optional[dict] = None  # Optional trace information from agent


class KnowledgeBase(ABC):
    """Abstract base class for knowledge base implementations."""

    @abstractmethod
    def load(self, file_paths: List[str], metadatas: Optional[List[dict]] = None):
        """
        Load documents into the knowledge base from file paths.

        Args:
            file_paths: List of file paths to load.
            metadatas: Optional list of metadata dicts for each file.
        """
        pass

    @abstractmethod
    def search(self, query: str, k: int = 4) -> List[SearchResult]:
        """
        Search for relevant documents.

        Args:
            query: Search query string.
            k: Number of documents to return.

        Returns:
            List of SearchResult objects.
        """
        pass

    @abstractmethod
    def answer(self, question: str, k: int = 4) -> Tuple[str, List[SearchResult]]:
        """
        Answer a question using the knowledge base.

        This method creates a fresh session (no conversation history),
        searches for relevant context, and generates an answer.

        Args:
            question: The question to answer.
            k: Number of documents to retrieve for context.

        Returns:
            Tuple of (answer_text, search_results).
        """
        pass
