#!/usr/bin/env python3
"""
Deploy Reranker Server with FastAPI

This script starts a reranker server compatible with Infinity/Cohere API format.

Requirements:
    pip install transformers torch fastapi uvicorn

Usage:
    python deploy_infinity.py
    python deploy_infinity.py --model BAAI/bge-reranker-v2-m3 --port 7997
    python deploy_infinity.py --device cpu --port 7997
"""

import argparse
from contextlib import asynccontextmanager

import torch
import uvicorn
from fastapi import FastAPI
from pydantic import BaseModel
from transformers import AutoModelForSequenceClassification, AutoTokenizer


class RerankRequest(BaseModel):
    query: str
    documents: list[str]
    model: str | None = None
    top_n: int | None = None


class RerankResult(BaseModel):
    index: int
    relevance_score: float


class RerankResponse(BaseModel):
    results: list[RerankResult]


model = None
tokenizer = None


@asynccontextmanager
async def lifespan(app: FastAPI):
    global model, tokenizer
    model_name = app.state.model_name
    device = app.state.device

    print(f"Loading model: {model_name}")
    tokenizer = AutoTokenizer.from_pretrained(model_name)
    model = AutoModelForSequenceClassification.from_pretrained(model_name)
    model.to(device)
    model.eval()
    print(f"Model loaded on {device}")

    yield

    del model, tokenizer


app = FastAPI(title="Reranker Server", lifespan=lifespan)


@app.post("/rerank", response_model=RerankResponse)
async def rerank(request: RerankRequest):
    pairs = [[request.query, doc] for doc in request.documents]

    with torch.no_grad():
        inputs = tokenizer(
            pairs,
            padding=True,
            truncation=True,
            max_length=512,
            return_tensors="pt",
        ).to(app.state.device)

        logits = model(**inputs, return_dict=True).logits.view(-1).float()
        # Normalize to 0-1 range using sigmoid
        scores = torch.sigmoid(logits)

    results = [
        RerankResult(index=i, relevance_score=float(score))
        for i, score in enumerate(scores)
    ]

    results.sort(key=lambda x: x.relevance_score, reverse=True)

    if request.top_n and request.top_n > 0:
        results = results[: request.top_n]

    return RerankResponse(results=results)


@app.get("/health")
async def health():
    return {"status": "ok"}


def main():
    parser = argparse.ArgumentParser(description="Deploy Reranker Server")
    parser.add_argument(
        "--model",
        type=str,
        default="BAAI/bge-reranker-v2-m3",
        help="Model ID from Hugging Face",
    )
    parser.add_argument("--port", type=int, default=7997)
    parser.add_argument("--host", type=str, default="0.0.0.0")
    parser.add_argument(
        "--device",
        type=str,
        default="cuda" if torch.cuda.is_available() else "cpu",
    )

    args = parser.parse_args()

    app.state.model_name = args.model
    app.state.device = args.device

    print(f"Starting server on http://{args.host}:{args.port}")
    print(f"Rerank endpoint: http://localhost:{args.port}/rerank")

    uvicorn.run(app, host=args.host, port=args.port)


if __name__ == "__main__":
    main()
