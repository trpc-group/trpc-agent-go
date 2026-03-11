#
# Tencent is pleased to support the open source community by making trpc-agent-go available.
#
# Copyright (C) 2025 Tencent.  All rights reserved.
#
# trpc-agent-go is licensed under the Apache License Version 2.0.
#
#
"""
LangChain Chain-based Knowledge Base Implementation.

This module provides a RAG knowledge base using LangChain's LCEL (LangChain
Expression Language) chain instead of a ReAct agent. The pipeline is:

    retrieve -> format_contexts -> prompt -> LLM -> parse

This gives a deterministic, reproducible flow: every question triggers exactly
one retrieval, and the LLM sees exactly the same prompt template. There is no
agent loop, no tool-calling, and no non-deterministic retry logic.

All parameters (chunk_size, chunk_overlap, embedding model, temperature, etc.)
are kept identical to the agent-based LangChain implementation so that the two
can be compared on equal footing.
"""

import os
import sys
from typing import List, Optional, Tuple

from langchain_text_splitters import RecursiveCharacterTextSplitter
from langchain_postgres import PGVector
from langchain_openai import ChatOpenAI, OpenAIEmbeddings
from langchain_core.prompts import ChatPromptTemplate
from langchain_core.output_parsers import StrOutputParser
from langchain_core.runnables import RunnablePassthrough
from langchain_core.documents import Document
from pydantic import SecretStr
from sqlalchemy import create_engine, text

sys.path.append(os.path.dirname(os.path.dirname(os.path.dirname(os.path.abspath(__file__)))))
from util import get_config
from knowledge_system.base import KnowledgeBase, SearchResult


RAG_PROMPT = ChatPromptTemplate.from_messages([
    ("system",
     "You are a helpful assistant that answers questions based on the provided context.\n\n"
     "CRITICAL RULES (IMPORTANT !!!):\n"
     "1. Answer ONLY using information from the context below.\n"
     "2. Do NOT add external knowledge, explanations, or context not found in the provided documents.\n"
     "3. Do NOT provide additional details, synonyms, or interpretations beyond what is explicitly stated.\n"
     "4. Be concise and stick strictly to the facts from the provided context.\n"
     "5. Give only the direct answer.\n\n"
     "Context:\n{context}"),
    ("human", "{question}"),
])


def _format_docs(docs: List[Document]) -> str:
    """Join retrieved documents into a single context string."""
    return "\n\n---\n\n".join(doc.page_content for doc in docs)


