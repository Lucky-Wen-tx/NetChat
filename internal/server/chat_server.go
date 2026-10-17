package server

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"net"
	"strings"
	"sync"
	"time"

	"netChat/internal/store"
	"netChat/pkg/utils"
)

const (
	// keepAlivePeriod TCP 保活探测周期。
	// 客户端断电、休眠或 NAT 超时留下的僵死连接靠内核探活回收，
	// 否则它会一直留在 clients 里：虚增在线人数，还被算作广播目标。
	keepAlivePeriod = 30 * time.Second

	// shutdownDrainTimeout 关服时等待各客户端发送队列排空的上限
	shutdownDrainTimeout = 500 * time.Millisecond

	// authenticateTimeout 单次登录认证访问数据库的超时。
	// 必须设上限：数据库卡住时不能把登录协程一直挂着。
	authenticateTimeout = 5 * time.Second
)

type ChatServer struct {
	name    string
	store   *store.Store
	clients map[string]*ClientConn
	lock    sync.RWMutex
}

func NewChatServer(name string, st *store.Store) *ChatServer {
	return &ChatServer{
		name:    name,
		store:   st,
		clients: make(map[string]*ClientConn),
	}
}

// broadcast 向在线客户端推送消息，except 指定要跳过的昵称（空串表示全员都发）。
//
// 注意本函数持有读锁，因此遍历过程中的每一个写动作都必须是非阻塞的（见 ClientConn.enqueue）。
func (s *ChatServer) broadcast(msg string, except string) {
	s.lock.RLock()
	defer s.lock.RUnlock()

	for name, c := range s.clients {
		if name == except {
			continue
		}
		c.enqueue(msg)
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

// pendingSends 统计所有客户端待发送队列里还剩多少条消息
func (s *ChatServer) pendingSends() int {
	s.lock.RLock()
	defer s.lock.RUnlock()

	n := 0
	for _, c := range s.clients {
		n += len(c.send)
	}
	return n
}

// rejectLogin 给登录失败的连接回一条 error 消息再关闭。
// 有了这条回执，客户端才能在 Login 阶段就明确失败，
// 而不是写成功就当自己进去了，随后才收到一句莫名其妙的错误。
func (s *ChatServer) rejectLogin(conn net.Conn, reason string) {
	fmt.Fprintln(conn, utils.MsgError(reason, s.name))
	conn.Close()
}

// HandleConn 处理新接入的客户端连接
func (s *ChatServer) HandleConn(conn net.Conn) {
	clientAddr := conn.RemoteAddr().String()
	fmt.Printf("新连接接入：%s\n", clientAddr)

	// 内核级保活，不改变应用层协议
	if tcpConn, ok := conn.(*net.TCPConn); ok {
		_ = tcpConn.SetKeepAlive(true)
		_ = tcpConn.SetKeepAlivePeriod(keepAlivePeriod)
	}

	// 这条连接自始至终只用这一个 Scanner，原因见 ClientConn.reader 的注释
	reader := bufio.NewScanner(conn)

	// 首行必须是昵称
	if !reader.Scan() {
		if err := reader.Err(); err != nil {
			fmt.Printf("读取连接[%s]昵称失败：%v\n", clientAddr, err)
		} else {
			fmt.Printf("连接[%s]未发送昵称，登录失败连接断开\n", clientAddr)
		}
		conn.Close()
		return
	}

	// 昵称必须由服务端自己校验：客户端那侧只是提前提示，绕开它是零成本的
	nickname := strings.TrimSpace(reader.Text())
	if err := utils.VerifyName(nickname); err != nil {
		s.rejectLogin(conn, fmt.Sprintf("昵称不合法：%v", err))
		fmt.Printf("连接[%s] 昵称[%s]不合法，已拒绝\n", clientAddr, nickname)
		return
	}

	// 第二行是密码。必须复用上面这个 reader（Scanner 会预读，换掉会丢数据）。
	// 密码不做 TrimSpace：注册和登录都必须按用户输入的原文处理。
	if !reader.Scan() {
		if err := reader.Err(); err != nil {
			fmt.Printf("读取连接[%s]密码失败：%v\n", clientAddr, err)
		} else {
			fmt.Printf("连接[%s]未发送密码，登录失败连接断开\n", clientAddr)
		}
		conn.Close()
		return
	}
	password := reader.Text()
	if err := utils.VerifyPassword(password); err != nil {
		s.rejectLogin(conn, fmt.Sprintf("密码不合法：%v", err))
		fmt.Printf("连接[%s] 用户[%s]密码不合法，已拒绝\n", clientAddr, nickname)
		return
	}

	authCtx, cancel := context.WithTimeout(context.Background(), authenticateTimeout)
	created, err := s.store.Authenticate(authCtx, nickname, password)
	cancel()
	switch {
	case errors.Is(err, store.ErrInvalidPassword):
		s.rejectLogin(conn, "用户名或密码错误")
		fmt.Printf("连接[%s] 用户[%s]密码错误，已拒绝\n", clientAddr, nickname)
		return
	case err != nil:
		// 只记录用户名和错误本身，绝不记录密码
		fmt.Printf("连接[%s] 用户[%s]认证失败（数据库错误）：%v\n", clientAddr, nickname, err)
		s.rejectLogin(conn, "服务器暂时无法验证登录，请稍后再试")
		return
	}
	if created {
		fmt.Printf("新用户[%s] 已自动注册\n", nickname)
	}

	client := newClientConn(conn, reader, nickname, s)
	if err := s.addClient(client); err != nil {
		s.rejectLogin(conn, fmt.Sprintf("%v", err))
		fmt.Printf("连接[%s] 昵称冲突：%s\n", clientAddr, nickname)
		return
	}

	// 登录回执。此刻 writeLoop 还没启动，直接写不会出现第二个写者。
	fmt.Fprintln(conn, utils.MsgSystem(fmt.Sprintf(
		"欢迎你，%s！当前在线 %d 人。", nickname, s.OnlineCount()), s.name))

	// 启动该客户端专属读写协程
	go client.writeLoop()
	go client.readLoop()

	// 系统广播上线通知（不含本人，本人已经在欢迎语里知道在线人数了）
	s.broadcast(utils.MsgSystem(fmt.Sprintf(
		"用户[%s] 加入本聊天室[%s]，当前在线人数%d人", nickname, s.name, s.OnlineCount()), s.name), nickname)
	fmt.Printf("用户[%s] 上线，地址：%s\n", nickname, clientAddr)
}

// Shutdown 广播关闭通知并断开所有客户端连接。
//
// 通知是尽力而为的：先投递，再等各客户端的发送队列排空（队列排空只说明
// 数据已交给写协程，所以再留一点余量让它们真正写进 TCP 缓冲），最后才断连接。
// 不等就直接 Close 的话，队列里的通知会被丢掉，客户端只会看到连接莫名其妙断了。
// 要做到严格可靠，需要每个 writeLoop 显式回一个"已写出"的信号，这里没这必要。
func (s *ChatServer) Shutdown(reason string) {
	s.broadcast(utils.MsgSystem(reason, s.name), "")

	deadline := time.Now().Add(shutdownDrainTimeout)
	for time.Now().Before(deadline) && s.pendingSends() > 0 {
		time.Sleep(10 * time.Millisecond)
	}
	time.Sleep(50 * time.Millisecond)

	s.lock.RLock()
	conns := make([]net.Conn, 0, len(s.clients))
	for _, c := range s.clients {
		conns = append(conns, c.Conn)
	}
	s.lock.RUnlock()

	for _, conn := range conns {
		conn.Close()
	}
}
