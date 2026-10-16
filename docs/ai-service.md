# AI service

`netra-ai` ([`ai/`](../ai/README.md)) is the final anomaly detection service. For each queued
anomaly, `netra-api` posts the robot's photo and early result to it
([pipeline](architecture.md#anomaly-pipeline)); `netra-ai` asks a vision-language model on Ollama
to review the photo and answers with a category from a fixed list (or `none`), a confidence and a
one-sentence description.

| File | Contents |
|---|---|
| [`netra_ai/main.py`](../ai/netra_ai/main.py) | FastAPI app, configuration, image preparation, Ollama call, metrics |
| [`netra_ai/classify.py`](../ai/netra_ai/classify.py) | Prompt and normalisation of the model's answer |
| [`tests/test_ai.py`](../ai/tests/test_ai.py) | Tests |
| [`pyproject.toml`](../ai/pyproject.toml) | Package `netra-ai` 0.1.0, dependencies, ruff and pytest settings |
| [`Dockerfile`](../ai/Dockerfile) | Image |

## Endpoints

| Method | Path | What it does |
|---|---|---|
| POST | `/detect` | Multipart: `image` (file, required) and `early` (form field with JSON, default `{}`) → `{category, confidence, description}` |
| GET | `/healthz` | Asks Ollama for its models: `{"status": "ok", "model"}`, `{"status": "model_missing", "model"}` or `{"status": "ollama_unreachable", "error"}`, each with HTTP 200 |
| GET | `/metrics` | Prometheus text format |

FastAPI's own `/docs` and `/openapi.json` are left at their defaults.

### POST /detect

```bash
curl -s http://localhost:8090/detect \
  -F image=@photo.jpg \
  -F 'early={"category": "trash", "confidence": 0.74, "description": "a bottle"}'
```

```json
{"category": "trash", "confidence": 0.91, "description": "A plastic bottle on the floor next to the escalator."}
```

| Status | When |
|---|---|
| 200 | Answer as above; `category` is one of `NETRA_AI_CATEGORIES` or `none` |
| 400 | `{"detail": "unreadable image: ..."}`: Pillow cannot open the upload |
| 422 | FastAPI validation error, for example without an `image` field |
| 502 | `{"detail": "model unavailable: ..."}`: Ollama unreachable, timed out or answered with an error status |

`netra-api` sends `early` as `{"category", "confidence", "description"}`; only `category` (default
`unknown`) and `confidence` (default 0) are used. An `early` value that is not valid JSON counts as
`{}`.

## Processing

1. **Image.** Opened with Pillow, converted to RGB, shrunk so the longest side is at most
   `NETRA_AI_MAX_SIDE` pixels (aspect ratio kept, never enlarged), re-encoded as JPEG quality 88 and
   base64-encoded.
2. **Prompt.** Built by `build_prompt` from the template in `classify.py`:

   ```text
   You are the final reviewer of a mall patrol robot. The robot flagged this photo as "{early}"
   ({confidence} sure). Look carefully at the floor and the people. Decide which single category
   is really present: {options}. Use "none" when the robot was wrong. Answer only JSON:
   {"category": "<one of {options}>", "confidence": <0..1>, "description": "<one sentence in
   {language} describing what and where>"}.
   ```

   `{early}` is the early category with underscores as spaces, `{confidence}` the early confidence
   as a whole percentage, `{options}` the configured categories plus `none`, and `{language}` is
   `NETRA_AI_LANGUAGE`.
3. **Model call.** `POST {OLLAMA_URL}/api/generate` with `model`, `prompt`, `images` (the base64
   JPEG), `format: "json"`, `stream: false`, `keep_alive` (`NETRA_AI_KEEP_ALIVE`) and
   `options: {"temperature": 0.0, "num_predict": 160}`. An `asyncio.Semaphore` of
   `NETRA_AI_CONCURRENCY` limits calls in flight; the HTTP client times out after
   `NETRA_AI_TIMEOUT` seconds.
4. **Normalisation** (`normalise`) of the model's `response` text:
   - The text from the first `{` to the last `}` is parsed as JSON; if there is none or it does not
     parse, no field is taken from it.
   - The category is lower-cased with spaces turned into underscores. A configured category or
     `none` is kept; anything else is matched by keywords against the category and description
     text, trying the configured categories in order, and becomes `none` when nothing matches.
   - The description is the model's `description` or, without one, the raw answer, trimmed to 400
     characters.
   - The confidence defaults to 0.6 when missing or not a number, is divided by 100 when above 1
     (percentages), clamped to 0 to 1 and rounded to three decimals.

| Category | Keywords (substring match, lower case) |
|---|---|
| `trash` | trash, garbage, litter, rubbish, bottle, cup, wrapper, plastic bag, tissue |
| `spill` | spill, liquid, puddle, wet floor, water on, stain |
| `floor_damage` | crack, broken tile, damaged floor, hole, loose tile, chipped |
| `fallen_person` | fallen, lying on the floor, person lying, collapsed, unconscious |
| any other configured category | its own name with underscores as spaces |

In `netra-api`, `none` or a confidence below the rule's `min_confidence` dismisses the anomaly, and a
category without a rule uses the `unknown` rule. If `netra-ai` fails (status 300 or above, or no
answer within the 120 s client timeout of `netra-api`), the robot's own result is used.

## Configuration

| Variable | Default | Effect |
|---|---|---|
| `OLLAMA_URL` | `http://ollama:11434` | Ollama base URL |
| `NETRA_AI_MODEL` | `qwen2.5vl:7b` | Ollama vision model, for example `llava:13b` or `minicpm-v` |
| `NETRA_AI_CATEGORIES` | `trash,spill,floor_damage,fallen_person` | Categories offered to the model; must match the NETRA rule codes |
| `NETRA_AI_LANGUAGE` | `English` | Language of the description |
| `NETRA_AI_MAX_SIDE` | `1024` | Longest image side sent to the model, pixels |
| `NETRA_AI_TIMEOUT` | `90` | Timeout towards Ollama, seconds |
| `NETRA_AI_KEEP_ALIVE` | `1h` | How long Ollama keeps the model loaded |
| `NETRA_AI_CONCURRENCY` | `1` | Model calls in flight |

Which of these the deployment sets, and how values are parsed, is in
[configuration](configuration.md#netra-ai).

## Metrics

| Metric | Type | Labels | Meaning |
|---|---|---|---|
| `netra_ai_requests_total` | counter | `category`, `result` | Detections: `result` is `agree` (same category as the robot), `changed`, or `error` (Ollama failed; `category` is then the robot's) |
| `netra_ai_latency_seconds` | histogram | | Duration of successful Ollama calls; buckets 0.5, 1, 2, 4, 8, 16, 32 and 64 s |

`/metrics` renders the default `prometheus_client` registry, so the client library's process and
Python metrics are included.

## Running locally

```bash
ollama pull qwen2.5vl:7b
cd ai && pip install -e ".[dev]"
OLLAMA_URL=http://localhost:11434 uvicorn netra_ai.main:app --port 8090
```

Point `netra-api` at it with `NETRA_FINAL_DETECTOR_URL=http://localhost:8090/detect`. A GPU is
recommended for the default model ([`ai/README.md`](../ai/README.md)).

## Tests and lint

```bash
cd ai
pip install -e ".[dev]"
python -m pytest -q
ruff check . && ruff format --check .
```

The tests need neither Ollama nor a GPU: `test_detect_endpoint` replaces the HTTP client with an
`httpx.MockTransport`.

| Test | Checks |
|---|---|
| `test_normalise_variants` | JSON answers, off-list wording mapped by keywords, percentages, answers without JSON |
| `test_prompt_mentions_early_result` | The prompt contains the robot's category and confidence |
| `test_detect_endpoint` | The photo is downscaled to `NETRA_AI_MAX_SIDE`, the request asks for JSON, the verdict is returned, `/metrics` counts it |
| `test_bad_image_is_400` | An upload that is not an image gets 400 |

## Docker image

[`ai/Dockerfile`](../ai/Dockerfile) builds on `python:3.12-slim`, installs the package with `pip`,
and runs as the system user `app`:

- port 8090, command `uvicorn netra_ai.main:app --host 0.0.0.0 --port 8090 --workers 1` (the
  concurrency limit applies per process);
- `HEALTHCHECK` every 30 s (5 s timeout) fetching `http://127.0.0.1:8090/healthz`. As `/healthz`
  answers 200 also when Ollama is down or the model is missing, the container then still reports
  healthy; read the `status` field to tell.

CI builds the image for `linux/amd64` and `linux/arm64` and pushes it as
`ghcr.io/<repository owner>/netra-ai` ([development](development.md#continuous-integration)).
