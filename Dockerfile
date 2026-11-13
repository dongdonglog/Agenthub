# 前端构建
FROM node:22-bookworm-slim AS web
WORKDIR /web
COPY web/package.json web/package-lock.json ./
RUN npm ci
COPY web ./
RUN npm run build

# 构建
FROM golang:1.23-bookworm AS build
WORKDIR /src
ARG VERSION=dev
COPY go.mod go.sum ./
RUN go mod download
COPY . .
COPY --from=web /internal/admin/static /src/internal/admin/static
RUN CGO_ENABLED=0 go build -trimpath \
    -ldflags "-s -w -X github.com/moying/agenthub/internal/version.Version=${VERSION}" \
    -o /out/agentd ./cmd/agentd
RUN mkdir -p /out/data

# 运行：Node 22 + pi 1.4.0 + 基础命令行工具
FROM node:22-bookworm-slim
ARG PI_VERSION=1.4.0
RUN apt-get update \
    && apt-get install -y --no-install-recommends ca-certificates ripgrep bash procps python3 \
    && rm -rf /var/lib/apt/lists/* \
    && npm install --global "@earendil-works/pi-coding-agent@${PI_VERSION}" \
    && npm cache clean --force
WORKDIR /app
COPY --from=build /out/agentd /app/agentd
COPY migrations /app/migrations
COPY examples/mcp /app/examples/mcp
COPY --from=build --chown=node:node /out/data /data
ENV AGENTHUB_HTTP_ADDR=0.0.0.0:8787
ENV AGENTHUB_ADMIN_ADDR=0.0.0.0:8788
ENV AGENTHUB_DATA_DIR=/data
ENV PI_BIN=/usr/local/bin/pi
USER node
EXPOSE 8787 8788
HEALTHCHECK --interval=30s --timeout=5s --start-period=20s --retries=3 CMD ["/app/agentd", "-healthcheck"]
ENTRYPOINT ["/app/agentd"]
