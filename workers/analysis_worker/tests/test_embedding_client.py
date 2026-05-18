from unittest.mock import patch, MagicMock

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
