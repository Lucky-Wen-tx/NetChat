package server

import (
	"bufio"
	"fmt"
	"net"
	"sync"

	"netChat/pkg/utils"
)

type ChatServer struct {
	name    string
	clients map[string]*ClientConn
	lock    sync.RWMutex
}

func NewChatServer(name string) *ChatServer {
	return &ChatServer{
		name:    name,
		clients: make(map[string]*ClientConn),
	}
}

// broadcastSystem 广播系统消息
func (s *ChatServer) broadcastSystem(content string) {
	msg, _ := utils.MsgSystem(content, s.name)

	s.lock.RLock()
	defer s.lock.RUnlock()
	for _, c := range s.clients {
		c.send <- msg
	}
}

// broadcastUser 广播用户消息，排除发送者本人
func (s *ChatServer) broadcastUser(sender string, msg string) {
	s.lock.RLock()
	defer s.lock.RUnlock()

	for name, c := range s.clients {
		if name == sender {
			continue
		}
		c.send <- msg
	}
}

// addClient 新增在线用户，重名返回错误
func (s *ChatServer) addClient(client *ClientConn) error {
	s.lock.Lock()
	defer s.lock.Unlock()
	if _, exist := s.clients[client.User.Name]; exist {
		return fmt.Errorf("该昵称[%s]已被占用", client.User.Name)
	}
	s.clients[client.User.Name] = client
	return nil
}

// removeClient 移除在线用户
func (s *ChatServer) removeClient(client *ClientConn) {
	s.lock.Lock()
	defer s.lock.Unlock()
	delete(s.clients, client.User.Name)
}

// OnlineCount 获取当前在线人数
func (s *ChatServer) OnlineCount() int {
	s.lock.RLock()
	defer s.lock.RUnlock()
	return len(s.clients)
}

// HandleConn 处理新接入的客户端连接
func (s *ChatServer) HandleConn(conn net.Conn) {
	clientAddr := conn.RemoteAddr().String()
	fmt.Printf("新连接接入：%s\n", clientAddr)

	// 使用昵称完成登录，并校验通过
	scanner := bufio.NewScanner(conn)
	if !scanner.Scan() {
		conn.Close()
		fmt.Printf("连接[%s]未发送昵称，登录失败连接断开\n", clientAddr)
		return
	}

	nickname := scanner.Text()

	client := newClientConn(conn, nickname, s)
	if err := s.addClient(client); err != nil {
		tip := fmt.Sprintf("%v",err)
		msg, err := utils.MsgSystem(tip, s.name)
		if err != nil {
			fmt.Printf("序列化消息失败，系统: %v\n", err)
		}
		fmt.Fprintln(conn, msg)
		conn.Close()
		fmt.Printf("连接[%s] 昵称冲突: %s\n", clientAddr, nickname)
		return
	}

	// 启动该客户端专属读写协程
	go client.writeLoop()
	go client.readLoop()

	// 系统广播上线通知
	s.broadcastSystem(fmt.Sprintf("用户[%s] 加入本聊天室[%s]，当前在线人数%d人", nickname, s.name, s.OnlineCount()))
	fmt.Printf("用户[%s] 上线，地址：%s\n", nickname, clientAddr)
}
