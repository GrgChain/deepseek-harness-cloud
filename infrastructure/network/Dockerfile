FROM node:24.21.0-bookworm-slim@sha256:51b1100cc2a83d370c6a60952e3f2989c8a43159d0e38586e090f3b3326efefd
RUN apt-get update && apt-get install -y --no-install-recommends python3 iptables && rm -rf /var/lib/apt/lists/*
COPY helper.py /opt/harness-network/helper.py
ENTRYPOINT ["python3", "/opt/harness-network/helper.py"]
