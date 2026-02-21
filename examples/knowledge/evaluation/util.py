"""
Utility functions for RAG evaluation.
"""

import os
from urllib.parse import quote_plus

# Default configuration values
DEFAULT_EMBEDDING_MODEL = "bge-m3"
DEFAULT_MODEL_NAME = "deepseek-v3.2"
DEFAULT_EVAL_MODEL_NAME = "gemini-3-flash"  # Default evaluation model
DEFAULT_OPENAI_BASE_URL = "https://api.openai.com/v1"

# PGVector defaults
DEFAULT_PGVECTOR_HOST = "127.0.0.1"
DEFAULT_PGVECTOR_PORT = "5432"
DEFAULT_PGVECTOR_USER = "root"
DEFAULT_PGVECTOR_PASSWORD = "123"
DEFAULT_PGVECTOR_DATABASE = "vector"

# ChromaDB defaults
DEFAULT_CHROMADB_PATH = "./chromadb_storage"


def get_pg_connection() -> str:
    """
    Build PostgreSQL connection string from environment variables.

    Environment variables:
        PGVECTOR_HOST: PostgreSQL host (default: 127.0.0.1)
        PGVECTOR_PORT: PostgreSQL port (default: 5432)
        PGVECTOR_USER: PostgreSQL user (default: root)
        PGVECTOR_PASSWORD: PostgreSQL password (default: empty)
        PGVECTOR_DATABASE: PostgreSQL database (default: vector)

    Returns:
        PostgreSQL connection string for SQLAlchemy.
    """
    host = os.environ.get("PGVECTOR_HOST", DEFAULT_PGVECTOR_HOST)
    port = os.environ.get("PGVECTOR_PORT", DEFAULT_PGVECTOR_PORT)
    user = os.environ.get("PGVECTOR_USER", DEFAULT_PGVECTOR_USER)
    password = os.environ.get("PGVECTOR_PASSWORD", DEFAULT_PGVECTOR_PASSWORD)
    database = os.environ.get("PGVECTOR_DATABASE", DEFAULT_PGVECTOR_DATABASE)

    if not password:
        print(f"WARNING: PGVECTOR_PASSWORD not set, using empty password")
        print(f"  Current env vars: PGVECTOR_HOST={host}, PGVECTOR_USER={user}, PGVECTOR_DATABASE={database}")

    encoded_user = quote_plus(user)
    encoded_password = quote_plus(password)

    return f"postgresql+psycopg://{encoded_user}:{encoded_password}@{host}:{port}/{database}"


def get_chromadb_config() -> dict:
    """
    Get ChromaDB configuration from environment variables.

    Environment variables:
        CHROMADB_PATH: Path to ChromaDB storage directory (default: ./chromadb_storage)

    Returns:
        Dict with ChromaDB configuration.
    """
    return {
        "path": os.environ.get("CHROMADB_PATH", DEFAULT_CHROMADB_PATH),
    }


def get_config():
    """
    Get configuration from environment variables with defaults.

    Environment variables:
        EMBEDDING_MODEL: Embedding model name (default: server:274214)
        MODEL_NAME: LLM model name for knowledge/RAG (default: deepseek-v3.2)
        EVAL_MODEL_NAME: LLM model name for evaluation (default: gemini-3-flash)
        OPENAI_API_KEY: OpenAI API key (required)
        OPENAI_BASE_URL: OpenAI API base URL (default: https://api.openai.com/v1)
        EVAL_API_KEY: Evaluation model API key (default: same as OPENAI_API_KEY)
        EVAL_BASE_URL: Evaluation model base URL (default: same as OPENAI_BASE_URL)
        PGVECTOR_*: PostgreSQL connection parameters
        CHROMADB_*: ChromaDB configuration

    Returns:
        Dict with configuration values.
    """
    api_key = os.environ.get("OPENAI_API_KEY", "")
    base_url = os.environ.get("OPENAI_BASE_URL", DEFAULT_OPENAI_BASE_URL)
    chroma_api_key = os.environ.get("CHROMA_OPENAI_API_KEY")
    chroma_api_base = os.environ.get("CHROMA_OPENAI_API_BASE")
    
    return {
        # Knowledge/RAG model config
        "embedding_model": os.environ.get("EMBEDDING_MODEL", DEFAULT_EMBEDDING_MODEL),
        "model_name": os.environ.get("MODEL_NAME", DEFAULT_MODEL_NAME),
        "api_key": api_key,
        "base_url": base_url,
        # Evaluation model config (can use different model/endpoint)
        "eval_model_name": os.environ.get("EVAL_MODEL_NAME", DEFAULT_EVAL_MODEL_NAME),
        "eval_api_key": os.environ.get("EVAL_API_KEY", api_key),
        "eval_base_url": os.environ.get("EVAL_BASE_URL", base_url),
        # Database config
        "pg_connection": get_pg_connection(),
        # ChromaDB config
        "chromadb": {
            **get_chromadb_config(),
            "api_key": chroma_api_key or api_key,
            "api_base": chroma_api_base or base_url,
        },
    }
