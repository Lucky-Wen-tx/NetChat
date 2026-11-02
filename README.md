# NetChat

一个 Go 实现的 TCP 聊天室：服务端 + 命令行客户端，消息用结构化 JSON 承载。
用户名和密码通过 MySQL 持久化，密码只存 bcrypt 哈希。
Redis 承担活跃度排名、异步消息队列和用户哈希缓存。带 Docker 多阶段构建。

```
cmd/server        服务端入口（监听 :8080 / CHAT_NAME 指定聊天室名）
cmd/client        客户端入口（SERVER_ADDR 指定服务端地址）
internal/model    消息与用户对象
internal/server   服务端：连接管理、登录认证、广播、读写协程、队列消费
internal/client   客户端：登录、收发、退出
internal/store    用户账号持久化（MySQL）+ Redis 封装（排名/队列/缓存）
pkg/utils         协议打包/解析、昵称与密码校验、控制台格式化
```

## 通信协议

传输层就是裸 TCP，**按行分隔**（`\n`），一行一条报文。

### 1. 握手

1. 客户端连上后依次发送**两行裸文本**（不是 JSON）：
   - 第 1 行：昵称
   - 第 2 行：密码

2. 服务端校验昵称：

   | 规则 | 说明 |
   | --- | --- |
   | 长度 2~20 | 按**字符数**算，不是字节数 |
   | 无空白字符 | 含空格、制表符 |
   | 无控制字符 | 含 ANSI 转义序列——昵称会被打印进别人的终端 |
   | 不与他人重名 | 房间内昵称唯一 |

   密码规则：至少 6 个字符、不超过 72 字节（bcrypt 上限）、无空白/控制字符；密码按原样处理，不做 trim。
   账号行为：用户名不存在时**首次登录自动注册**，已存在时校验密码，不匹配则拒绝登录。

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
- `type` 必须显式是 `user`，其他值直接丢弃；
- `from` 一律丢弃，按这条连接握手时登记的昵称重填；
- `content` 长度不超过 2000 字符，且不能含控制字符。

单行还有 64KB 的硬上限（`bufio.Scanner` 的默认值），超了该连接会报错断开。

### 4. 客户端命令

| 命令 | 行为 |
| --- | --- |
| `/rank` | 查询活跃度 Top10 排名，服务端单独回复请求者 |
| `/exit` | 退出聊天室 |

## Redis 功能

### 活跃度排名

用 Redis Sorted Set（`activity:rank`）记录每个用户的消息数。用户每发一条消息 `ZINCRBY` +1，
离线后分数不清零，跨会话累加。客户端发 `/rank` 查看 Top10。

### 异步消息队列

用户发消息后，服务端将报文 `LPUSH` 进 Redis List（`msg:queue`），
由独立的 `queueWorker` 协程 `BRPOP` 取出并广播。
I/O 读协程只负责入队，广播由 worker 完成，慢客户端不会阻塞其他用户的读循环。

### 用户哈希缓存

登录时先查 Redis（`user:hash:<username>`），命中则直接比对 bcrypt，
未命中走 MySQL 认证，成功后回写缓存（TTL 24 小时）。
缓存里存的是 bcrypt 哈希，不是明文密码。

## 运行

本地（需要先有可用的 MySQL 和 Redis）：

```bash
# 服务端必须提供 MYSQL_DSN 和 REDIS_ADDR，否则启动失败
MYSQL_DSN='netchat:password@tcp(127.0.0.1:3306)/netchat?parseTime=true&charset=utf8mb4' \
REDIS_ADDR='127.0.0.1:6379' \
  go run ./cmd/server

go run ./cmd/client                      # 另开终端，默认连 127.0.0.1:8080
```

Docker / Compose：

```bash
cp .env.example .env                      # 首次运行：复制并修改数据库密码
docker compose up -d --build mysql redis server  # 起 MySQL + Redis + 服务端
docker compose logs -f server
docker compose run --rm --no-deps client  # 进客户端聊天，退出即删容器
docker compose down                       # 停止，数据保留在命名卷中
docker compose down -v                    # 连数据一起删除
```

`users` 表由服务端启动时自动创建，无需手动执行 SQL。
数据保存在 `mysql-data` 命名卷中：普通 `down` 不会丢，`down -v` 才会清空。

按当前 compose 配置，服务端**没有**把端口映射到宿主机，所以宿主机上的 `go run ./cmd/client`
连不上容器里的服务端；想本地调试就自己在 compose 里补一段 `ports`。
