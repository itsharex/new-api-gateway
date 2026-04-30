from dataclasses import dataclass, field
from ipaddress import ip_address
from urllib.parse import ParseResult, urlparse


METADATA_HOSTS = {"169.254.169.254", "metadata.google.internal"}


@dataclass(frozen=True)
class MediaSnapshotPolicy:
    allowed_domains: set[str] = field(default_factory=set)
    max_size_bytes: int = 20 * 1024 * 1024
    redirect_limit: int = 3
    mime_allowlist: set[str] = field(
        default_factory=lambda: {"image/png", "image/jpeg", "image/webp", "audio/mpeg", "audio/wav"}
    )


def validate_snapshot_url(raw_url: str, policy: MediaSnapshotPolicy) -> ParseResult:
    parsed = urlparse(raw_url)
    if parsed.scheme not in {"http", "https"}:
        raise ValueError("media snapshot url must use http/https")
    if not parsed.hostname:
        raise ValueError("media snapshot url host is required")

    hostname = parsed.hostname.lower()
    if hostname in {domain.lower() for domain in policy.allowed_domains}:
        return parsed
    if hostname in METADATA_HOSTS:
        raise ValueError("media snapshot url resolves to private or metadata address")

    try:
        address = ip_address(hostname)
    except ValueError:
        return parsed

    if (
        address.is_private
        or address.is_loopback
        or address.is_link_local
        or address.is_reserved
        or address.is_multicast
    ):
        raise ValueError("media snapshot url resolves to private or metadata address")
    return parsed
