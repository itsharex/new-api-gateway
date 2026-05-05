from pathlib import Path
from typing import Protocol, runtime_checkable


@runtime_checkable
class EvidenceStore(Protocol):
    def read_text(self, object_ref: str) -> str: ...
    def read_bytes(self, object_ref: str) -> bytes: ...
    def write_text(self, object_ref: str, data: str) -> None: ...
    def write_bytes(self, object_ref: str, data: bytes) -> None: ...


class FilesystemEvidenceStore:
    def __init__(self, root: str | Path):
        self.root = Path(root).resolve()

    def read_text(self, object_ref: str) -> str:
        path = self._path_for_ref(object_ref)
        return path.read_text(encoding="utf-8")

    def read_bytes(self, object_ref: str) -> bytes:
        path = self._path_for_ref(object_ref)
        return path.read_bytes()

    def write_text(self, object_ref: str, data: str) -> None:
        path = self._path_for_ref(object_ref)
        path.parent.mkdir(parents=True, exist_ok=True)
        tmp = path.with_suffix(path.suffix + ".tmp")
        try:
            tmp.write_text(data, encoding="utf-8")
            tmp.replace(path)
        except BaseException:
            tmp.unlink(missing_ok=True)
            raise

    def write_bytes(self, object_ref: str, data: bytes) -> None:
        path = self._path_for_ref(object_ref)
        path.parent.mkdir(parents=True, exist_ok=True)
        path.write_bytes(data)

    def _path_for_ref(self, object_ref: str) -> Path:
        if not object_ref:
            raise ValueError("object ref is empty")
        if "\\" in object_ref or "//" in object_ref or ".." in object_ref:
            raise ValueError(f"invalid object ref {object_ref!r}")
        ref_path = Path(object_ref)
        if ref_path.is_absolute():
            raise ValueError(f"invalid object ref {object_ref!r}")
        candidate = (self.root / ref_path).resolve()
        if candidate != self.root and self.root not in candidate.parents:
            raise ValueError(f"object ref escapes evidence root {object_ref!r}")
        return candidate
