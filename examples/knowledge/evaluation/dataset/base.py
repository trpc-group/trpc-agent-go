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
    def load_documents(self, force_reload: bool = False) -> str:
        """
        Load all documents to a temporary directory.

        Args:
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

    def cleanup(self) -> None:
        """Remove any temporary files or directories created by this dataset.

        Subclasses should override this to clean up cached documents.
        The default implementation is a no-op.
        """
        pass
