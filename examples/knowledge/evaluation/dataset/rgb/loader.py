"""
RGB (Retrieval-Augmented Generation Benchmark) Dataset Loader.

Loads the RGB dataset from https://github.com/chen700564/RGB
for RAG evaluation. The dataset provides queries with positive
and negative passages, supporting noise-rate controlled evaluation.

Data format (JSONL, each line is a JSON object):
    {
        "id": int,
        "query": str,
        "answer": str | list,
        "positive": [str, ...],
        "negative": [str, ...]
    }
"""

import json
import math
import os
import shutil
import sys
from typing import Any, Dict, List, Optional, Union

sys.path.append(os.path.dirname(os.path.dirname(os.path.abspath(__file__))))
from base import BaseDataset, QAItem


_RGB_DATA_URL = (
    "https://raw.githubusercontent.com/chen700564/RGB/master/data/{filename}"
)

_AVAILABLE_SUBSETS = {
    "en": "en_refine.json",
    "zh": "zh_refine.json",
    "en_int": "en_int.json",
    "zh_int": "zh_int.json",
    "en_fact": "en_fact.json",
    "zh_fact": "zh_fact.json",
}


def _flatten_answer(answer: Any) -> str:
    """Flatten the RGB answer field into a single ground-truth string.

    RGB answers come in three shapes:
      - str:              "Tampa, Florida"
      - list[str]:        ["Scottie Scheffler"]
      - list[list[str]]:  [["Jan 2, 2022", "January 2, 2022"], ["other"]]
    For the nested case each inner list is a group of equivalent acceptable
    answers; we pick the first element of each group and join with "; ".
    """
    if isinstance(answer, str):
        return answer
    if isinstance(answer, list):
        parts: List[str] = []
        for item in answer:
            if isinstance(item, list):
                parts.append(str(item[0]) if item else "")
            else:
                parts.append(str(item))
        return "; ".join(parts)
    return str(answer)


def _flatten_passages(passages: Union[List[str], List[List[str]]]) -> List[str]:
    """Flatten possibly-nested passage lists (``_int`` subsets) into a flat list."""
    flat: List[str] = []
    for item in passages:
        if isinstance(item, list):
            flat.extend(str(p) for p in item)
        else:
            flat.append(str(item))
    return flat


def _download_file(url: str, dest: str) -> None:
    """Download a file from *url* to *dest* using urllib (no extra deps)."""
    import urllib.request

    print(f"   Downloading {url} ...")
    urllib.request.urlretrieve(url, dest)
    print(f"   Saved to {dest}")


