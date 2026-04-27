import json
import sys
from dataclasses import dataclass


@dataclass(frozen=True)
class TraceCapturedJob:
    type: str
    trace_id: str
    route_pattern: str
    protocol_family: str
    capture_mode: str
    employee_no: str


def parse_job(line: str) -> TraceCapturedJob:
    data = json.loads(line)
    return TraceCapturedJob(**data)


def main() -> int:
    payload = sys.stdin.read().strip()
    if not payload:
        return 0
    job = parse_job(payload)
    print(json.dumps({"accepted_trace_id": job.trace_id, "worker_status": "accepted"}))
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
