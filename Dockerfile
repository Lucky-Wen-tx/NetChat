# ==============================================================================
# NetChat 多阶段构建 Dockerfile
#
# 单文件双 target 设计：server / client 两个运行时镜像共用同一个 builder 阶段，
# 因此依赖缓存层和编译产物只算一次，第二次构建可直接命中缓存。
#
# 手动构建用法：
#   docker build --target server -t netchat-server:latest .
#   docker build --target client -t netchat-client:latest .
# 通常不手动执行，交给 docker compose 编排（见 docker-compose.yml）
# ==============================================================================


# ------------------------------------------------------------------------------
# 阶段 1：builder —— 编译环境，不会进入最终镜像
# ------------------------------------------------------------------------------
# 构建镜像 tag 与 go.mod 中声明的 go 1.26.5 保持一致，
# 避免 Go 工具链因版本不足去联网下载 toolchain 导致构建失败。
FROM golang:1.26.5-alpine AS builder

WORKDIR /build

# 先只拷贝依赖清单，再执行下载。
# 目的：利用 Docker 的分层缓存——只要 go.mod 不变，这一层就命中缓存，
# 后续改源码不会触发依赖重新下载。
# 注意：本项目没有 go.sum（无第三方依赖），所以这里不能写 "COPY go.mod go.sum ./"，
# 那样会因找不到 go.sum 而直接构建失败。
COPY go.mod ./

# 当前项目纯标准库，该命令实际不下载任何东西；
# 保留此结构是为了将来引入第三方依赖时自动获得缓存收益。
RUN go mod download

# 再拷贝源码。源码是频繁改动的，必须放在依赖层之后，否则每次改代码都要重下依赖。
COPY . .

# 一次性编译出两个二进制，产物统一放到 /out 供后续两个运行时阶段取用
#   CGO_ENABLED=0    关闭 cgo，产出纯静态二进制，不绑定构建机的 libc
#   GOOS=linux       明确目标平台，防止在 Windows/Mac 上构建出错误平台的可执行文件
#   -trimpath        去掉二进制内嵌的本地绝对路径（如 E:\Golang\study\NetChat）
#   -ldflags "-s -w" 去掉符号表和 DWARF 调试信息，体积约减小 25%~30%
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/server ./cmd/server \
 && CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/client ./cmd/client


# ------------------------------------------------------------------------------
# 阶段 2：server —— 服务端运行时镜像
# ------------------------------------------------------------------------------
FROM alpine:3.20 AS server

# 以非 root 运行，降低容器被攻破后的影响面。
# 服务端监听 8080（大于 1024 的非特权端口），普通用户即可绑定，无需 root。
RUN adduser -D -u 10001 netchat

WORKDIR /app
COPY --from=builder /out/server /app/server

# EXPOSE 仅为文档说明，不会真的把端口发布到宿主机（那是 compose ports 的职责）
EXPOSE 8080

USER netchat
ENTRYPOINT ["/app/server"]


# ------------------------------------------------------------------------------
# 阶段 3：client —— 客户端运行时镜像
# ------------------------------------------------------------------------------
FROM alpine:3.20 AS client

RUN adduser -D -u 10001 netchat

WORKDIR /app
COPY --from=builder /out/client /app/client

USER netchat
ENTRYPOINT ["/app/client"]
