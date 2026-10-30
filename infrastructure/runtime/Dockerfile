FROM node:24.21.0-bookworm-slim@sha256:51b1100cc2a83d370c6a60952e3f2989c8a43159d0e38586e090f3b3326efefd AS node

FROM ubuntu:24.04@sha256:24737cef17f4e22b1977df7d9a05e77c4e28b7da5b8911f8d8ccda136bf3573c
RUN apt-get update && DEBIAN_FRONTEND=noninteractive apt-get install -y --no-install-recommends \
    bash git python3 python-is-python3 python3-pip python3-venv \
    ca-certificates curl wget unzip xz-utils tini libstdc++6 \
    fonts-dejavu-core fonts-liberation2 fonts-wqy-zenhei \
    && rm -rf /var/lib/apt/lists/*
COPY --from=node /usr/local/bin/node /usr/local/bin/node
COPY --from=node /usr/local/lib/node_modules/npm /usr/local/lib/node_modules/npm
RUN ln -s ../lib/node_modules/npm/bin/npm-cli.js /usr/local/bin/npm \
    && ln -s ../lib/node_modules/npm/bin/npx-cli.js /usr/local/bin/npx
WORKDIR /opt/harness-runtime
COPY package.json package-lock.json ./
RUN npm ci --omit=dev --ignore-scripts --no-audit --no-fund
COPY contract.mjs private-file.mjs bootstrap.mjs start.mjs external-model.mjs messages-wire.mjs ./
ENV NODE_ENV=production \
    HOME=/data/dsh \
    PATH=/data/dsh/.local/bin:/opt/harness-runtime/node_modules/.bin:${PATH}
USER 1000:1000
ENTRYPOINT ["/usr/bin/tini", "--", "node", "/opt/harness-runtime/start.mjs"]
