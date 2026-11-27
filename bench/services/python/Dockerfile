FROM python:3.15-slim-trixie
ENV PYTHONUNBUFFERED=1
WORKDIR /app
COPY requirements.txt ./
# Wheels only, and compile bytecode at build time so a cold start does not.
RUN pip install --no-cache-dir --only-binary=:all: -r requirements.txt && python -m compileall -q /usr/local/lib/python3.15
COPY main.py ./
RUN python -m compileall -q main.py
USER 65532:65532
# One worker and several threads is Google's recommendation for Cloud Run: the
# instance is the unit of scaling, and a second worker only doubles start-up.
CMD ["gunicorn", "--bind", ":8080", "--workers", "1", "--threads", "8", "--timeout", "0", "main:app"]
