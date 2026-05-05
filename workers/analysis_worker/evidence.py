from pathlib import Path
from typing import Protocol, runtime_checkable

_FILE_SCHEME = "file:///"


@runtime_checkable
class EvidenceStore(Protocol):
    def read_text(self, object_ref: str) -> str: ...
    def read_bytes(self, object_ref: str) -> bytes: ...
    def write_text(self, object_ref: str, data: str) -> str: ...
    def write_bytes(self, object_ref: str, data: bytes) -> str: ...


class FilesystemEvidenceStore:
    def __init__(self, root: str | Path):
        self.root = Path(root).resolve()

    def read_text(self, object_ref: str) -> str:
        path = self._path_for_ref(object_ref)
        return path.read_text(encoding="utf-8")

    def read_bytes(self, object_ref: str) -> bytes:
        path = self._path_for_ref(object_ref)
        return path.read_bytes()

    def write_text(self, object_ref: str, data: str) -> str:
        path = self._path_for_ref(object_ref)
        path.parent.mkdir(parents=True, exist_ok=True)
        tmp = path.with_suffix(path.suffix + ".tmp")
        try:
            tmp.write_text(data, encoding="utf-8")
            tmp.replace(path)
        except BaseException:
            tmp.unlink(missing_ok=True)
            raise
        return object_ref

    def write_bytes(self, object_ref: str, data: bytes) -> str:
        path = self._path_for_ref(object_ref)
        path.parent.mkdir(parents=True, exist_ok=True)
        path.write_bytes(data)
        return object_ref

    def _path_for_ref(self, object_ref: str) -> Path:
        if not object_ref.startswith(_FILE_SCHEME):
            raise ValueError(f"invalid object ref {object_ref!r}: must start with {_FILE_SCHEME}")
        relative = object_ref[len(_FILE_SCHEME):]
        if not relative:
            raise ValueError(f"invalid object ref {object_ref!r}: empty path")
        if "\\" in relative or "//" in relative or ".." in relative:
            raise ValueError(f"invalid object ref {object_ref!r}")
        ref_path = Path(relative)
        if ref_path.is_absolute():
            raise ValueError(f"invalid object ref {object_ref!r}")
        candidate = (self.root / ref_path).resolve()
        if candidate != self.root and self.root not in candidate.parents:
            raise ValueError(f"object ref escapes evidence root {object_ref!r}")
        return candidate
