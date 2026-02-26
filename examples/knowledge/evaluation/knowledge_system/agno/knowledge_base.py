"""
Agno Knowledge Base Implementation.

This module provides a RAG knowledge base using Agno with PGVector
for answering questions.
"""

import os
import sys
from typing import List, Optional, Tuple

from agno.agent import Agent
from agno.knowledge.knowledge import Knowledge
from agno.vectordb.pgvector import PgVector
from agno.knowledge.embedder.openai import OpenAIEmbedder
from agno.models.openai import OpenAIChat
from agno.db.postgres import PostgresDb
from agno.knowledge.reader.markdown_reader import MarkdownReader
from agno.knowledge.chunking.fixed import FixedSizeChunking
from sqlalchemy import create_engine, text

sys.path.append(os.path.dirname(os.path.dirname(os.path.dirname(os.path.abspath(__file__)))))
from util import get_config
from knowledge_system.base import KnowledgeBase, SearchResult


AGENT_INSTRUCTIONS = """You are a helpful assistant that answers questions using a knowledge base search tool.

CRITICAL RULES(IMPORTANT !!!):
1. You MUST call the search tool AT LEAST ONCE before answering. NEVER answer without searching first.
2. Answer ONLY using information retrieved from the search tool.
3. Do NOT add external knowledge, explanations, or context not found in the retrieved documents.
4. Do NOT provide additional details, synonyms, or interpretations beyond what is explicitly stated in the search results.
5. Use the search tool at most 3 times. If you haven't found the answer after 3 searches, provide the best answer from what you found.
6. Be concise and stick strictly to the facts from the retrieved information.
7. Give only the direct answer."""


