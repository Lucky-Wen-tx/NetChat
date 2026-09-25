package main

import (
	"fmt"
	"net"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"netChat/internal/server"
)

// defaultChatName 聊天室默认名称。
// 这是默认值的【唯一来源】：docker-compose.yml 只负责透传 CHAT_NAME 环境变量，
// 不写默认值。这样以后改默认名字只需改这一行，compose 文件不用动，
// 也就不会产生配置漂移、导致容器被意外重建。
const defaultChatName = "摸鱼小组"

func main() {
	// 聊天室名称：优先读环境变量 CHAT_NAME，未设置或只输入空白时回退到默认值。
	// 启动时指定：CHAT_NAME=技术交流 docker compose up -d server
	ChatName := strings.TrimSpace(os.Getenv("CHAT_NAME"))
	if ChatName == "" {
		ChatName = defaultChatName
	}

	// 初始化服务端实例对象
	chatServer := server.NewChatServer(ChatName)

	listener, err := net.Listen("tcp", ":8080")
	if err != nil {
		fmt.Printf("监听失败：%v\n", err)
	}
	defer listener.Close()
	fmt.Printf("TCP 聊天服务端[%s]已启动，监听 :8080\n", ChatName)

	// 监听系统信号，实现优雅关闭
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-quit
		fmt.Println("\n服务端正在关闭...")
		listener.Close()
		os.Exit(0)
	}()

	for {
		conn, err := listener.Accept()
		if err != nil {
			if opErr, ok := err.(*net.OpError); ok && opErr.Err.Error() == "use of closed network connection" {
				return
			}
			fmt.Printf("接收连接失败一次: %v\n", err)
			continue
		}
		go chatServer.HandleConn(conn)
	}
}