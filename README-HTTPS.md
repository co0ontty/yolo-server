# HTTPS 配置说明

## 概述

本项目现已支持 HTTPS 访问，使用自签名证书（开发环境）或正式证书（生产环境）。

## 快速开始

### 1. 生成自签名证书（仅首次）

```bash
cd server
./gen-cert.sh
```

这会生成以下文件：
- `certs/server.crt` - 证书文件
- `certs/server.key` - 私钥文件

**注意：** 自签名证书在浏览器中会显示"不安全"警告，点击"继续访问"即可。

### 2. 启动服务

```bash
docker compose up -d
```

### 3. 访问地址

- **HTTPS**: `https://localhost:8443` 或 `https://你的 IP:8443`
- **HTTP**: `http://localhost:8118` (自动重定向到 HTTPS)

## CLI 配置

CLI 已内置支持自签名证书，无需额外配置。

```bash
# 设置环境变量
export VIBE_SERVER=https://192.168.0.7:8443
# 或
export YOLO_SERVER_WS=wss://192.168.0.7:8443/ws/cli
```

## 替换为正式证书

生产环境建议替换为正式证书：

1. 将你的证书文件复制到 `certs/` 目录：
   ```bash
   cp /path/to/your/server.crt ./server/certs/
   cp /path/to/your/server.key ./server/certs/
   ```

2. 重新构建并启动 Docker：
   ```bash
   docker compose down
   docker compose up -d
   ```

## 端口说明

| 端口 | 协议 | 说明 |
|------|------|------|
| 8118 | HTTP | 自动重定向到 HTTPS (443) |
| 8443 | HTTPS | 主要访问端口 |

## 证书目录挂载

`docker-compose.yml` 中已配置证书卷挂载：
```yaml
volumes:
  - ./certs:/app/certs  # SSL 证书目录
```

确保 `certs/` 目录包含以下文件：
- `server.crt`
- `server.key`
