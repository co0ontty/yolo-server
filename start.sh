#!/bin/sh

# 检查并生成 SSL 证书（如果不存在）
if [ ! -f "/app/certs/server.crt" ] || [ ! -f "/app/certs/server.key" ]; then
    echo "SSL 证书不存在，正在生成自签名证书..."
    /app/gen-cert.sh
fi

# 配置 Web 认证
if [ "$WEB_AUTH_ENABLED" = "true" ] && [ -n "$WEB_AUTH_PASSWORD" ]; then
    echo "启用 Web 认证..."
    # 生成 htpasswd 文件，用户名为 admin
    htpasswd -bc /etc/nginx/.htpasswd admin "$WEB_AUTH_PASSWORD"
    # 如果设置了自定义用户名，使用自定义用户名
    if [ -n "$WEB_AUTH_USER" ]; then
        htpasswd -bc /etc/nginx/.htpasswd "$WEB_AUTH_USER" "$WEB_AUTH_PASSWORD"
    fi
    echo "Web 认证已启用 (用户名：${WEB_AUTH_USER:-admin})"
else
    echo "Web 认证未启用 (设置 WEB_AUTH_ENABLED=true 和 WEB_AUTH_PASSWORD 来启用)"
    # 创建空的 htpasswd 文件避免 nginx 启动失败
    touch /etc/nginx/.htpasswd
fi

# 导出环境变量给 nginx 使用
export WEB_AUTH_ENABLED WEB_AUTH_USER WEB_AUTH_PASSWORD

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
