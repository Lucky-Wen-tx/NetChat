package client

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"strings"
	"sync"

	"netChat/internal/model"
	"netChat/pkg/utils"
)

// ChatClient 聊天室客户端
type ChatClient struct {
	conn net.Conn
	name string
	quit chan struct{} // 退出信号：任意一侧收尾时关闭

	// input 是控制台输入的唯一读取器，Login 与 inputLoop 共用。
	// 不能各建一个：bufio.Scanner 会预读最多 4096 字节，
	// 新建 Scanner 等于把已预读进内存、还没消费的输入（比如粘贴进来的多行）一起丢掉。
	input *bufio.Scanner

	// reader 同理，是服务端连接的唯一行读取器，Login 读登录回执、
	// acceptLoop 收消息都走它，中途绝不能重建。
	reader *bufio.Scanner

	stopOnce sync.Once
}

// NewChatClient 创建客户端实例
func NewChatClient() *ChatClient {
	return newChatClient(os.Stdin)
}

// newChatClient 用指定的输入源创建客户端。
// 单独抽出来是为了让测试能注入输入流——控制台输入是这条链路上唯一没法直接测的部分。
func newChatClient(input io.Reader) *ChatClient {
	return &ChatClient{
		quit:  make(chan struct{}),
		input: bufio.NewScanner(input),
	}
}

// Connect 连接服务端
func (c *ChatClient) Connect(addr string) error {
	conn, err := net.Dial("tcp", addr)
	if err != nil {
		return fmt.Errorf("连接服务器失败: %v", err)
	}
	c.conn = conn
	c.reader = bufio.NewScanner(conn)
	return nil
}

// Login 输入昵称、完成服务端登录，并等待服务端的确认
func (c *ChatClient) Login() error {
	fmt.Print("【系统】请输入自己的昵称：")

	for {
		if !c.input.Scan() {
			if err := c.input.Err(); err != nil {
				return fmt.Errorf("读取控制台输入失败: %v", err)
			}
			return fmt.Errorf("输入终止")
		}
		name := strings.TrimSpace(c.input.Text())
		if err := utils.VerifyName(name); err != nil {
			fmt.Printf("【警告】昵称格式不合法: %v\n", err)
			fmt.Print("【系统】请重新输入昵称：")
			continue
		}
		c.name = name
		break
	}

	// 密码只用于本次登录，不保存到 ChatClient 字段里。
	// 继续复用同一个 input Scanner，具体原因同昵称输入。
	fmt.Print("【系统】请输入密码（首次登录将自动注册）：")
	var password string
	for {
		if !c.input.Scan() {
			if err := c.input.Err(); err != nil {
				return fmt.Errorf("读取控制台输入失败: %v", err)
			}
			return fmt.Errorf("输入终止")
		}
		password = c.input.Text()
		if err := utils.VerifyPassword(password); err != nil {
			fmt.Printf("【警告】密码格式不合法: %v\n", err)
			fmt.Print("【系统】请重新输入密码：")
			continue
		}
		break
	}

	// 登录协议：第一行昵称、第二行密码，都是裸文本。
	// 密码按用户输入原样发送，不做 TrimSpace。
	if _, err := fmt.Fprintln(c.conn, c.name); err != nil {
		return fmt.Errorf("登录失败: %v", err)
	}
	if _, err := fmt.Fprintln(c.conn, password); err != nil {
		return fmt.Errorf("登录失败: %v", err)
	}

	// 必须等服务端回执才算登录成功。
	// 昵称冲突时服务端只回一条 error 就断开，绝不能凭「写成功」就宣布进入聊天室，
	// 否则用户会先看到「正在进入聊天室」，紧接着才收到失败提示。
	if !c.reader.Scan() {
		if err := c.reader.Err(); err != nil {
			return fmt.Errorf("等待服务端响应失败: %v", err)
		}
		return fmt.Errorf("服务端未响应就断开了连接")
	}

	var msg model.Message
	if err := json.Unmarshal(c.reader.Bytes(), &msg); err != nil {
		return fmt.Errorf("无法解析服务端响应: %v", err)
	}
	switch msg.Type {
	case model.MsgTypeError:
		return fmt.Errorf("登录失败: %s", msg.Content)
	case model.MsgTypeSystem:
		utils.FormatPrintln(&msg) // 打印欢迎语
	default:
		return fmt.Errorf("服务端响应类型异常: %s", msg.Type)
	}
	return nil
}

// Start 启动客户端：接收协程与控制台输入协程并发运行
func (c *ChatClient) Start() {
	go c.acceptLoop()
	go c.inputLoop()
}

// Wait 阻塞，直到任意一侧结束（服务端断开 / 用户输入 exit / 控制台 EOF）
func (c *ChatClient) Wait() {
	<-c.quit
}

// stop 结束客户端：关闭 quit 并关掉连接，两侧协程都能据此退出。只会执行一次。
func (c *ChatClient) stop() {
	c.stopOnce.Do(func() {
		close(c.quit)
		c.conn.Close() // 关闭连接可让另一侧阻塞在 Scan 的协程立刻返回
	})
}

// acceptLoop 接收协程：持续接收并打印服务端消息
func (c *ChatClient) acceptLoop() {
	defer c.stop()

	for c.reader.Scan() {
		var msg model.Message
		if err := json.Unmarshal(c.reader.Bytes(), &msg); err != nil {
			fmt.Printf("【系统】收到无法解析的消息，已忽略: %v\n", err)
			continue
		}
		utils.FormatPrintln(&msg)
	}

	// 走到这里说明服务端断开或读取出错，stop 会让主流程退出
	if err := c.reader.Err(); err != nil {
		fmt.Printf("与服务器连接异常: %v\n", err)
	} else {
		fmt.Println("【系统】服务端已关闭连接")
	}
}

// inputLoop 控制台输入协程：读取输入并发消息
func (c *ChatClient) inputLoop() {
	defer c.stop()

	for c.input.Scan() {
		content := strings.TrimSpace(c.input.Text())
		if content == "" {
			continue
		}
		if content == "exit" {
			fmt.Println("【系统】退出聊天室")
			return
		}
		// 打包信息；from 由服务端按登录昵称重填，这里填的只是报文占位
		if _, err := fmt.Fprintln(c.conn, utils.MsgUser(content, c.name)); err != nil {
			fmt.Printf("【系统】发送失败，与服务端的连接可能已断开: %v\n", err)
			return
		}
	}

	if err := c.input.Err(); err != nil {
		fmt.Printf("【系统】读取控制台输入异常: %v\n", err)
	}
}
