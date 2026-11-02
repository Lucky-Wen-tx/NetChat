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

	"golang.org/x/crypto/bcrypt"
)

const (
	// keepAlivePeriod TCP 保活周期。僵死连接靠内核探活回收，否则一直占着在线列表。
	keepAlivePeriod = 30 * time.Second

	// shutdownDrainTimeout 关服时等待发送队列排空的上限
	shutdownDrainTimeout = 500 * time.Millisecond

	// authenticateTimeout 单次登录认证的超时，防止数据库卡住时登录协程一直挂着。
	authenticateTimeout = 5 * time.Second
)

type ChatServer struct {
	name       string
	store      *store.Store
	redisStore *store.RedisStore
	clients    map[string]*ClientConn
	lock       sync.RWMutex
}

func NewChatServer(name string, st *store.Store, rs *store.RedisStore) *ChatServer {
	s := &ChatServer{
		name:       name,
		store:      st,
		redisStore: rs,
		clients:    make(map[string]*ClientConn),
	}
	go s.queueWorker()
	return s
}

// broadcast 向在线客户端推送消息，except 指定要跳过的昵称（空串表示全员都发）。
// 持读锁遍历，因此 enqueue 必须非阻塞（见 ClientConn.enqueue）。
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

// rejectLogin 回一条 error 再关闭连接，让客户端在 Login 阶段就明确失败。
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

	// 这条连接自始至终只用这一个 Scanner，原因见 ClientConn.reader
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

	// 昵称必须由服务端校验：客户端那侧只是提前提示，绕开它是零成本的
	nickname := strings.TrimSpace(reader.Text())
	if err := utils.VerifyName(nickname); err != nil {
		s.rejectLogin(conn, fmt.Sprintf("昵称不合法：%v", err))
		fmt.Printf("连接[%s] 昵称[%s]不合法，已拒绝\n", clientAddr, nickname)
		return
	}

	// 第二行是密码。必须复用同一个 reader（Scanner 会预读，换掉会丢数据）。
	// 密码不做 TrimSpace：注册和登录都按用户输入原文处理。
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
	created, err := s.authenticate(authCtx, nickname, password)
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

	// 系统广播上线通知（不含本人）
	s.broadcast(utils.MsgSystem(fmt.Sprintf(
		"用户[%s] 加入本聊天室[%s]，当前在线人数%d人", nickname, s.name, s.OnlineCount()), s.name), nickname)
	fmt.Printf("用户[%s] 上线，地址：%s\n", nickname, clientAddr)
}

// Shutdown 广播关闭通知并断开所有连接。
// 先投递再等队列排空，不等的话通知会被丢掉，客户端只会看到连接莫名断开。
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

// authenticate 先查 Redis 缓存，未命中走 MySQL，成功后回写缓存。
// 缓存 TTL 24 小时，过期后自然回源。
func (s *ChatServer) authenticate(ctx context.Context, username, password string) (created bool, err error) {
	// 1. 先查 Redis 缓存
	hash, hit, err := s.redisStore.GetUserHash(ctx, username)
	if err != nil {
		fmt.Printf("Redis 缓存查询失败（用户[%s]），回退 MySQL：%v\n", username, err)
	} else if hit {
		if bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)) != nil {
			return false, store.ErrInvalidPassword
		}
		return false, nil
	}

	// 2. 缓存未命中，走 MySQL
	created, err = s.store.Authenticate(ctx, username, password)
	if err != nil {
		return false, err
	}

	// 3. 认证成功，回写缓存
	dbHash, err := s.store.GetHash(ctx, username)
	if err != nil {
		fmt.Printf("用户[%s] 认证成功但获取哈希失败：%v\n", username, err)
		return created, nil
	}
	if err := s.redisStore.SetUserHash(ctx, username, dbHash, 24*time.Hour); err != nil {
		fmt.Printf("用户[%s] 缓存写入失败：%v\n", username, err)
	}
	return created, nil
}

// queueWorker 循环从 Redis 取消息并广播。单 worker 保证按序广播。
func (s *ChatServer) queueWorker() {
	for {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		item, err := s.redisStore.PopMessage(ctx, 1*time.Second)
		cancel()
		if err != nil {
			fmt.Printf("消息队列消费失败：%v\n", err)
			time.Sleep(time.Second)
			continue
		}
		if item.Payload == "" {
			continue // 超时无消息
		}
		s.broadcast(item.Payload, item.Except)
	}
}

// handleRank 处理 /rank 命令：查询活跃度 Top10 并单独回复请求者。
func (s *ChatServer) handleRank(client *ClientConn) {
	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	entries, err := s.redisStore.GetTopActivity(ctx, 10)
	cancel()

	if err != nil {
		client.enqueue(utils.MsgError(fmt.Sprintf("查询活跃度失败：%v", err), s.name))
		return
	}
	if len(entries) == 0 {
		client.enqueue(utils.MsgSystem("暂无活跃度数据", s.name))
		return
	}

	var sb strings.Builder
	sb.WriteString("【活跃度排名 Top10】\n")
	for i, e := range entries {
		sb.WriteString(fmt.Sprintf("  %d. %s (%d条消息)\n", i+1, e.Name, e.Score))
	}
	client.enqueue(utils.MsgSystem(sb.String(), s.name))
}
