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
    request_raw_ref: str = ""
    request_headers_ref: str = ""
    response_raw_ref: str = ""
    response_headers_ref: str = ""
    request_content_type: str = ""
    response_content_type: str = ""
    model_requested: str = ""
    usage_prompt_tokens: int = 0
    usage_completion_tokens: int = 0
    usage_total_tokens: int = 0
    usage_reasoning_tokens: int = 0
    usage_cached_tokens: int = 0


def parse_job(line: str) -> TraceCapturedJob:
    data = json.loads(line)
    known = {field: data.get(field, TraceCapturedJob.__dataclass_fields__[field].default) for field in TraceCapturedJob.__dataclass_fields__}
    return TraceCapturedJob(**known)


def main() -> int:
    payload = sys.stdin.read().strip()
    if not payload:
        return 0
    job = parse_job(payload)
    print(json.dumps({
        "accepted_trace_id": job.trace_id,
        "worker_status": "accepted",
        "response_raw_ref": job.response_raw_ref,
        "usage_total_tokens": job.usage_total_tokens
    }))
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
