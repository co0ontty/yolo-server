# 部署指南

## 架构

```
用户浏览器 <---> nginx (80端口) <---> Go server (3100端口)
                                      ^
                                      |
                              用户本地 CLI (可选)
```

- **nginx**: 服务前端静态文件 + 反向代理 API/WebSocket
- **Go server**: 只负责 API 和 WebSocket 转发
- **CLI**: 用户本地运行，连接服务器

## 部署步骤

### 1. 构建前端
```bash
cd frontend
npm install
npm run build
```

### 2. 启动 Go server
```bash
cd server
go run cmd/main.go  # 默认监听 3100 端口
# 或通过环境变量指定端口
PORT=8080 go run cmd/main.go
```

### 3. 配置 nginx

复制 `server/nginx.conf` 到 nginx 配置目录，修改 `root` 路径：

```nginx
root /path/to/vibe-coding/frontend/dist;  # 改为实际路径
```

启动 nginx：
```bash
nginx -c /path/to/vibe-coding/server/nginx.conf
# 或
sudo cp server/nginx.conf /etc/nginx/sites-available/vibe-coding
sudo ln -s /etc/nginx/sites-available/vibe-coding /etc/nginx/sites-enabled/
nginx -t && nginx -s reload
```

### 4. 启动 CLI worker (可选)

用户需要自己运行 CLI 来执行任务：
```bash
cd cli
go build -o vibe-cli ./cmd
VIBE_SERVER=http://localhost:3100 ./vibe-cli
```

可以运行多个 CLI worker 并行处理任务。

## 访问

- 前端: http://localhost/
- WebSocket: ws://localhost/ws
- CLI: ws://localhost/ws/cli
