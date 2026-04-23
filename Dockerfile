FROM alpine:latest

# 安装 HTTPS 证书与时区数据，保证服务在最小运行时镜像中可用
RUN apk --no-cache add ca-certificates tzdata

# 创建运行目录。dist 版镜像通常会绑定宿主机目录到 /data，
# 为避免宿主机文件权限与容器内普通用户 UID 不一致导致无法写入，
# 这里保持 root 运行，优先保证配置与 sqlite 数据可落盘。
RUN mkdir -p /app /data

WORKDIR /data

# 构建时请使用 dist 目录作为上下文：
# docker build -f Dockerfile.dist -t clisimplehub-server:dist dist
COPY cliSimpleHub-server-linux-amd64.tar.gz /tmp/cliSimpleHub-server-linux-amd64.tar.gz

# 解压发布产物到固定位置，保持镜像入口简单直接
RUN tar -xzf /tmp/cliSimpleHub-server-linux-amd64.tar.gz -C /app && \
    chmod +x /app/cliSimpleHub-server && \
    rm -f /tmp/cliSimpleHub-server-linux-amd64.tar.gz

ENV CONFIG_PATH=/data/config.json

EXPOSE 5600

ENTRYPOINT ["/app/cliSimpleHub-server"]