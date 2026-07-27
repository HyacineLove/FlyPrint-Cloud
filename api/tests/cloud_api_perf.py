import json
import statistics
import sys
import time
from concurrent.futures import ThreadPoolExecutor, as_completed
from typing import Any, Dict, List

import requests

from cloud_test_utils import auth_headers, ensure_results_dir, get_admin_token, get_base_url, timestamp_slug


def percentile(values: List[float], p: float) -> float:
    if not values:
        return 0.0
    ordered = sorted(values)
    index = int(round((len(ordered) - 1) * p))
    return ordered[index]


def single_request(endpoint: Dict[str, Any], base_url: str, token: str) -> Dict[str, Any]:
    url = f"{base_url}{endpoint['path']}"
    headers = {}
    if endpoint.get("auth"):
        headers.update(auth_headers(token))
    start = time.perf_counter()
    response = requests.request(
        method=endpoint["method"],
        url=url,
        headers=headers,
        data=endpoint.get("data"),
        timeout=20,
    )
    elapsed_ms = (time.perf_counter() - start) * 1000
    return {
        "ok": response.status_code == endpoint["expected_status"],
        "status_code": response.status_code,
        "elapsed_ms": elapsed_ms,
    }


def run_endpoint_profile(
    endpoint: Dict[str, Any],
    base_url: str,
    token: str,
    concurrency: int,
    rounds_per_level: int,
) -> Dict[str, Any]:
    latencies: List[float] = []
    errors = 0
    started = time.perf_counter()
    for round_index in range(rounds_per_level):
        with ThreadPoolExecutor(max_workers=concurrency) as executor:
            futures = [
                executor.submit(single_request, endpoint, base_url, token)
                for _ in range(concurrency)
            ]
            for future in as_completed(futures):
                result = future.result()
                latencies.append(result["elapsed_ms"])
                if not result["ok"]:
                    errors += 1
        if round_index != rounds_per_level - 1:
            time.sleep(1.1)
    total_requests = concurrency * rounds_per_level
    total_elapsed = time.perf_counter() - started
    return {
        "endpoint": endpoint["name"],
        "path": endpoint["path"],
        "concurrency": concurrency,
        "requests": total_requests,
        "success_count": total_requests - errors,
        "error_count": errors,
        "avg_ms": round(statistics.mean(latencies), 2) if latencies else 0.0,
        "min_ms": round(min(latencies), 2) if latencies else 0.0,
        "max_ms": round(max(latencies), 2) if latencies else 0.0,
        "p95_ms": round(percentile(latencies, 0.95), 2) if latencies else 0.0,
        "throughput_rps": round(total_requests / total_elapsed, 2) if total_elapsed > 0 else 0.0,
    }


def main() -> int:
    base_url = get_base_url()
    endpoints = [
        {"name": "auth_mode", "method": "GET", "path": "/auth/mode", "expected_status": 200, "auth": False},
        {"name": "api_health", "method": "GET", "path": "/api/v1/health", "expected_status": 200, "auth": False},
        {
            "name": "auth_token",
            "method": "POST",
            "path": "/auth/token",
            "expected_status": 200,
            "auth": False,
            "data": {
                "grant_type": "password",
                "username": "admin@flyprint.local",
                "password": "admin123",
                "scope": "fly-print-admin fly-print-operator edge:register edge:printer print:submit",
            },
        },
        {"name": "dashboard_trends", "method": "GET", "path": "/api/v1/admin/dashboard/trends", "expected_status": 200, "auth": True},
        {"name": "edge_nodes", "method": "GET", "path": "/api/v1/admin/edge-nodes", "expected_status": 200, "auth": True},
        {"name": "printers", "method": "GET", "path": "/api/v1/admin/printers", "expected_status": 200, "auth": True},
        {"name": "print_jobs", "method": "GET", "path": "/api/v1/admin/print-jobs", "expected_status": 200, "auth": True},
    ]
    concurrency_levels = [1, 5, 10]
    rounds_per_level = 5

    with requests.Session() as session:
        admin_token = get_admin_token(session)

    records: List[Dict[str, Any]] = []
    for endpoint in endpoints:
        for concurrency in concurrency_levels:
            record = run_endpoint_profile(
                endpoint,
                base_url,
                admin_token,
                concurrency,
                rounds_per_level,
            )
            records.append(record)
            print(
                f"{endpoint['name']} c={concurrency}: "
                f"avg={record['avg_ms']}ms p95={record['p95_ms']}ms "
                f"errors={record['error_count']}"
            )

    summary = {
        "base_url": base_url,
        "concurrency_levels": concurrency_levels,
        "rounds_per_level": rounds_per_level,
        "records": records,
    }

    output_path = ensure_results_dir() / f"cloud_api_perf_{timestamp_slug()}.json"
    output_path.write_text(json.dumps(summary, ensure_ascii=False, indent=2), encoding="utf-8")
    print(f"Performance result file: {output_path}")
    return 0


if __name__ == "__main__":
    sys.exit(main())
