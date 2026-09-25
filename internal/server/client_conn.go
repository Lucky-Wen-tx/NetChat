package server

import (
	"bufio"
	"fmt"
	"net"
	"time"

	"netChat/internal/model"
)

// ClientConn 服务端客户端连接运行时对象
type ClientConn struct {
	User   model.User
	Conn   net.Conn
	send   chan string
	server *ChatServer
}

// newClientConn 创建客户端连接(包内私有，不对外暴露创建细节)
func newClientConn(conn net.Conn, name string, server *ChatServer) *ClientConn {
	return &ClientConn{
		User: model.User{
			Name:       name,
			JoinTime:   time.Now(),
			RemoteAddr: conn.RemoteAddr().String(),
		},
		Conn:   conn,
		send:   make(chan string, 16),
		server: server,
	}
}

// writeLoop 专属写协程：负责向该客户端发送所有消息
func (c *ClientConn) writeLoop() {
	for msg := range c.send {
		// 按行发送
		_, err := fmt.Fprintln(c.Conn, msg)
		if err != nil {
			fmt.Printf("向用户[%s]发送消息失败：%v\n", c.User.Name, err)
			return
		}
	}
}

// readLoop 专属读协程：读取客户端消息并触发广播
func (c *ClientConn) readLoop() {
	defer func() {
		close(c.send)
		c.server.removeClient(c)
		c.Conn.Close()
		// 广播下线通知
		c.server.broadcastSystem(fmt.Sprintf("用户%s 退出本聊天室，当前在线%d人", c.User.Name, c.server.OnlineCount()))
		fmt.Printf("用户[%s] 离线：%s\n", c.User.Name, c.User.RemoteAddr)
	}()

	scanner := bufio.NewScanner(c.Conn)
	for scanner.Scan() {
		msg := scanner.Text()
		fmt.Printf("收到用户[%s]消息：%s\n", c.User.Name, msg)

		c.server.broadcastUser(c.User.Name, msg)
	}

	if err := scanner.Err(); err != nil {
		fmt.Printf("用户[%s] 连接异常：%v\n", c.User.Name, err)
	}
}
