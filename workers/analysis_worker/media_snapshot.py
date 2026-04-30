from dataclasses import dataclass, field
from ipaddress import IPv4Address, IPv6Address, ip_address
import socket
from typing import Callable, Iterable
from urllib.parse import ParseResult, urlparse


METADATA_HOSTS = {"169.254.169.254", "metadata.google.internal"}
LOCALHOST_HOSTS = {"localhost"}
Resolver = Callable[[str], Iterable[str]]


@dataclass(frozen=True)
class MediaSnapshotPolicy:
    allowed_domains: set[str] = field(default_factory=set)
    resolver: Resolver | None = None
    max_size_bytes: int = 20 * 1024 * 1024
    redirect_limit: int = 3
    mime_allowlist: set[str] = field(
        default_factory=lambda: {"image/png", "image/jpeg", "image/webp", "audio/mpeg", "audio/wav"}
    )


def _default_resolver(hostname: str) -> list[str]:
    try:
        infos = socket.getaddrinfo(hostname, None, type=socket.SOCK_STREAM)
    except socket.gaierror as exc:
        raise ValueError("media snapshot url host could not be resolved") from exc
    return sorted({info[4][0] for info in infos})


def _canonical_hostname(hostname: str) -> str:
    value = hostname.strip().rstrip(".").lower()
    try:
        return value.encode("idna").decode("ascii")
    except UnicodeError as exc:
        raise ValueError("media snapshot url host is invalid") from exc


def _canonical_allowed_domains(domains: set[str]) -> set[str]:
    return {_canonical_hostname(domain) for domain in domains if domain.strip()}


def _address_is_blocked(address: IPv4Address | IPv6Address) -> bool:
    return (
        address.is_private
        or address.is_loopback
        or address.is_link_local
        or address.is_reserved
        or address.is_multicast
        or address.is_unspecified
        or str(address) == "169.254.169.254"
    )


def _canonical_parse_result(parsed: ParseResult, hostname: str) -> ParseResult:
    host = f"[{hostname}]" if ":" in hostname else hostname
    if parsed.port is not None:
        host = f"{host}:{parsed.port}"
    return parsed._replace(netloc=host)


def validate_snapshot_url(raw_url: str, policy: MediaSnapshotPolicy) -> ParseResult:
    parsed = urlparse(raw_url)
    if parsed.scheme not in {"http", "https"}:
        raise ValueError("media snapshot url must use http/https")
    if not parsed.hostname:
        raise ValueError("media snapshot url host is required")

    hostname = _canonical_hostname(parsed.hostname)
    if hostname in METADATA_HOSTS or hostname in LOCALHOST_HOSTS:
        raise ValueError("media snapshot url resolves to private or metadata address")

    try:
        address = ip_address(hostname)
    except ValueError:
        address = None

    allowed_domains = _canonical_allowed_domains(policy.allowed_domains)
    if allowed_domains and hostname not in allowed_domains:
        raise ValueError("media snapshot url host is not allowlisted")

    addresses = [address] if address is not None else [
        ip_address(resolved) for resolved in (policy.resolver or _default_resolver)(hostname)
    ]
    if not addresses:
        raise ValueError("media snapshot url host could not be resolved")
    if any(_address_is_blocked(resolved) for resolved in addresses):
        raise ValueError("media snapshot url resolves to private or metadata address")
    return _canonical_parse_result(parsed, hostname)
