# 微信云托管部署镜像(参考 WeixinCloud/wxcloudrun-golang 模板)。
# 必填环境变量见 README「部署到微信云托管」:WECHAT_APP_ID、WECHAT_APP_SECRET、REDIS_ADDR(及 REDIS_PASSWORD);
# MYSQL_ADDRESS / MYSQL_USERNAME / MYSQL_PASSWORD 由云托管注入。

FROM golang:1.24-alpine AS builder
WORKDIR /app
ENV CGO_ENABLED=0 GOOS=linux GOPROXY=https://goproxy.cn,direct
# 先单独下载依赖,源码变动时可复用这一层缓存
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN go build -trimpath -ldflags="-s -w" -o /app/fc_server .

FROM alpine:3.20
RUN apk add --no-cache ca-certificates tzdata
# 服务端口 80 与云托管默认一致;日志只写 stdout 由云托管采集(容器内文件会随实例销毁);
# 只信任云托管内网网关转发的 X-Forwarded-For,用于取真实客户端 IP。
ENV TZ=Asia/Shanghai \
    PORT=80 \
    GIN_MODE=release \
    LOG_DIR="" \
    LOG_CONSOLE=true \
    TRUSTED_PROXIES=10.0.0.0/8,172.16.0.0/12,192.168.0.0/16
WORKDIR /app
COPY --from=builder /app/fc_server /app/fc_server
EXPOSE 80
CMD ["/app/fc_server"]
