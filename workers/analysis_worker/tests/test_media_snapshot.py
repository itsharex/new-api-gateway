import pytest

from media_snapshot import MediaSnapshotPolicy, validate_snapshot_url


def test_rejects_non_http_urls():
    with pytest.raises(ValueError, match="http/https"):
        validate_snapshot_url("file:///etc/passwd", MediaSnapshotPolicy())


def test_rejects_metadata_ip():
    with pytest.raises(ValueError, match="private or metadata"):
        validate_snapshot_url("http://169.254.169.254/latest/meta-data", MediaSnapshotPolicy())


def test_rejects_private_ip_without_allowlist():
    with pytest.raises(ValueError, match="private or metadata"):
        validate_snapshot_url("http://10.0.0.5/image.png", MediaSnapshotPolicy())


@pytest.mark.parametrize(
    "url",
    [
        "http://127.0.0.1/image.png",
        "http://169.254.1.1/image.png",
        "http://240.0.0.1/image.png",
        "http://224.0.0.1/image.png",
    ],
)
def test_rejects_loopback_link_local_reserved_and_multicast_ips(url):
    with pytest.raises(ValueError, match="private or metadata"):
        validate_snapshot_url(url, MediaSnapshotPolicy())


def test_allows_configured_company_domain():
    policy = MediaSnapshotPolicy(allowed_domains={"assets.company.test"})
    result = validate_snapshot_url("https://assets.company.test/a.png", policy)
    assert result.hostname == "assets.company.test"
