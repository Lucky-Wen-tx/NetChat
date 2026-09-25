package client

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"strings"

	"netChat/internal/model"
	"netChat/pkg/utils"
)

// ChatClient 聊天室客户端
type ChatClient struct {
	conn net.Conn
	name string
	quit chan struct{} // 退出信号
}

// NewChatClient 创建客户端实例
func NewChatClient() *ChatClient {
	return &ChatClient{
		quit: make(chan struct{}),
	}
}

// Connect 连接服务端
func (c *ChatClient) Connect(addr string) error {
	conn, err := net.Dial("tcp", addr)
	if err != nil {
		return fmt.Errorf("连接服务器失败: %v", err)
	}
	c.conn = conn
	return nil
}

// Login 输入昵称并完成服务端登录
func (c *ChatClient) Login() error {
	scanner := bufio.NewScanner(os.Stdin)
	fmt.Print("【系统】请输入自己的昵称：")
	
	for {
		if !scanner.Scan() {
			return fmt.Errorf("输入终止")
		}
		name := strings.TrimSpace(scanner.Text())
		if err := utils.VerifyName(name); err != nil {
			fmt.Printf("【警告】昵称格式不合法: %v\n", err)
			fmt.Print("【系统】请重新输入昵称：")
			continue
		}
		c.name = name
		break
	}

	if err := scanner.Err(); err != nil {
		return fmt.Errorf("与服务器连接异常: %v\n", err)
	}

	// 第一条消息发送昵称，完成服务端登录
	_, err := fmt.Fprintln(c.conn, c.name)
	if err != nil {
		return fmt.Errorf("登录失败: %v", err)
	}
	return nil
}

// Start 启动客户端：启动接收协程，主线程负责发送
func (c *ChatClient) Start() {
	go c.acceptLoop()
	c.sendLoop()
}

// acceptLoop 后台接收协程：持续接收并打印消息
func (c *ChatClient) acceptLoop() {
	defer close(c.quit)
	scanner := bufio.NewScanner(c.conn)
	for scanner.Scan() {
		var msg model.Message
		err := json.Unmarshal(scanner.Bytes(), &msg)
		if err != nil {
			fmt.Printf("解析消息失败: %v\n", err)
			continue
		}

		// 按消息类型格式化输出
		utils.FormatPrintln(&msg)
	}

	if err := scanner.Err(); err != nil {
		fmt.Printf("与服务器连接异常: %v\n", err)
	}
}

// sendLoop 主线程发送逻辑：读取控制台输入并发消息
func (c *ChatClient) sendLoop() {
	scanner := bufio.NewScanner(os.Stdin)
	for scanner.Scan() {
		content := strings.TrimSpace(scanner.Text())
		if content == "" {
			continue
		}
		if content == "exit" {
			fmt.Println("【系统】退出聊天室")
			c.conn.Close()
			return
		}
		// 打包信息
		msg, err := utils.MsgUser(content, c.name)
		if err != nil {
			fmt.Printf("序列化消息失败，用户[%s]: %v\n", c.name, err)
			fmt.Println("【系统】发送消息失败")
			return
		}
		_, err = fmt.Fprintln(c.conn, msg)
		if err != nil {
			fmt.Printf("请检查服务器连接: %v\n", err)
			fmt.Println("【系统】发送消息失败")
			return
		}
		
	}

	if err := scanner.Err(); err != nil {
		fmt.Printf("与服务器连接异常: %v\n", err)
	}
}

// Wait 等待客户端退出
func (c *ChatClient) Wait() {
	<-c.quit
}
