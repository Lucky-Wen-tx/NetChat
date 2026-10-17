package main

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"netChat/internal/server"
	"netChat/internal/store"
)

// defaultChatName 聊天室默认名称。
// 这是默认值的【唯一来源】：docker-compose.yml 只负责透传 CHAT_NAME 环境变量，
// 不写默认值。这样以后改默认名字只需改这一行，compose 文件不用动，
// 也就不会产生配置漂移、导致容器被意外重建。
const defaultChatName = "摸鱼小组"

// listenAddr 服务端监听地址
const listenAddr = ":8080"

func main() {
	// 聊天室名称：优先读环境变量 CHAT_NAME，未设置或只输入空白时回退到默认值。
	// 启动时指定：CHAT_NAME=技术交流 docker compose up -d server
	chatName := strings.TrimSpace(os.Getenv("CHAT_NAME"))
	if chatName == "" {
		chatName = defaultChatName
	}

	// 初始化 MySQL。必须放在 net.Listen 之前：数据库不可用时应直接失败退出，
	// 而不是让服务端看起来启动成功、实际每次登录都报错。
	// DSN 只从环境变量读取，代码里不保留任何默认密码。
	dsn := strings.TrimSpace(os.Getenv("MYSQL_DSN"))
	if dsn == "" {
		fmt.Println("缺少环境变量 MYSQL_DSN，例如：netchat:password@tcp(127.0.0.1:3306)/netchat?parseTime=true&charset=utf8mb4")
		os.Exit(1)
	}

	initCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	st, err := store.Open(initCtx, dsn)
	cancel()
	if err != nil {
		fmt.Printf("初始化 MySQL 失败：%v\n", err)
		os.Exit(1)
	}
	defer st.Close()

	schemaCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	err = st.EnsureSchema(schemaCtx)
	cancel()
	if err != nil {
		fmt.Printf("初始化数据表失败：%v\n", err)
		os.Exit(1)
	}

	// 初始化服务端实例对象
	chatServer := server.NewChatServer(chatName, st)

	listener, err := net.Listen("tcp", listenAddr)
	if err != nil {
		// 必须在这里退出：listener 是 nil，继续往下走到 Close/Accept 会直接 panic，
		// 真正的失败原因反而被 panic 栈埋掉
		fmt.Printf("监听 %s 失败：%v\n", listenAddr, err)
		os.Exit(1)
	}
	fmt.Printf("TCP 聊天服务端[%s]已启动，监听 %s\n", chatName, listenAddr)

	// 优雅关闭：收到信号只关监听、通知在线客户端，由 Accept 循环自然退出。
	// 不用 os.Exit——那会跳过所有 defer 并把进程直接掐断。
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-quit
		fmt.Println("\n服务端正在关闭...")
		listener.Close() // 令下面的 Accept 立刻返回
		chatServer.Shutdown("服务端正在关闭，连接将被断开")
	}()

	// 最大重连次数
	maxTest := 10
	for maxTest > 0 {
		conn, err := listener.Accept()
		if err != nil {
			// errors.Is 判断「监听已关闭」，比拿错误文案做字符串比对可靠
			if errors.Is(err, net.ErrClosed) {
				fmt.Println("服务端已停止监听，退出")
				return
			}
			fmt.Printf("接收连接失败一次: %v\n", err)
			maxTest--
			continue
		}
		go chatServer.HandleConn(conn)
	}
}
