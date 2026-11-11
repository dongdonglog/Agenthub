# 构建
FROM golang:1.23-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags "-s -w" -o /out/agentd ./cmd/agentd

# 运行：非 root、只读根文件系统
FROM gcr.io/distroless/static-debian12:nonroot
WORKDIR /app
COPY --from=build /out/agentd /app/agentd
COPY migrations /app/migrations
COPY skills /app/skills
ENV AGENTHUB_HTTP_ADDR=0.0.0.0:8787
EXPOSE 8787 8788
USER nonroot:nonroot
ENTRYPOINT ["/app/agentd"]
