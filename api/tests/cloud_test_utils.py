import json
import os
import time
from pathlib import Path
from typing import Any, Dict, Optional

import requests


ROOT_DIR = Path(__file__).resolve().parents[3]
EDGE_CONFIG_PATH = ROOT_DIR / "fly-print-edge" / "config.json"
RESULTS_DIR = ROOT_DIR / "output" / "test-results"


def ensure_results_dir() -> Path:
    RESULTS_DIR.mkdir(parents=True, exist_ok=True)
    return RESULTS_DIR


def load_edge_config() -> Dict[str, Any]:
    if not EDGE_CONFIG_PATH.exists():
        return {}
    return json.loads(EDGE_CONFIG_PATH.read_text(encoding="utf-8"))


def get_base_url() -> str:
    return os.environ.get("FLYPRINT_BASE_URL", "http://127.0.0.1:8012").rstrip("/")


def get_admin_credentials() -> Dict[str, str]:
    return {
        "username": os.environ.get("FLYPRINT_ADMIN_EMAIL", "admin@flyprint.local"),
        "password": os.environ.get("FLYPRINT_ADMIN_PASSWORD", "admin123"),
    }


def get_edge_client_credentials() -> Dict[str, str]:
    cfg = load_edge_config()
    cloud_cfg = cfg.get("cloud", {})
    return {
        "client_id": os.environ.get("FLYPRINT_EDGE_CLIENT_ID", cloud_cfg.get("client_id", "edge-default")),
        "client_secret": os.environ.get("FLYPRINT_EDGE_CLIENT_SECRET", cloud_cfg.get("client_secret", "")),
    }


def get_admin_token(session: Optional[requests.Session] = None) -> str:
    sess = session or requests.Session()
    creds = get_admin_credentials()
    payload = {
        "grant_type": "password",
        "username": creds["username"],
        "password": creds["password"],
        "scope": "fly-print-admin fly-print-operator edge:register edge:printer print:submit",
    }
    resp = sess.post(f"{get_base_url()}/auth/token", data=payload, timeout=15)
    resp.raise_for_status()
    return resp.json()["access_token"]


def get_edge_token(session: Optional[requests.Session] = None) -> str:
    sess = session or requests.Session()
    creds = get_edge_client_credentials()
    payload = {
        "grant_type": "client_credentials",
        "client_id": creds["client_id"],
        "client_secret": creds["client_secret"],
        "scope": "edge:register edge:printer file:read",
    }
    resp = sess.post(f"{get_base_url()}/auth/token", data=payload, timeout=15)
    resp.raise_for_status()
    return resp.json()["access_token"]


def auth_headers(token: str) -> Dict[str, str]:
    return {"Authorization": f"Bearer {token}"}


def timestamp_slug() -> str:
    return time.strftime("%Y%m%d_%H%M%S")
