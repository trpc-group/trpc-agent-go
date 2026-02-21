"""
LangChain Knowledge Base Implementation.

This module provides a RAG knowledge base using LangChain with PGVector
and ReAct agent for answering questions.
"""

import os
import sys
import time
from typing import List, Optional, Tuple

from langchain_text_splitters import RecursiveCharacterTextSplitter
from langchain_postgres import PGVector
from langchain_openai import ChatOpenAI, OpenAIEmbeddings
from langchain_core.tools import tool
from langchain.agents import AgentExecutor, create_tool_calling_agent
from langchain_core.prompts import ChatPromptTemplate
from pydantic import SecretStr
from sqlalchemy import create_engine, text

sys.path.append(os.path.dirname(os.path.dirname(os.path.dirname(os.path.abspath(__file__)))))
from util import get_config
from knowledge_system.base import KnowledgeBase, SearchResult


AGENT_PROMPT = ChatPromptTemplate.from_messages([
    ("system", """You are a helpful assistant that answers questions using a knowledge base search tool.

CRITICAL RULES(IMPORTANT !!!):
1. You MUST call the search tool AT LEAST ONCE before answering. NEVER answer without searching first.
2. Answer ONLY using information retrieved from the search tool.
3. Do NOT add external knowledge, explanations, or context not found in the retrieved documents.
4. Do NOT provide additional details, synonyms, or interpretations beyond what is explicitly stated in the search results.
5. Use the search tool at most 3 times. If you haven't found the answer after 3 searches, provide the best answer from what you found.
6. Be concise and stick strictly to the facts from the retrieved information.
7. Give only the direct answer."""),
    ("human", "{input}"),
    ("placeholder", "{agent_scratchpad}"),
])


class LangChainKnowledgeBase(KnowledgeBase):
    """A knowledge base using LangChain with PGVector and ReAct agent."""

    def __init__(
        self,
        embedding_model: Optional[str] = None,
        llm_model: Optional[str] = None,
        base_url: Optional[str] = None,
        api_key: Optional[str] = None,
        pg_connection: Optional[str] = None,
        collection_name: str = "langchain_docs",
        chunk_size: int = 500,
        chunk_overlap: int = 50,
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
        """
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
            tiktoken_enabled=False,  # Disable tiktoken for non-OpenAI models
            check_embedding_ctx_length=False,  # Skip context length check
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

    def load(self, file_paths: List[str], metadatas: Optional[List[dict]] = None):
        """
        Load documents from file paths into the knowledge base with chunking.

        Args:
            file_paths: List of file paths to load.
            metadatas: Optional list of metadata dicts for each file.
        """
        # Clear existing data before loading new documents
        try:
            # Use SQLAlchemy to execute TRUNCATE (pg_connection is SQLAlchemy format)
            engine = create_engine(self._pg_connection)
            with engine.connect() as conn:
                # Truncate embedding table first (has foreign key to collection)
                conn.execute(text("TRUNCATE TABLE langchain_pg_embedding CASCADE"))
                # Truncate collection table
                conn.execute(text("TRUNCATE TABLE langchain_pg_collection CASCADE"))
                conn.commit()
                # Verify truncation
                result = conn.execute(text("SELECT COUNT(*) FROM langchain_pg_embedding"))
                count = result.scalar()
                print(f"   Truncated existing tables, embedding count after truncation: {count}")
            # Reinitialize vectorstore after truncating collection table
            self.vectorstore = PGVector(
                embeddings=self.embeddings,
                collection_name=self._collection_name,
                connection=self._pg_connection,
                use_jsonb=True,
            )
        except Exception as e:
            # If truncation fails, silently continue
            # The tables will be created fresh when adding documents
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

        # Add documents in batches to avoid rate limiting
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
                # Print first doc in batch for debugging
                if batch:
                    print(f"   First doc length: {len(batch[0].page_content)} chars")
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
        """
        Answer a question using a fresh ReAct agent session.

        Creates a new agent for each question to ensure no conversation history.

        Args:
            question: The question to answer.
            k: Number of documents to retrieve for context.

        Returns:
            Tuple of (answer_text, search_results).
        """
        self._last_search_results = []

        @tool
        def search_knowledge_base(query: str) -> str:
            """this is a search tool that help search information you need. It's your knowledgebase, you search information by the tool to answer user's question."""
            results = self.search(query, k=k)
            self._last_search_results.extend(results)

            if not results:
                return "No relevant documents found."

            return "\n\n---\n\n".join(
                f"[Score: {r.score:.3f}]\n{r.content}" for r in results
            )

        llm = ChatOpenAI(
            model=self._llm_model,
            temperature=0,
            api_key=SecretStr(self._api_key) if self._api_key else None,
            base_url=self._base_url,
            timeout=120,
            max_retries=1,
        )

        tools = [search_knowledge_base]
        agent = create_tool_calling_agent(llm, tools, AGENT_PROMPT)
        agent_executor = AgentExecutor(
            agent=agent,
            tools=tools,
            verbose=False,
            handle_parsing_errors=True,
        )

        result = agent_executor.invoke({"input": question})
        answer = result.get("output", "")

        return answer, self._last_search_results
