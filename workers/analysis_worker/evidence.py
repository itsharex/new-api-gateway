from pathlib import Path


class FileEvidenceStore:
    def __init__(self, root: str | Path):
        self.root = Path(root).resolve()

    def read_text(self, object_ref: str) -> str:
        path = self._path_for_ref(object_ref)
        return path.read_text(encoding="utf-8")

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
