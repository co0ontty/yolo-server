#!/bin/bash

# 生成自签名证书脚本
# 用于本地开发测试，生产环境请替换为正式证书

# 允许通过环境变量覆盖，默认为 ./certs（容器内可通过挂载或环境变量设置为 /app/certs）
CERT_DIR="${CERT_DIR:-./certs}"

echo "生成自签名证书..."

# 创建证书目录
mkdir -p "$CERT_DIR"

# 生成私钥和自签名证书
openssl req -x509 -newkey rsa:4096 \
    -keyout "$CERT_DIR/server.key" \
    -out "$CERT_DIR/server.crt" \
    -days 365 \
    -nodes \
    -subj "/C=CN/ST=Local/L=Local/O=Development/CN=localhost" \
    -addext "subjectAltName=DNS:localhost,IP:127.0.0.1,IP:192.168.1.1,IP:192.168.0.1,IP:10.0.0.1"

echo "证书已生成到 $CERT_DIR 目录"
echo "  - server.crt (证书)"
echo "  - server.key (私钥)"
echo ""
echo "开发环境使用：证书有效期 365 天"
echo "生产环境请替换为正式证书"
