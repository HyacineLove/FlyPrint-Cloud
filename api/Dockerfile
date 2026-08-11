ARG DOCKERHUB_MIRROR=docker.1ms.run
FROM ${DOCKERHUB_MIRROR}/library/golang:1.25-alpine AS builder

# 设置Go代理和网络配置
ENV GOPROXY=https://goproxy.cn,direct
ENV GOSUMDB=sum.golang.org

WORKDIR /app

COPY . .

# 下载依赖并生成go.sum
RUN go mod tidy -x
RUN go mod verify

# 构建应用
RUN CGO_ENABLED=0 GOOS=linux go build -a -installsuffix cgo -o main ./cmd/server

# 运行阶段
FROM ${DOCKERHUB_MIRROR}/library/alpine:3.21

RUN addgroup -S -g 10001 flyprint && adduser -S -D -H -u 10001 -G flyprint flyprint && \
    mkdir -p /app/uploads && chown -R flyprint:flyprint /app

WORKDIR /app

# 从builder阶段复制二进制文件
COPY --from=builder --chown=flyprint:flyprint /app/main ./main

# 暴露端口
EXPOSE 8080

# 运行应用
USER 10001:10001

CMD ["./main"]
