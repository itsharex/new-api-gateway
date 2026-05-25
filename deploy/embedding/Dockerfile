FROM python:3.11-slim AS builder

WORKDIR /build
COPY requirements.txt .
RUN pip install --no-cache-dir --prefix=/install \
    -i https://mirrors.aliyun.com/pypi/simple/ \
    --trusted-host mirrors.aliyun.com \
    --extra-index-url https://download.pytorch.org/whl/cpu \
    -r requirements.txt

FROM python:3.11-slim

COPY --from=builder /install /usr/local

WORKDIR /app
COPY server.py healthcheck.py .

HEALTHCHECK --interval=5s --timeout=3s --retries=30 --start-period=30s \
    CMD ["python", "/app/healthcheck.py"]

EXPOSE 8000
CMD ["uvicorn", "server:app", "--host", "0.0.0.0", "--port", "8000"]
