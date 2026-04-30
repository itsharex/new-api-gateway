import pytest

from media_snapshot import MediaSnapshotPolicy, validate_snapshot_url


def fake_resolver(*addresses):
    def resolve(_hostname):
        return list(addresses)

    return resolve


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


@pytest.mark.parametrize(
    "url",
    [
        "http://localhost/image.png",
        "http://localhost./image.png",
        "http://metadata.google.internal./computeMetadata/v1/",
    ],
)
def test_rejects_localhost_and_metadata_hostname_variants(url):
    with pytest.raises(ValueError, match="private or metadata"):
        validate_snapshot_url(url, MediaSnapshotPolicy(resolver=fake_resolver("93.184.216.34")))


@pytest.mark.parametrize("resolved_ip", ["10.0.0.5", "::1"])
def test_rejects_hostnames_that_resolve_to_private_or_loopback_ips(resolved_ip):
    policy = MediaSnapshotPolicy(resolver=fake_resolver(resolved_ip))
    with pytest.raises(ValueError, match="private or metadata"):
        validate_snapshot_url("https://assets.example.test/image.png", policy)


@pytest.mark.parametrize("resolved_ip", ["100.64.0.1", "100.127.255.254"])
def test_rejects_hostnames_that_resolve_to_shared_address_space(resolved_ip):
    policy = MediaSnapshotPolicy(resolver=fake_resolver(resolved_ip))
    with pytest.raises(ValueError, match="private or metadata"):
        validate_snapshot_url("https://assets.example.test/image.png", policy)


def test_rejects_non_allowlisted_domain_when_allowlist_is_configured():
    policy = MediaSnapshotPolicy(
        allowed_domains={"assets.company.test"},
        resolver=fake_resolver("93.184.216.34"),
    )
    with pytest.raises(ValueError, match="not allowlisted"):
        validate_snapshot_url("https://cdn.company.test/a.png", policy)


def test_allows_public_hostname_when_no_allowlist_is_configured():
    policy = MediaSnapshotPolicy(resolver=fake_resolver("93.184.216.34"))
    result = validate_snapshot_url("https://public.example.test/a.png", policy)

    assert result.hostname == "public.example.test"


def test_validated_url_carries_resolved_public_addresses_for_downloader_binding():
    policy = MediaSnapshotPolicy(resolver=fake_resolver("93.184.216.34", "2606:2800:220:1:248:1893:25c8:1946"))
    result = validate_snapshot_url("https://public.example.test/a.png", policy)

    assert result.canonical_hostname == "public.example.test"
    assert [str(address) for address in result.resolved_addresses] == [
        "93.184.216.34",
        "2606:2800:220:1:248:1893:25c8:1946",
    ]
    assert result.parsed.hostname == "public.example.test"


def test_allowlisted_domain_still_rejects_private_resolved_ip():
    policy = MediaSnapshotPolicy(
        allowed_domains={"assets.company.test"},
        resolver=fake_resolver("10.0.0.5"),
    )
    with pytest.raises(ValueError, match="private or metadata"):
        validate_snapshot_url("https://assets.company.test/a.png", policy)


def test_allows_configured_company_domain():
    policy = MediaSnapshotPolicy(
        allowed_domains={"assets.company.test"},
        resolver=fake_resolver("93.184.216.34"),
    )
    result = validate_snapshot_url("https://ASSETS.Company.Test./a.png", policy)
    assert result.hostname == "assets.company.test"
