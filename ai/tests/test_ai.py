"""Tests for the final detection service."""

import io
import json

import httpx
from fastapi.testclient import TestClient
from PIL import Image

from netra_ai import main
from netra_ai.classify import build_prompt, normalise

CATS = ["trash", "spill", "floor_damage", "fallen_person"]


def jpeg(size=(2000, 1500)) -> bytes:
    """Return a test JPEG."""
    out = io.BytesIO()
    Image.new("RGB", size, (90, 90, 90)).save(out, format="JPEG")
    return out.getvalue()


def test_normalise_variants():
    """JSON, off-list words and percentages are normalised."""
    assert (
        normalise('{"category": "trash", "confidence": 0.9, "description": "botol"}', CATS)["category"]
        == "trash"
    )
    assert normalise('{"category": "wet floor", "confidence": 70}', CATS) == {
        "category": "spill",
        "confidence": 0.7,
        "description": '{"category": "wet floor", "confidence": 70}',
    }
    assert normalise("nothing here", CATS)["category"] == "none"


def test_prompt_mentions_early_result():
    """The reviewer sees what the robot thought."""
    assert '"floor damage" (82% sure)' in build_prompt(CATS, "floor_damage", 0.82)


def test_detect_endpoint(monkeypatch):
    """The endpoint downsizes the image and returns the model verdict."""
    seen = {}

    def handler(request: httpx.Request) -> httpx.Response:
        body = json.loads(request.content)
        seen["body"] = body
        answer = {
            "category": "trash",
            "confidence": 0.93,
            "description": "Botol plastik di lantai dekat eskalator",
        }
        return httpx.Response(200, json={"response": json.dumps(answer)})

    monkeypatch.setattr(main, "_client", httpx.AsyncClient(transport=httpx.MockTransport(handler)))
    client = TestClient(main.app)
    reply = client.post(
        "/detect",
        files={"image": ("a.jpg", jpeg(), "image/jpeg")},
        data={"early": json.dumps({"category": "trash", "confidence": 0.6})},
    )
    assert reply.status_code == 200
    assert reply.json()["category"] == "trash" and reply.json()["confidence"] == 0.93
    sent = Image.open(io.BytesIO(__import__("base64").b64decode(seen["body"]["images"][0])))
    assert max(sent.size) <= main.MAX_SIDE and seen["body"]["format"] == "json"
    assert "netra_ai_requests_total" in client.get("/metrics").text


def test_bad_image_is_400():
    """Garbage uploads are rejected."""
    reply = TestClient(main.app).post("/detect", files={"image": ("a.jpg", b"nope", "image/jpeg")})
    assert reply.status_code == 400
