#!/bin/sh

# 检查并生成 SSL 证书（如果不存在）
if [ ! -f "/app/certs/server.crt" ] || [ ! -f "/app/certs/server.key" ]; then
    echo "SSL 证书不存在，正在生成自签名证书..."
    /app/gen-cert.sh
fi

# 启动 Go server 后台
/app/vibe-server &
VIBE_PID=$!

# 如果安装了 claude CLI，启动内置 CLI worker
if command -v claude >/dev/null 2>&1 && [ -x /app/vibe-cli ]; then
  echo "检测到 claude CLI，启动内置 CLI worker..."
  /app/vibe-cli &
  CLI_PID=$!
else
  echo "未检测到 claude CLI，跳过内置 CLI worker。请在外部运行 CLI worker。"
fi

# 启动 nginx 前台
nginx -g "daemon off;" &
NGINX_PID=$!

# 等待任一进程退出
wait -n

# 杀死其他进程
kill $VIBE_PID $NGINX_PID ${CLI_PID:-} 2>/dev/null
