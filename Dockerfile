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
RUN CGO_ENABLED=0 go build -trimpath -ldflags "-s -w" -o /out/agenthub-ops-mcp ./cmd/agenthub-ops-mcp
RUN mkdir -p /out/data

# 运行：Node 22 + pi 1.4.0 + 常用命令行工具链
FROM node:22-bookworm-slim
ARG PI_VERSION=1.4.0
ARG GLAB_VERSION=1.128.0
RUN apt-get update \
    && apt-get install -y --no-install-recommends \
        ca-certificates bash procps python3 python3-pip \
        ripgrep fd-find git git-lfs gh \
        curl wget jq yq less tree file unzip zip sqlite3 xxd \
        openssh-client vim-tiny \
    && ln -sf /usr/bin/fdfind /usr/local/bin/fd \
    && if ! command -v glab >/dev/null 2>&1; then \
         arch="$(dpkg --print-architecture)"; \
         case "${arch}" in \
           amd64) glab_arch=amd64; glab_sha=bc024819e7598b19429a199cd87f640d98b2b67416d8ad796f19e4260b57a7c7 ;; \
           arm64) glab_arch=arm64; glab_sha=60d0512085a8c1be7c5ef51aa7e4ee28213444cea0376b3260707d90489199ea ;; \
           *) echo "unsupported architecture: ${arch}" >&2; exit 1 ;; \
         esac; \
         curl -fsSL --retry 3 "https://gitlab.com/gitlab-org/cli/-/releases/v${GLAB_VERSION}/downloads/glab_${GLAB_VERSION}_linux_${glab_arch}.tar.gz" -o /tmp/glab.tar.gz; \
         echo "${glab_sha}  /tmp/glab.tar.gz" | sha256sum -c -; \
         mkdir -p /tmp/glab-extract; \
         tar -xzf /tmp/glab.tar.gz -C /tmp/glab-extract bin/glab; \
         install -m 0755 /tmp/glab-extract/bin/glab /usr/local/bin/glab; \
         rm -rf /tmp/glab-extract /tmp/glab.tar.gz; \
       fi \
    && rm -rf /var/lib/apt/lists/* \
    && npm install --global "@earendil-works/pi-coding-agent@${PI_VERSION}" \
    && npm cache clean --force
WORKDIR /app
COPY --from=build /out/agentd /app/agentd
COPY --from=build /out/agenthub-ops-mcp /app/agenthub-ops-mcp
COPY migrations /app/migrations
COPY pi-package /app/pi-package
COPY examples/mcp /app/examples/mcp
COPY --from=build --chown=node:node /out/data /data
RUN mkdir -p /data/home /data/glab /data/gh && chown -R node:node /data
ENV HOME=/data/home
ENV XDG_CONFIG_HOME=/data/home/.config
ENV GLAB_CONFIG_DIR=/data/glab
ENV GH_CONFIG_DIR=/data/gh
ENV GIT_CONFIG_GLOBAL=/data/home/.gitconfig
ENV AGENTHUB_HTTP_ADDR=0.0.0.0:8787
ENV AGENTHUB_ADMIN_ADDR=0.0.0.0:8788
ENV AGENTHUB_DATA_DIR=/data
ENV PI_BIN=/usr/local/bin/pi
USER node
EXPOSE 8787 8788
HEALTHCHECK --interval=30s --timeout=5s --start-period=20s --retries=3 CMD ["/app/agentd", "-healthcheck"]
ENTRYPOINT ["/app/agentd"]
