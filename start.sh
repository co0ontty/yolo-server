#!/bin/sh

# 检查并生成 SSL 证书（如果不存在）
if [ ! -f "/app/certs/server.crt" ] || [ ! -f "/app/certs/server.key" ]; then
    echo "SSL 证书不存在，正在生成自签名证书..."
    /app/gen-cert.sh
fi

# 配置 Web 认证并生成 nginx 配置
if [ "$WEB_AUTH_ENABLED" = "true" ] && [ -n "$WEB_AUTH_PASSWORD" ]; then
    echo "启用 Web 认证..."
    # 生成 htpasswd 文件
    htpasswd -bc /etc/nginx/.htpasswd "${WEB_AUTH_USER:-admin}" "$WEB_AUTH_PASSWORD"
    echo "Web 认证已启用 (用户名：${WEB_AUTH_USER:-admin})"
    # 替换 nginx 配置中的占位符为认证配置
    sed -i 's/AUTH_BASIC_PLACEHOLDER/auth_basic "Vibe Coding - 请输入密码";\n            auth_basic_user_file \/etc\/nginx\/.htpasswd;/g' /etc/nginx/nginx.conf
else
    echo "Web 认证未启用 (设置 WEB_AUTH_ENABLED=true 和 WEB_AUTH_PASSWORD 来启用)"
    # 替换占位符为 auth_basic off
    sed -i 's/AUTH_BASIC_PLACEHOLDER/auth_basic off;/g' /etc/nginx/nginx.conf
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
