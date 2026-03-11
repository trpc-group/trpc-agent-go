"""
MultiHop-RAG Dataset Loader.

Loads the MultiHop-RAG dataset from https://github.com/yixuantt/MultiHop-RAG
for RAG evaluation.  The dataset contains 2556 multi-hop queries whose
evidence is distributed across 2-4 documents, plus a news-article corpus.

Data files
----------
corpus.json : list of dicts
    Each entry: {"title", "body", "published_at", "source"}
MultiHopRAG.json : list of dicts
    Each entry: {"query", "answer", "question_type",
                 "evidence_list": [{"fact": str, ...}, ...]}
"""

import json
import os
import re
import shutil
import sys
from typing import Any, Dict, List, Optional

sys.path.append(os.path.dirname(os.path.dirname(os.path.abspath(__file__))))
from base import BaseDataset, QAItem


_GITHUB_RAW = (
    "https://raw.githubusercontent.com/yixuantt/MultiHop-RAG/main/dataset/{filename}"
)

_CORPUS_FILE = "corpus.json"
_QA_FILE = "MultiHopRAG.json"


def _download_file(url: str, dest: str) -> None:
    """Download a file from *url* to *dest* using urllib."""
    import urllib.request

    print(f"   Downloading {url} ...")
    urllib.request.urlretrieve(url, dest)
    print(f"   Saved to {dest}")


class MultiHopRAGDataset(BaseDataset):
    """Loader for the MultiHop-RAG benchmark dataset.

    Parameters
    ----------
    data_dir : str or None
        Override the directory where raw data files are cached.
        Defaults to ``dataset/multihop_rag/mhrag_data/`` next to this file.
    question_types : list[str] or None
        Filter QA items by question_type.  ``None`` means keep all types
        (except ``null_query``).
    """

    def __init__(
        self,
        data_dir: Optional[str] = None,
        question_types: Optional[List[str]] = None,
    ):
        self._data_dir = data_dir or os.path.join(
            os.path.dirname(os.path.abspath(__file__)), "mhrag_data"
        )
        self.question_types = question_types
        self._doc_dir: Optional[str] = None
        self._corpus: Optional[List[Dict[str, Any]]] = None
        self._qa_data: Optional[List[Dict[str, Any]]] = None

    def _ensure_file(self, filename: str) -> str:
        """Return local path to *filename*, downloading from GitHub if absent."""
        os.makedirs(self._data_dir, exist_ok=True)
        local_path = os.path.join(self._data_dir, filename)
        if not os.path.exists(local_path):
            url = _GITHUB_RAW.format(filename=filename)
            _download_file(url, local_path)
        return local_path

    def _load_corpus(self) -> List[Dict[str, Any]]:
        if self._corpus is not None:
            return self._corpus
        path = self._ensure_file(_CORPUS_FILE)
        with open(path, "r", encoding="utf-8") as f:
            self._corpus = json.load(f)
        print(f"   Loaded {len(self._corpus)} corpus documents from MultiHop-RAG")
        return self._corpus

    def _load_qa(self) -> List[Dict[str, Any]]:
        if self._qa_data is not None:
            return self._qa_data
        path = self._ensure_file(_QA_FILE)
        with open(path, "r", encoding="utf-8") as f:
            self._qa_data = json.load(f)
        print(f"   Loaded {len(self._qa_data)} QA entries from MultiHop-RAG")
        return self._qa_data

    def load_documents(self, force_reload: bool = True, **kwargs) -> str:
        """Export corpus articles as individual text files for the KB.

        Each corpus article is written as a ``.txt`` file whose content
        includes the title, source, published_at, and body.
        """
        self._doc_dir = os.path.join(
            os.path.dirname(os.path.abspath(__file__)), "mhrag_docs"
        )

        if not force_reload and os.path.exists(self._doc_dir) and os.listdir(self._doc_dir):
            existing = len([
                f for f in os.listdir(self._doc_dir)
                if os.path.isfile(os.path.join(self._doc_dir, f))
            ])
            print(f"   Reusing existing {existing} documents in {self._doc_dir}")
            return self._doc_dir

        corpus = self._load_corpus()

        if os.path.exists(self._doc_dir):
            shutil.rmtree(self._doc_dir)
        os.makedirs(self._doc_dir, exist_ok=True)

        count = 0
        for idx, article in enumerate(corpus):
            title = article.get("title", "")
            body = article.get("body", "")
            source = article.get("source", "")
            published_at = article.get("published_at", "")

            safe_title = re.sub(r'[^\w]', '_', title[:80])
            filename = f"mhrag_{idx:06d}_{safe_title}.txt"
            filepath = os.path.join(self._doc_dir, filename)

            content = (
                f"Title: {title}\n"
                f"Source: {source}\n"
                f"Published: {published_at}\n\n"
                f"{body}"
            )
            with open(filepath, "w", encoding="utf-8") as f:
                f.write(content)
            count += 1

        print(f"   Exported {count} corpus articles as documents")
        return self._doc_dir

    def load_qa_items(
        self,
        per_type_limit: int = 150,
    ) -> List[QAItem]:
        """Build QA items from MultiHop-RAG queries.

        The ``context`` field is assembled from the evidence_list facts,
        which represent the gold evidence passages for each query.

        Parameters
        ----------
        per_type_limit : int
            Take at most this many items per ``question_type``.
            Defaults to 150.
        """
        from collections import defaultdict

        qa_data = self._load_qa()

        groups: Dict[str, List[Dict[str, Any]]] = defaultdict(list)
        for entry in qa_data:
            qtype = entry.get("question_type", "")
            if qtype == "null_query":
                continue
            if self.question_types and qtype not in self.question_types:
                continue
            groups[qtype].append(entry)

        items: List[QAItem] = []
        for qtype, entries in sorted(groups.items()):
            selected = entries[:per_type_limit]
            for entry in selected:
                query = entry["query"]
                answer = entry["answer"]

                evidence_list = entry.get("evidence_list", [])
                facts = [
                    ev["fact"] for ev in evidence_list if isinstance(ev, dict) and "fact" in ev
                ]
                context = "\n\n".join(facts) if facts else ""

                items.append(
                    QAItem(
                        question=query,
                        answer=str(answer),
                        context=context,
                        source_doc=f"multihop_rag_{qtype}",
                    )
                )
            print(f"   {qtype}: {len(selected)}/{len(entries)} items")

        print(f"   Loaded {len(items)} QA items from MultiHop-RAG")
        return items

    def cleanup(self) -> None:
        """Remove cached document directory."""
        if self._doc_dir and os.path.exists(self._doc_dir):
            shutil.rmtree(self._doc_dir)
            self._doc_dir = None
