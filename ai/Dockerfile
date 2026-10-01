FROM python:3.12-slim
WORKDIR /app
ENV PYTHONUNBUFFERED=1 PIP_NO_CACHE_DIR=1
COPY pyproject.toml ./
COPY netra_ai ./netra_ai
RUN pip install . && useradd -r app
USER app
EXPOSE 8090
HEALTHCHECK --interval=30s --timeout=5s CMD python -c "import urllib.request;urllib.request.urlopen('http://127.0.0.1:8090/healthz')"
CMD ["uvicorn", "netra_ai.main:app", "--host", "0.0.0.0", "--port", "8090", "--workers", "1"]
