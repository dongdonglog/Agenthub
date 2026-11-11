# 构建
FROM golang:1.23-alpine AS build
WORKDIR /src
ARG VERSION=dev
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath \
    -ldflags "-s -w -X github.com/moying/agenthub/internal/version.Version=${VERSION}" \
    -o /out/agentd ./cmd/agentd
# 预创建数据目录：命名卷首次挂载会继承镜像内目录的属主，否则非 root 无法写入
RUN mkdir -p /out/data

# 运行：非 root、只读根文件系统
FROM gcr.io/distroless/static-debian12:nonroot
WORKDIR /app
COPY --from=build /out/agentd /app/agentd
COPY migrations /app/migrations
COPY --chown=nonroot:nonroot skills /app/skills
COPY --from=build --chown=nonroot:nonroot /out/data /data
ENV AGENTHUB_HTTP_ADDR=0.0.0.0:8787
ENV AGENTHUB_DATA_DIR=/data
EXPOSE 8787 8788
USER nonroot:nonroot
HEALTHCHECK --interval=30s --timeout=5s --start-period=15s --retries=3 CMD ["/app/agentd", "-healthcheck"]
ENTRYPOINT ["/app/agentd"]
