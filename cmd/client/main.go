package main

import (
	"fmt"
	"os"

	"netChat/internal/client"
)

// defaultServerAddr 本地直接运行时的默认服务端地址。
// 容器编排场景下不会走到这里，而是由环境变量 SERVER_ADDR 覆盖，
// 原因：容器内的 127.0.0.1 指向客户端容器自身，连不到服务端容器。
const defaultServerAddr = "127.0.0.1:8080"

func main() {
	defer fmt.Println("与服务器断开连接")

	cli := client.NewChatClient()

	// 服务端地址：优先读环境变量 SERVER_ADDR，未设置时回退到本地默认值。
	// 这样同一份二进制既能本地 go run，也能在 docker-compose 里用服务名互联。
	serverAddr := os.Getenv("SERVER_ADDR")
	if serverAddr == "" {
		serverAddr = defaultServerAddr
	}

	// 1. 连接服务端
	if err := cli.Connect(serverAddr); err != nil {
		fmt.Println(err)
		return
	}
	fmt.Println("成功连接服务器")

	// 2. 登录聊天室
	if err := cli.Login(); err != nil {
		fmt.Println(err)
		return
	}

	fmt.Println("【系统】正在进入聊天室，输入消息按回车发送，输入exit退出")

	// 3. 启动收发
	cli.Start()
	cli.Wait()
}