class LangChainChainKnowledgeBase(KnowledgeBase):
    """A knowledge base using LangChain LCEL chain (retrieve -> prompt -> LLM).

    Compared to the agent-based variant, this implementation:
    - Always performs exactly one retrieval per question.
    - Does not rely on tool-calling or agent loops.
    - Produces fully deterministic retrieval behaviour.
    """

    def __init__(
        self,
        embedding_model: Optional[str] = None,
        llm_model: Optional[str] = None,
        base_url: Optional[str] = None,
        api_key: Optional[str] = None,
        pg_connection: Optional[str] = None,
        collection_name: str = "langchain_chain_docs",
        chunk_size: int = 500,
        chunk_overlap: int = 50,
    ):
        config = get_config()

        embedding_model = embedding_model or config["embedding_model"]
        llm_model = llm_model or config["model_name"]
        base_url = base_url or config["base_url"]
        api_key = api_key or config["api_key"]
        pg_connection = pg_connection or config["pg_connection"]

        self._api_key = api_key
        self._base_url = base_url
        self._llm_model = llm_model
        self._pg_connection = pg_connection
        self._collection_name = collection_name

        self.embeddings = OpenAIEmbeddings(
            model=embedding_model,
            api_key=SecretStr(api_key) if api_key else None,
            base_url=base_url,
            tiktoken_enabled=False,
            check_embedding_ctx_length=False,
        )
        self.text_splitter = RecursiveCharacterTextSplitter(
            chunk_size=chunk_size,
            chunk_overlap=chunk_overlap,
        )
        self.vectorstore = PGVector(
            embeddings=self.embeddings,
            collection_name=collection_name,
            connection=pg_connection,
            use_jsonb=True,
        )

        self._last_search_results: List[SearchResult] = []

    # ------------------------------------------------------------------
    # KnowledgeBase interface
    # ------------------------------------------------------------------

    def load(self, file_paths: List[str], metadatas: Optional[List[dict]] = None):
        """Load documents from file paths into the knowledge base with chunking."""
        try:
            engine = create_engine(self._pg_connection)
            with engine.connect() as conn:
                conn.execute(text("TRUNCATE TABLE langchain_pg_embedding CASCADE"))
                conn.execute(text("TRUNCATE TABLE langchain_pg_collection CASCADE"))
                conn.commit()
                result = conn.execute(text("SELECT COUNT(*) FROM langchain_pg_embedding"))
                count = result.scalar()
                print(f"   Truncated existing tables, embedding count after truncation: {count}")
            self.vectorstore = PGVector(
                embeddings=self.embeddings,
                collection_name=self._collection_name,
                connection=self._pg_connection,
                use_jsonb=True,
            )
        except Exception as e:
            print(f"   Truncate tables skipped: {e}")

        texts = []
        file_metadatas = []
        for i, filepath in enumerate(file_paths):
            with open(filepath, "r", encoding="utf-8") as f:
                texts.append(f.read())
                meta = {"source": os.path.basename(filepath)}
                if metadatas and i < len(metadatas):
                    meta.update(metadatas[i])
                file_metadatas.append(meta)

        documents = self.text_splitter.create_documents(texts, file_metadatas)
        print(f"   Split {len(file_paths)} files into {len(documents)} chunks")

        batch_size = 50
        total = len(documents)
        total_batches = (total + batch_size - 1) // batch_size
        for i in range(0, total, batch_size):
            batch = documents[i:i + batch_size]
            try:
                self.vectorstore.add_documents(batch)
                print(f"   Added batch {i // batch_size + 1}/{total_batches} ({len(batch)} chunks)")
            except Exception as e:
                print(f"   ERROR in batch {i // batch_size + 1}/{total_batches}: {e}")
                if batch:
                    print(f"   First doc length: {len(batch[0].page_content)} chars")
                raise

    def search(self, query: str, k: int = 4) -> List[SearchResult]:
        """Search for relevant documents with similarity scores."""
        results = self.vectorstore.similarity_search_with_relevance_scores(query, k=k)
        return [
            SearchResult(
                content=doc.page_content,
                score=score,
                metadata=doc.metadata,
            )
            for doc, score in results
        ]

    def answer(self, question: str, k: int = 4) -> Tuple[str, List[SearchResult]]:
        """Answer a question using a fixed retrieve -> prompt -> LLM chain.

        Unlike the agent-based variant, this always performs exactly one
        retrieval and feeds the results directly into the LLM prompt.
        """
        self._last_search_results = []

        retriever = self.vectorstore.as_retriever(search_kwargs={"k": k})

        llm = ChatOpenAI(
            model=self._llm_model,
            temperature=0,
            api_key=SecretStr(self._api_key) if self._api_key else None,
            base_url=self._base_url,
            timeout=120,
            max_retries=1,
        )

        # Retrieve documents first so we can capture them for evaluation.
        docs = retriever.invoke(question)

        self._last_search_results = [
            SearchResult(
                content=doc.page_content,
                score=0.0,
                metadata=doc.metadata,
            )
            for doc in docs
        ]

        # Build and invoke the chain.
        chain = RAG_PROMPT | llm | StrOutputParser()
        answer_text = chain.invoke({
            "context": _format_docs(docs),
            "question": question,
        })

        return answer_text, self._last_search_results
