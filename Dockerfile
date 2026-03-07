# 阶段 1: 构建 Go server（需要 Go 1.22+）
FROM golang:1.22-alpine AS builder

WORKDIR /build

COPY go.mod go.sum ./
RUN go mod download

COPY cmd/ ./cmd/
COPY internal/ ./internal/
RUN CGO_ENABLED=0 GOOS=linux go build -o vibe-server ./cmd

# 阶段 2: 最终镜像
FROM alpine:latest

WORKDIR /app

RUN apk --no-cache add ca-certificates nginx nodejs npm

# 创建目录结构
RUN mkdir -p /app/data /app/internal /app/dist/web /app/dist/cli

# 从 builder 复制 Go server
COPY --from=builder /build/vibe-server .

# 复制已构建的静态文件和运行时依赖
COPY dist ./dist
COPY internal ./internal
COPY node_modules ./node_modules
COPY package.json ./
COPY nginx.conf.docker /etc/nginx/nginx.conf
COPY start.sh /app/start.sh

# 修复文件权限
RUN chmod -R 755 /app/dist
RUN chmod +x /app/vibe-server
RUN chmod +x /app/start.sh

EXPOSE 80

CMD ["/app/start.sh"]
