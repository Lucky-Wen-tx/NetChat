# NetChat

一个纯标准库（无第三方依赖）实现的 TCP 聊天室：服务端 + 命令行客户端，
消息用结构化 JSON 承载，带 Docker 双 target 镜像。

```
cmd/server        服务端入口（监听 :8080 / CHAT_NAME 指定聊天室名）
cmd/client        客户端入口（SERVER_ADDR 指定服务端地址）
internal/model    消息与用户对象
internal/server   服务端：连接管理、广播、读写协程
internal/client   客户端：登录、收发、退出
pkg/utils         协议打包/解析、昵称校验、控制台格式化
```

## 通信协议

传输层就是裸 TCP，**按行分隔**（`\n`），一行一条报文。

### 1. 握手

1. 客户端连上后，**第一行必须是昵称**，纯文本，不是 JSON。
2. 服务端校验昵称：

   | 规则 | 说明 |
   | --- | --- |
   | 长度 2~20 | 按**字符数**算，不是字节数（否则中文昵称会被误判超长） |
   | 无空白字符 | 含空格、制表符 |
   | 无控制字符 | 含 ANSI 转义序列——昵称会被打印进别人的终端，不拦就能操控它 |
   | 不与他人重名 | 房间内昵称唯一 |

3. 服务端回**恰好一条**报文：

   - `type = system`：登录成功，`content` 是欢迎语（含当前在线人数）。
   - `type = error`：登录失败，`content` 是原因，**随后立即关闭连接**。

   握手完成前，客户端发的任何其他内容都不会被处理。

### 2. 聊天

握手成功后，双方都按 `Message` 收发，每行一个 JSON 对象：

```json
{"type":"user","from":"alice","content":"你好","timestamp":"2026-10-16T14:03:21.5+08:00"}
```

| 字段 | 方向 | 说明 |
| --- | --- | --- |
| `type` | 双向 | `system` 服务端广播 / `user` 用户消息 / `error` 服务端拒绝 |
| `from` | 双向 | **只有服务端填的才可信**，客户端填什么都会被丢弃 |
| `content` | 双向 | 消息正文 |
| `timestamp` | 服务端 → 客户端 | RFC3339 时间，由服务端在转发时重新打上 |

### 3. 服务端如何对待客户端报文

服务端**只把客户端报文当作 `content` 的来源**，信封由服务端用自己的数据重建：

- `type` 必须显式是 `user`，其他值直接丢弃（否则客户端能伪造系统广播冒充服务端）；
- `from` 一律丢弃，按这条连接握手时登记的昵称重填（否则能冒用他人昵称发言）；
- `content` 长度不超过 2000 字符，且不能含控制字符。

单行还有 64KB 的硬上限（`bufio.Scanner` 的默认值），超了该连接会报错断开。

## 运行

本地：

```bash
go run ./cmd/server                      # 默认聊天室名「摸鱼小组」
CHAT_NAME=技术交流 go run ./cmd/server    # 指定聊天室名
go run ./cmd/client                      # 另开终端，默认连 127.0.0.1:8080
SERVER_ADDR=127.0.0.1:8080 go run ./cmd/client
```

Docker / Compose：

```bash
docker compose up -d --build server       # 起服务端（容器内监听 8080）
docker compose logs -f server
docker compose run --rm --no-deps client  # 进客户端聊天，退出即删容器
docker compose down
```

按当前 compose 配置，服务端**没有**把端口映射到宿主机，
所以宿主机上的 `go run ./cmd/client` 连不上容器里的服务端；
想本地调试就自己在 compose 里补一段 `ports`。

