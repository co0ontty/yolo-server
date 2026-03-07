#!/bin/sh

# 启动 Go server 后台
/app/vibe-server &
VIBE_PID=$!

# 启动 nginx 前台
nginx -g "daemon off;" &
NGINX_PID=$!

# 等待任一进程退出
wait -n

# 杀死另一个进程
kill $VIBE_PID $NGINX_PID 2>/dev/null
