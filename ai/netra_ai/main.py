"""FastAPI app: POST /detect (multipart image + early JSON) -> final classification."""

import asyncio
import base64
import io
import json
import os
import time
from typing import Annotated

import httpx
from fastapi import FastAPI, File, Form, HTTPException, UploadFile
from fastapi.responses import PlainTextResponse
from PIL import Image
from prometheus_client import CONTENT_TYPE_LATEST, Counter, Histogram, generate_latest

from netra_ai.classify import build_prompt, normalise

OLLAMA_URL = os.environ.get("OLLAMA_URL", "http://ollama:11434").rstrip("/")
MODEL = os.environ.get("NETRA_AI_MODEL", "qwen2.5vl:7b")
CATEGORIES = [
    c.strip()
    for c in os.environ.get("NETRA_AI_CATEGORIES", "trash,spill,floor_damage,fallen_person").split(",")
    if c.strip()
]
MAX_SIDE = int(os.environ.get("NETRA_AI_MAX_SIDE", "1024"))
TIMEOUT = float(os.environ.get("NETRA_AI_TIMEOUT", "90"))
KEEP_ALIVE = os.environ.get("NETRA_AI_KEEP_ALIVE", "1h")
LANGUAGE = os.environ.get("NETRA_AI_LANGUAGE", "English")

REQUESTS = Counter("netra_ai_requests_total", "Detections served.", ["category", "result"])
LATENCY = Histogram("netra_ai_latency_seconds", "Model latency.", buckets=(0.5, 1, 2, 4, 8, 16, 32, 64))

app = FastAPI(title="NETRA AI", version="0.1.0")
_gate = asyncio.Semaphore(int(os.environ.get("NETRA_AI_CONCURRENCY", "1")))
_client = httpx.AsyncClient(timeout=TIMEOUT)


def prepare(image: bytes) -> str:
    """Downscale to MAX_SIDE and return base64 JPEG."""
    img = Image.open(io.BytesIO(image)).convert("RGB")
    img.thumbnail((MAX_SIDE, MAX_SIDE))
    out = io.BytesIO()
    img.save(out, format="JPEG", quality=88)
    return base64.b64encode(out.getvalue()).decode()


@app.get("/healthz")
async def healthz() -> dict:
    """Report whether Ollama answers and the model is present."""
    try:
        tags = (await _client.get(f"{OLLAMA_URL}/api/tags", timeout=5)).json()
        present = any(m.get("name", "").startswith(MODEL) for m in tags.get("models", []))
        return {"status": "ok" if present else "model_missing", "model": MODEL}
    except httpx.HTTPError as error:
        return {"status": "ollama_unreachable", "error": str(error)}


@app.get("/metrics")
async def metrics() -> PlainTextResponse:
    """Prometheus metrics."""
    return PlainTextResponse(generate_latest().decode(), media_type=CONTENT_TYPE_LATEST)


@app.post("/detect")
async def detect(image: Annotated[UploadFile, File()], early: Annotated[str, Form()] = "{}") -> dict:
    """Classify an anomaly photo, using the robot's early result as context."""
    try:
        early_data = json.loads(early)
    except ValueError:
        early_data = {}
    early_category = str(early_data.get("category") or "unknown")
    early_confidence = float(early_data.get("confidence") or 0.0)
    try:
        encoded = prepare(await image.read())
    except Exception as error:  # noqa: BLE001 - any decode failure is a bad request
        raise HTTPException(status_code=400, detail=f"unreadable image: {error}") from error
    body = {
        "model": MODEL,
        "prompt": build_prompt(CATEGORIES, early_category, early_confidence, LANGUAGE),
        "images": [encoded],
        "format": "json",
        "stream": False,
        "keep_alive": KEEP_ALIVE,
        "options": {"temperature": 0.0, "num_predict": 160},
    }
    async with _gate:
        start = time.perf_counter()
        try:
            reply = await _client.post(f"{OLLAMA_URL}/api/generate", json=body)
            reply.raise_for_status()
        except httpx.HTTPError as error:
            REQUESTS.labels(early_category, "error").inc()
            raise HTTPException(status_code=502, detail=f"model unavailable: {error}") from error
        LATENCY.observe(time.perf_counter() - start)
    result = normalise(reply.json().get("response", ""), CATEGORIES)
    REQUESTS.labels(result["category"], "agree" if result["category"] == early_category else "changed").inc()
    return result
