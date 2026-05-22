import httpx


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

    def wait_until_ready(self, timeout: float = 30.0, interval: float = 1.0) -> None:
        import time

        deadline = time.monotonic() + timeout
        while True:
            try:
                resp = httpx.get(f"{self.base_url}/health", timeout=3.0)
                if resp.status_code == 200:
                    return
            except Exception:
                pass
            remaining = deadline - time.monotonic()
            if remaining <= 0:
                raise RuntimeError("embedding service not ready")
            time.sleep(min(interval, remaining))
