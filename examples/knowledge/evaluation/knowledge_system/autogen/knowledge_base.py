"""
AutoGen Knowledge Base Implementation.

This module provides a RAG knowledge base using AutoGen (pyautogen) with
RetrieveUserProxyAgent and PGVector for answering questions.
"""

import os
import sys
from typing import List, Optional, Tuple

from autogen import AssistantAgent
from autogen.agentchat.contrib.retrieve_user_proxy_agent import RetrieveUserProxyAgent

sys.path.append(os.path.dirname(os.path.dirname(os.path.dirname(os.path.abspath(__file__)))))
from util import get_config
from knowledge_system.base import KnowledgeBase, SearchResult


SYSTEM_MESSAGE = """You are a helpful assistant that answers questions using retrieved context.

CRITICAL RULES:
1. Answer ONLY using information from the retrieved context.
2. Do NOT add external knowledge, explanations, or context not found in the retrieved documents.
3. Do NOT provide additional details, synonyms, or interpretations beyond what is explicitly stated in the context.
4. Be concise and stick strictly to the facts from the retrieved information.

If the context doesn't contain the answer, say "I cannot find this information in the knowledge base" instead of making up an answer."""


class AutoGenKnowledgeBase(KnowledgeBase):
    """A knowledge base using AutoGen RetrieveUserProxyAgent with PGVector."""

    def __init__(
        self,
        embedding_model: Optional[str] = None,
        llm_model: Optional[str] = None,
        base_url: Optional[str] = None,
        api_key: Optional[str] = None,
        pg_connection: Optional[str] = None,
        collection_name: str = "autogen_docs",
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

        # Parse PG connection string for db_config
        self._db_config = self._parse_pg_connection(self._pg_connection)

        # LLM config for AutoGen
        self._config_list = [
            {
                "model": self._llm_model,
                "api_key": self._api_key,
                "base_url": self._base_url,
            }
        ]

        self._llm_config = {
            "timeout": 600,
            "cache_seed": None,
            "config_list": self._config_list,
            "temperature": 0,
        }

        self._loaded_file_paths: List[str] = []
        self._last_search_results: List[SearchResult] = []
        self._ragproxyagent: Optional[RetrieveUserProxyAgent] = None

    def _parse_pg_connection(self, connection_string: str) -> dict:
        """
        Parse PostgreSQL connection string to db_config dict.

        Args:
            connection_string: SQLAlchemy format connection string.

        Returns:
            Dict with connection_string for PGVector.
        """
        # Convert from postgresql+psycopg:// to postgresql://
        pg_url = connection_string.replace("postgresql+psycopg://", "postgresql://")
        return {
            "connection_string": pg_url,
        }

    def load(self, file_paths: List[str], metadatas: Optional[List[dict]] = None):
        """
        Load documents from file paths into the knowledge base.

        Args:
            file_paths: List of file paths to load.
            metadatas: Optional list of metadata dicts for each file.
        """
        self._loaded_file_paths = file_paths
        print(f"   Prepared {len(file_paths)} files for AutoGen RAG")

        # Create RetrieveUserProxyAgent with document paths
        # Documents will be loaded when initiate_chat is called
        self._ragproxyagent = RetrieveUserProxyAgent(
            name="ragproxyagent",
            human_input_mode="NEVER",
            max_consecutive_auto_reply=3,
            retrieve_config={
                "task": "qa",
                "docs_path": file_paths,
                "chunk_token_size": self._chunk_size,
                "model": self._config_list[0]["model"],
                "vector_db": "pgvector",
                "collection_name": self._collection_name,
                "db_config": self._db_config,
                "get_or_create": False,
                "overwrite": True,
                "top_k": self._max_results,
            },
            code_execution_config=False,
        )
        print(f"   Initialized RetrieveUserProxyAgent with PGVector")

    def search(self, query: str, k: int = 4) -> List[SearchResult]:
        """
        Search for relevant documents with similarity scores.

        Args:
            query: Search query string.
            k: Number of documents to return.

        Returns:
            List of SearchResult objects.
        """
        if not self._ragproxyagent:
            return []

        try:
            # Use the retrieve_docs method to search
            results = self._ragproxyagent.retrieve_docs(
                problem=query,
                n_results=k,
            )

            search_results = []
            if results:
                docs = results[0] if isinstance(results, tuple) else results
                for i, doc in enumerate(docs if isinstance(docs, list) else [docs]):
                    content = doc if isinstance(doc, str) else str(doc)
                    search_results.append(SearchResult(
                        content=content,
                        score=1.0 - (i * 0.1),
                        metadata={},
                    ))
            return search_results
        except Exception as e:
            print(f"   Search error: {e}")
            return []

    def answer(self, question: str, k: int = 4) -> Tuple[str, List[SearchResult]]:
        """
        Answer a question using AutoGen RetrieveUserProxyAgent.

        Args:
            question: The question to answer.
            k: Number of documents to retrieve for context.

        Returns:
            Tuple of (answer_text, search_results).
        """
        self._last_search_results = []

        if not self._ragproxyagent:
            # Create agent if not already created
            self._ragproxyagent = RetrieveUserProxyAgent(
                name="ragproxyagent",
                human_input_mode="NEVER",
                max_consecutive_auto_reply=3,
                retrieve_config={
                    "task": "qa",
                    "docs_path": self._loaded_file_paths,
                    "chunk_token_size": self._chunk_size,
                    "model": self._config_list[0]["model"],
                    "vector_db": "pgvector",
                    "collection_name": self._collection_name,
                    "db_config": self._db_config,
                    "get_or_create": True,
                    "overwrite": False,
                    "top_k": k,
                },
                code_execution_config=False,
            )

        # Create AssistantAgent
        assistant = AssistantAgent(
            name="assistant",
            system_message=SYSTEM_MESSAGE,
            llm_config=self._llm_config,
        )

        # Custom message generator to capture context
        last_results = self._last_search_results

        def custom_message_generator(sender, recipient, context):
            """Generate message with retrieved context."""
            problem = context.get("problem", "")
            
            # Retrieve documents
            sender.retrieve_docs(problem=problem, n_results=k)
            
            # Get context from retrieved docs
            doc_contents = sender._get_context(sender._results)
            
            # Capture search results
            if sender._results:
                docs = sender._results.get("documents", [[]])[0]
                distances = sender._results.get("distances", [[]])[0]
                
                for i, doc in enumerate(docs):
                    score = 1.0 - distances[i] if i < len(distances) else 0.5
                    last_results.append(SearchResult(
                        content=doc,
                        score=score,
                        metadata={},
                    ))
            
            # Build message with context
            message = f"""Context information is below.
---------------------
{doc_contents}
---------------------
Given the context information and not prior knowledge, answer the question.
Question: {problem}
"""
            return message

        # Initiate chat
        try:
            if self._ragproxyagent is None:
                return "Error: RAG agent not initialized", self._last_search_results
                
            chat_result = self._ragproxyagent.initiate_chat(
                assistant,
                message=custom_message_generator,
                problem=question,
                silent=True,
            )

            # Extract answer from chat history
            answer = ""
            if chat_result and chat_result.chat_history:
                for msg in reversed(chat_result.chat_history):
                    if msg.get("role") == "assistant" or msg.get("name") == "assistant":
                        content = msg.get("content", "")
                        if content:
                            answer = content.strip()
                            break

            return answer, self._last_search_results

        except Exception as e:
            print(f"   Answer error: {e}")
            return f"Error: {e}", self._last_search_results
