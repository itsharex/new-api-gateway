import logging
import time

import httpx

logger = logging.getLogger(__name__)


class EmbeddingClient:
    def __init__(self, base_url: str = "http://localhost:8081"):
        self.base_url = base_url.rstrip("/")

    def embed(self, text: str) -> list[float]:
        response = httpx.post(
            f"{self.base_url}/v1/embeddings",
            json={"input": text, "model": "BAAI/bge-m3"},
            timeout=30.0,
        )
        response.raise_for_status()
        return response.json()["data"][0]["embedding"]

    def embed_batch(self, texts: list[str]) -> list[list[float]]:
        if not texts:
            return []
        response = httpx.post(
            f"{self.base_url}/v1/embeddings",
            json={"input": texts, "model": "BAAI/bge-m3"},
            timeout=60.0,
        )
        response.raise_for_status()
        return [item["embedding"] for item in response.json()["data"]]

    def wait_until_ready(self, timeout: float = 300.0, interval: float = 1.0) -> None:
        """Block until the embedding service /health endpoint returns 200.

        Polls at ``interval`` seconds. Raises RuntimeError if the service
        does not become healthy within ``timeout`` seconds.
        """
        deadline = time.monotonic() + timeout
        last_unexpected: Exception | None = None
        while True:
            try:
                resp = httpx.get(f"{self.base_url}/health", timeout=3.0)
                if resp.status_code == 200:
                    return
            except (httpx.ConnectError, httpx.TimeoutException):
                pass  # Expected during startup; retry.
            except Exception as exc:
                last_unexpected = exc
                logger.warning("unexpected error probing %s/health: %s", self.base_url, exc)
            remaining = deadline - time.monotonic()
            if remaining <= 0:
                parts = [f"embedding service not ready at {self.base_url} after {timeout}s"]
                if last_unexpected is not None:
                    parts.append(f"(last unexpected error: {last_unexpected!r})")
                raise RuntimeError("; ".join(parts))
            time.sleep(min(interval, remaining))