class AgnoKnowledgeBase(KnowledgeBase):
    """A knowledge base using Agno with PGVector."""

    def __init__(
        self,
        embedding_model: Optional[str] = None,
        llm_model: Optional[str] = None,
        base_url: Optional[str] = None,
        api_key: Optional[str] = None,
        pg_connection: Optional[str] = None,
        collection_name: str = "agno_docs",
        chunk_size: int = 500,
        chunk_overlap: int = 50,
        max_results: int = 4,
    ):
        """
        Initialize the knowledge base.

        Args:
            embedding_model: OpenAI embedding model name. Defaults to EMBEDDING_MODEL env var.
            llm_model: OpenAI LLM model name. Defaults to MODEL_NAME env var.
            base_url: OpenAI API base URL. Defaults to OPENAI_BASE_URL env var.
            api_key: OpenAI API key. Defaults to OPENAI_API_KEY env var.
            pg_connection: PostgreSQL connection string. Defaults to PG_CONNECTION env var.
            collection_name: Name of the collection in PGVector.
            chunk_size: Size of text chunks for splitting.
            chunk_overlap: Overlap between chunks.
            max_results: Number of documents to retrieve per search (default: 4).
        """
        config = get_config()

        self._embedding_model = embedding_model or config["embedding_model"]
        self._llm_model = llm_model or config["model_name"]
        self._base_url = base_url or config["base_url"]
        self._api_key = api_key or config["api_key"]
        self._pg_connection = pg_connection or config["pg_connection"]
        self._collection_name = collection_name
        self._chunk_size = chunk_size
        self._chunk_overlap = chunk_overlap
        self._max_results = max_results

        # Initialize embedder with 1024 dimensions (matching the proxy API)
        self._embedder = OpenAIEmbedder(
            id=self._embedding_model,
            api_key=self._api_key,
            base_url=self._base_url,
            dimensions=1024,
        )

        # Initialize PgVector
        self._vector_db = PgVector(
            table_name=collection_name,
            db_url=self._pg_connection,
            embedder=self._embedder,
        )

        # Ensure table is created with correct dimensions
        self._vector_db.create()

        # Initialize Contents DB for storing content metadata
        self._contents_db = PostgresDb(
            db_url=self._pg_connection,
            db_schema="ai",
            knowledge_table=f"{collection_name}_contents",
        )

        # Initialize Knowledge with both vector_db and contents_db
        self._knowledge = Knowledge(
            vector_db=self._vector_db,
            contents_db=self._contents_db,
            max_results=self._max_results,
        )

        # Create reader with custom chunk settings (default is 5000, we want 500)
        # Use FixedSizeChunking to match LangChain's RecursiveCharacterTextSplitter behavior
        self._chunking_strategy = FixedSizeChunking(
            chunk_size=self._chunk_size,
            overlap=self._chunk_overlap,
        )
        # Use MarkdownReader instead of base Reader (base Reader.read() is not implemented)
        self._reader = MarkdownReader(
            chunking_strategy=self._chunking_strategy,
        )

        self._last_search_results: List[SearchResult] = []

    def load(self, file_paths: List[str], metadatas: Optional[List[dict]] = None):
        """
        Load documents from file paths into the knowledge base.

        Args:
            file_paths: List of file paths to load.
            metadatas: Optional list of metadata dicts for each file.
        """
        # Clear existing data before loading new documents
        try:
            engine = create_engine(self._pg_connection)
            with engine.connect() as conn:
                conn.execute(text(f"TRUNCATE TABLE {self._collection_name} CASCADE"))
                conn.commit()
                print(f"   Truncated table {self._collection_name}")
        except Exception as e:
            print(f"   Truncate table skipped: {e}")

        # Load documents using Knowledge.add_content()
        for i, filepath in enumerate(file_paths):
            try:
                meta = {"source": os.path.basename(filepath)}
                if metadatas and i < len(metadatas):
                    meta.update(metadatas[i])
                self._knowledge.add_content(
                    path=filepath,
                    name=os.path.basename(filepath),
                    metadata=meta,
                    upsert=True,
                    reader=self._reader,
                )
                if (i + 1) % 50 == 0 or (i + 1) == len(file_paths):
                    print(f"   Loaded {i + 1}/{len(file_paths)} files")
            except Exception as e:
                print(f"   ERROR loading {filepath}: {e}")
                raise

    def search(self, query: str, k: int = 4) -> List[SearchResult]:
        """
        Search for relevant documents with similarity scores.

        Args:
            query: Search query string.
            k: Number of documents to return.

        Returns:
            List of SearchResult objects.
        """
        results = self._vector_db.search(query=query, limit=k)
        return [
            SearchResult(
                content=doc.content if hasattr(doc, 'content') else str(doc),
                score=doc.score if hasattr(doc, 'score') else 0.0,
                metadata=doc.meta_data if hasattr(doc, 'meta_data') else {},
            )
            for doc in results
        ]

    def answer(self, question: str, k: int = 4) -> Tuple[str, List[SearchResult]]:
        """
        Answer a question using Agno Agent with knowledge.

        Args:
            question: The question to answer.
            k: Number of documents to retrieve for context.

        Returns:
            Tuple of (answer_text, search_results).
        """
        self._last_search_results = []

        # Create agent with knowledge
        agent = Agent(
            model=OpenAIChat(
                id=self._llm_model,
                api_key=self._api_key,
                base_url=self._base_url,
                temperature=0,  # Match LangChain and trpc-agent-go settings
            ),
            knowledge=self._knowledge,
            instructions=AGENT_INSTRUCTIONS,
            search_knowledge=True,
        )

        # Get answer from agent
        response = agent.run(question)
        answer = response.content if hasattr(response, 'content') else str(response)

        # Extract search results from agent's actual references
        self._last_search_results = []
        if hasattr(response, 'references') and response.references:
            for ref in response.references:
                if hasattr(ref, 'references') and ref.references:
                    for doc in ref.references:
                        if isinstance(doc, dict):
                            self._last_search_results.append(SearchResult(
                                content=doc.get('content', ''),
                                score=doc.get('score', 0.0),
                                metadata=doc.get('meta_data', {}),
                            ))
                        elif hasattr(doc, 'content'):
                            self._last_search_results.append(SearchResult(
                                content=doc.content if hasattr(doc, 'content') else str(doc),
                                score=doc.score if hasattr(doc, 'score') else 0.0,
                                metadata=doc.meta_data if hasattr(doc, 'meta_data') else {},
                            ))

        return answer or "", self._last_search_results

