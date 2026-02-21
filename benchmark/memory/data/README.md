# LoCoMo Dataset

This directory contains the LoCoMo benchmark dataset for evaluating long-term
conversational memory.

## Download

Download the LoCoMo dataset from the official repository:

```bash
# Clone the LoCoMo repository.
git clone https://github.com/snap-research/locomo.git

# Copy the dataset files.
cp locomo/data/locomo10/*.json ./
```

## Dataset Format

The LoCoMo dataset contains long-term conversational data with the following
structure:

```json
{
  "sample_id": "1",
  "speakers": ["Alice", "Bob"],
  "conversation": [
    {
      "session_id": "1",
      "session_date": "2023-01-15",
      "turns": [
        {"speaker": "Alice", "text": "..."},
        {"speaker": "Bob", "text": "..."}
      ],
      "observation": "Key observations from this session...",
      "summary": "Summary of this session..."
    }
  ],
  "qa": [
    {
      "question_id": "1_1",
      "question": "What did Alice mention about...?",
      "answer": "...",
      "category": "single-hop",
      "evidence": ["1"]
    }
  ],
  "event_summary": {
    "Alice": "Summary of events for Alice...",
    "Bob": "Summary of events for Bob..."
  }
}
```

## QA Categories

- `single-hop`: Single-hop questions answerable from one conversation segment.
- `multi-hop`: Multi-hop questions requiring multiple conversation segments.
- `temporal`: Temporal reasoning questions involving time relationships.
- `open-domain`: Open-domain questions requiring world knowledge.
- `adversarial`: Adversarial questions designed to test robustness.

## Reference

[LoCoMo: Long-Context Conversational Memory Benchmark](https://arxiv.org/abs/2402.17753)
