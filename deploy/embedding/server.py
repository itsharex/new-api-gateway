"""Lightweight embedding server compatible with HuggingFace TEI /v1/embeddings API."""

import time

from fastapi import FastAPI
from fastapi.responses import JSONResponse
from pydantic import BaseModel
from sentence_transformers import SentenceTransformer

MODEL_NAME = "BAAI/bge-m3"

app = FastAPI()

_model: SentenceTransformer | None = None


class EmbeddingRequest(BaseModel):
    input: str | list[str]
    model: str = MODEL_NAME


class EmbeddingResponse(BaseModel):
    object: str = "list"
    data: list[dict]
    model: str
    usage: dict


@app.on_event("startup")
def load_model() -> None:
    global _model
    _model = SentenceTransformer(MODEL_NAME)


@app.get("/health")
def health() -> dict:
    if _model is None:
        return JSONResponse({"status": "loading"}, status_code=503)
    return {"status": "ok"}


@app.post("/v1/embeddings")
def embeddings(req: EmbeddingRequest) -> EmbeddingResponse:
    if _model is None:
        return JSONResponse({"error": "model not ready"}, status_code=503)

    texts = req.input if isinstance(req.input, list) else [req.input]
    vectors = _model.encode(texts, normalize_embeddings=True).tolist()

    data = [
        {"object": "embedding", "index": i, "embedding": vec}
        for i, vec in enumerate(vectors)
    ]
    return EmbeddingResponse(
        data=data,
        model=MODEL_NAME,
        usage={"prompt_tokens": sum(len(t.split()) for t in texts), "total_tokens": sum(len(t.split()) for t in texts)},
    )
