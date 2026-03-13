# 阶段 1: 构建 Go server（需要 Go 1.22+）
FROM golang:1.22-alpine AS builder

WORKDIR /build/server

COPY go.mod go.sum ./
RUN go mod download

COPY cmd/ ./cmd/
COPY internal/ ./internal/
RUN CGO_ENABLED=0 GOOS=linux go build -o vibe-server ./cmd

# 阶段 1b: 构建 CLI worker
WORKDIR /build/cli

COPY cli-go.mod ./go.mod
COPY cli-go.sum ./go.sum
RUN go mod download

COPY cli-cmd/ ./cmd/
COPY cli-internal/ ./internal/
RUN CGO_ENABLED=0 GOOS=linux go build -o vibe-cli ./cmd

# 阶段 2: 最终镜像
FROM alpine:latest

WORKDIR /app

RUN apk --no-cache add ca-certificates nginx apache2-utils

# 创建目录结构
RUN mkdir -p /app/data /app/dist/web /app/dist/cli /app/certs

# 从 builder 复制 Go server 和 CLI worker
COPY --from=builder /build/server/vibe-server .
COPY --from=builder /build/cli/vibe-cli .

# 复制已构建的静态文件
COPY dist ./dist
COPY nginx.conf.docker /etc/nginx/nginx.conf
COPY gen-cert.sh /app/gen-cert.sh
COPY start.sh /app/start.sh

# 修复文件权限
RUN chmod -R 755 /app/dist
RUN chmod +x /app/vibe-server
RUN chmod +x /app/vibe-cli
RUN chmod +x /app/gen-cert.sh
RUN chmod +x /app/start.sh

EXPOSE 80 443

CMD ["/app/start.sh"]
