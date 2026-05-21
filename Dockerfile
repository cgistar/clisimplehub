FROM alpine:3.22

# 安装 HTTPS 证书与时区数据，保证服务在最小运行时镜像中可用
RUN echo -e https://mirrors.ustc.edu.cn/alpine/v3.22/main/ > /etc/apk/repositories && \
    apk --no-cache add ca-certificates tzdata && \
    ln -snf /usr/share/zoneinfo/Asia/Shanghai /etc/localtime && \
    echo "Asia/Shanghai" > /etc/timezone && \
    mkdir -p /app /data

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