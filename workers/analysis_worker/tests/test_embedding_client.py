import pytest
from unittest.mock import patch, MagicMock

import httpx

from embedding_client import EmbeddingClient


def _mock_response(embeddings: list[list[float]], status_code: int = 200):
    """Build a MagicMock that behaves like an httpx.Response."""
    resp = MagicMock()
    resp.status_code = status_code
    resp.raise_for_status = MagicMock()
    resp.json.return_value = {
        "data": [{"embedding": emb} for emb in embeddings],
    }
    return resp


def test_embed_calls_api_and_returns_vector():
    mock_resp = _mock_response([[0.1] * 1024])

    with patch("embedding_client.httpx.post", return_value=mock_resp) as mock_post:
        client = EmbeddingClient()
        result = client.embed("hello world")

    assert len(result) == 1024
    assert result == [0.1] * 1024
    mock_post.assert_called_once()
    call_kwargs = mock_post.call_args
    assert call_kwargs[1]["json"]["input"] == "hello world"


def test_embed_batch_returns_list():
    mock_resp = _mock_response([[0.1] * 1024, [0.2] * 1024])

    with patch("embedding_client.httpx.post", return_value=mock_resp) as mock_post:
        client = EmbeddingClient()
        result = client.embed_batch(["hello", "world"])

    assert len(result) == 2
    assert result[0] == [0.1] * 1024
    assert result[1] == [0.2] * 1024
    mock_post.assert_called_once()


def test_embed_batch_empty_returns_empty():
    with patch("embedding_client.httpx.post") as mock_post:
        client = EmbeddingClient()
        result = client.embed_batch([])

    assert result == []
    mock_post.assert_not_called()


def test_wait_until_ready_succeeds_immediately():
    mock_resp = MagicMock()
    mock_resp.status_code = 200
    with patch("embedding_client.httpx.get", return_value=mock_resp) as mock_get:
        client = EmbeddingClient("http://test-embed:80")
        client.wait_until_ready(timeout=5, interval=0.1)
    mock_get.assert_called_once_with("http://test-embed:80/health", timeout=3.0)


def test_wait_until_ready_retries_then_succeeds():
    resp_503 = MagicMock()
    resp_503.status_code = 503
    resp_200 = MagicMock()
    resp_200.status_code = 200
    with patch("embedding_client.httpx.get", side_effect=[resp_503, resp_503, resp_200]) as mock_get:
        client = EmbeddingClient()
        client.wait_until_ready(timeout=5, interval=0.01)
    assert mock_get.call_count == 3


def test_wait_until_ready_raises_on_timeout():
    resp_503 = MagicMock()
    resp_503.status_code = 503
    with patch("embedding_client.httpx.get", return_value=resp_503):
        client = EmbeddingClient("http://test-embed:80")
        with pytest.raises(RuntimeError, match=r"embedding service not ready at http://test-embed:80 after"):
            client.wait_until_ready(timeout=0.05, interval=0.02)


def test_wait_until_ready_retries_on_connect_error():
    resp_200 = MagicMock()
    resp_200.status_code = 200
    with patch("embedding_client.httpx.get", side_effect=[
        httpx.ConnectError("connection refused"),
        resp_200,
    ]) as mock_get:
        client = EmbeddingClient()
        client.wait_until_ready(timeout=5, interval=0.01)
    assert mock_get.call_count == 2
