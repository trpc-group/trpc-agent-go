"""
Base Dataset Interface for RAG Evaluation.
"""

from abc import ABC, abstractmethod
from typing import List, Optional
from dataclasses import dataclass


@dataclass
class QAItem:
    """A single QA item for evaluation."""

    question: str
    answer: str
    context: str
    source_doc: str


class BaseDataset(ABC):
    """Abstract base class for RAG evaluation datasets."""

    @abstractmethod
    def load_documents(self, max_docs: Optional[int] = None, filter_extensions: Optional[List[str]] = None, force_reload: bool = False) -> str:
        """
        Load documents to a temporary directory.

        Args:
            max_docs: Maximum number of documents to load. None for all.
            filter_extensions: List of file extensions to include. None for all.
            force_reload: If True, clear and reload documents. If False, reuse existing if present.

        Returns:
            Path to the temporary directory containing documents.
        """
        pass

    @abstractmethod
    def load_qa_items(self, max_items: Optional[int] = None) -> List[QAItem]:
        """
        Load QA items for evaluation.

        Args:
            max_items: Maximum number of QA items to load. None for all.

        Returns:
            List of QAItem objects.
        """
        pass