class RGBDataset(BaseDataset):
    """Loader for the RGB benchmark dataset.

    Parameters
    ----------
    subset : str
        Which subset to use.  One of ``en``, ``zh``, ``en_int``,
        ``zh_int``, ``en_fact``, ``zh_fact``.  Defaults to ``en``.
    noise_rate : float
        Fraction of retrieved passages that are negative (noisy).
        0.0 means all positive, 1.0 means all negative.
    passage_num : int
        Total number of passages to include per query.
    data_dir : str or None
        Override the directory where raw data files are cached.
        Defaults to ``dataset/rgb/rgb_data/`` next to this file.
    """

    def __init__(
        self,
        subset: str = "en",
        noise_rate: float = 0.0,
        passage_num: int = 5,
        data_dir: Optional[str] = None,
    ):
        if subset not in _AVAILABLE_SUBSETS:
            raise ValueError(
                f"Unknown RGB subset '{subset}'. "
                f"Choose from: {list(_AVAILABLE_SUBSETS.keys())}"
            )
        if not 0.0 <= noise_rate <= 1.0:
            raise ValueError(
                f"noise_rate must be between 0.0 and 1.0, got {noise_rate}"
            )
        self.subset = subset
        self.noise_rate = noise_rate
        self.passage_num = passage_num

        self._data_dir = data_dir or os.path.join(
            os.path.dirname(os.path.abspath(__file__)), "rgb_data"
        )
        self._doc_dir: Optional[str] = None
        self._instances: Optional[List[Dict[str, Any]]] = None

    def _ensure_data(self) -> List[Dict[str, Any]]:
        """Download (if needed) and parse the raw JSONL file."""
        if self._instances is not None:
            return self._instances

        os.makedirs(self._data_dir, exist_ok=True)
        filename = _AVAILABLE_SUBSETS[self.subset]
        local_path = os.path.join(self._data_dir, filename)

        if not os.path.exists(local_path):
            url = _RGB_DATA_URL.format(filename=filename)
            _download_file(url, local_path)

        instances: List[Dict[str, Any]] = []
        with open(local_path, "r", encoding="utf-8") as f:
            for line in f:
                line = line.strip()
                if line:
                    instances.append(json.loads(line))
        print(f"   Loaded {len(instances)} instances from RGB/{filename}")
        self._instances = instances
        return instances

    def load_documents(self, force_reload: bool = True) -> str:
        """Export positive passages as individual text files for the KB.

        Each unique positive passage across all instances is written as a
        separate file so that the knowledge-base can ingest them.
        """
        self._doc_dir = os.path.join(
            os.path.dirname(os.path.abspath(__file__)), "rgb_docs"
        )

        if not force_reload and os.path.exists(self._doc_dir) and os.listdir(self._doc_dir):
            existing = len([
                f for f in os.listdir(self._doc_dir)
                if os.path.isfile(os.path.join(self._doc_dir, f))
            ])
            print(f"   Reusing existing {existing} documents in {self._doc_dir}")
            return self._doc_dir

        instances = self._ensure_data()

        if os.path.exists(self._doc_dir):
            shutil.rmtree(self._doc_dir)
        os.makedirs(self._doc_dir, exist_ok=True)

        doc_idx = 0
        seen_texts: set = set()
        for inst in instances:
            passages = _flatten_passages(inst.get("positive", []))
            for text in passages:
                if text in seen_texts:
                    continue
                seen_texts.add(text)

                filepath = os.path.join(self._doc_dir, f"rgb_doc_{doc_idx:06d}.txt")
                with open(filepath, "w", encoding="utf-8") as f:
                    f.write(text)
                doc_idx += 1

        print(f"   Exported {doc_idx} unique positive passages as documents")
        return self._doc_dir

    def load_qa_items(self, max_items: Optional[int] = None) -> List[QAItem]:
        """Build QA items from the RGB dataset.

        For each instance we assemble a context string from positive passages
        (and optionally negative passages controlled by ``noise_rate``).
        """
        instances = self._ensure_data()

        items: List[QAItem] = []
        for inst in instances:
            query = inst["query"]
            answer = _flatten_answer(inst["answer"])

            positives = _flatten_passages(inst.get("positive", []))
            negatives = _flatten_passages(inst.get("negative", []))

            neg_num = math.ceil(self.passage_num * self.noise_rate)
            pos_num = self.passage_num - neg_num

            if pos_num > len(positives):
                pos_num = len(positives)
                neg_num = self.passage_num - pos_num
            if neg_num > len(negatives):
                neg_num = len(negatives)

            selected_pos = positives[:pos_num]
            selected_neg = negatives[:neg_num]
            docs = selected_pos + selected_neg

            context = "\n\n".join(docs)

            items.append(
                QAItem(
                    question=query,
                    answer=answer,
                    context=context,
                    source_doc=f"rgb_{self.subset}_{inst.get('id', 'unknown')}",
                )
            )

            if max_items is not None and len(items) >= max_items:
                break

        print(f"   Loaded {len(items)} QA items from RGB/{self.subset}")
        return items

    def cleanup(self) -> None:
        """Remove cached document directory."""
        if self._doc_dir and os.path.exists(self._doc_dir):
            shutil.rmtree(self._doc_dir)
            self._doc_dir = None
