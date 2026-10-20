FROM node:24.21.0-bookworm-slim@sha256:51b1100cc2a83d370c6a60952e3f2989c8a43159d0e38586e090f3b3326efefd
RUN apt-get update && apt-get install -y --no-install-recommends bash git python3 ca-certificates tini fonts-dejavu-core fonts-liberation2 fonts-wqy-zenhei && rm -rf /var/lib/apt/lists/*
WORKDIR /opt/harness-runtime
COPY package.json package-lock.json ./
RUN npm ci --omit=dev --ignore-scripts --no-audit --no-fund
COPY contract.mjs private-file.mjs bootstrap.mjs start.mjs external-model.mjs messages-wire.mjs ./
ENV NODE_ENV=production
USER 1000:1000
ENTRYPOINT ["/usr/bin/tini", "--", "node", "/opt/harness-runtime/start.mjs"]
