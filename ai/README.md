# netra-ai

Final anomaly detection for NETRA. `POST /detect` takes the robot photo (`image`) and its early
result (`early`, JSON) and answers `{category, confidence, description}` using a vision-language
model on Ollama (default `qwen2.5vl:7b`, GPU recommended). The robot's guess is part of the prompt,
answers are constrained to JSON and off-list wording is mapped to the configured categories.

| Env | Default | |
|---|---|---|
| `OLLAMA_URL` | `http://ollama:11434` | |
| `NETRA_AI_MODEL` | `qwen2.5vl:7b` | any Ollama vision model (`llava:13b`, `minicpm-v`, ...) |
| `NETRA_AI_CATEGORIES` | `trash,spill,floor_damage,fallen_person` | must match the NETRA rule codes |
| `NETRA_AI_CONCURRENCY` | `1` | simultaneous model calls |
| `NETRA_AI_MAX_SIDE` | `1024` | image downscale |

`GET /healthz` reports whether the model is present, `GET /metrics` exposes request counts and
latency for Prometheus.
