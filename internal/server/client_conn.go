package server

import (
	"bufio"
	"fmt"
	"net"
	"strings"
	"sync"
	"time"

	"netChat/internal/model"
	"netChat/pkg/utils"
)

// sendBufferSize 单个客户端待发送队列的长度。
//
// 它只用来削峰和识别失联：一旦写满就判定该客户端已经不读数据了（见 enqueue）。
// 取 64 是个折中——太小的话，客户端稍有一次卡顿（一次 GC、一次网络抖动）
// 就会被误踢；太大的话，一个慢客户端能占的内存又太多。
// 真正做到"判活"的是写失败和 TCP 保活，队列长度只是第一道快速反应。
const sendBufferSize = 64

// ClientConn 服务端客户端连接运行时对象
type ClientConn struct {
	User   model.User
	Conn   net.Conn
	send   chan string
	server *ChatServer

	// reader 是这条连接唯一的行读取器：昵称和后续消息都从它读。
	// 必须从头用到尾，不能读完昵称再新建一个——
	// bufio.Scanner 会预读最多 4096 字节，换掉 Scanner 就等于把
	// 已经预读进内存、但还没消费的数据（很可能是客户端发的第一条消息）一起丢掉。
	reader *bufio.Scanner

	// closeOnce 保证 cleanup 只执行一次
	closeOnce sync.Once
}

// newClientConn 创建客户端连接(包内私有，不对外暴露创建细节)
func newClientConn(conn net.Conn, reader *bufio.Scanner, name string, server *ChatServer) *ClientConn {
	return &ClientConn{
		User: model.User{
			Name:       name,
			JoinTime:   time.Now(),
			RemoteAddr: conn.RemoteAddr().String(),
		},
		Conn:   conn,
		reader: reader,
		send:   make(chan string, sendBufferSize),
		server: server,
	}
}

// enqueue 非阻塞地投递一条待发送消息。
//
// 队列写满说明这个客户端已经长时间不读数据（卡死、断电或故意为之）。
// 这里绝不能阻塞：广播是持有服务端读锁进行的，一旦卡住，
// 其他人的广播、新用户的登录会全部一起卡死——一个客户端就能拖垮整个服务端。
// 所以直接断开它，剩下的交给 readLoop 的统一清理流程。
func (c *ClientConn) enqueue(msg string) {
	select {
	case c.send <- msg:
	default:
		fmt.Printf("用户[%s] 发送队列已满，判定为失联连接并断开\n", c.User.Name)
		c.Conn.Close() // 关掉连接会让 readLoop 的 Scan 报错退出，从而走到 cleanup
	}
}

// writeLoop 专属写协程：负责向该客户端发送所有消息
func (c *ClientConn) writeLoop() {
	for msg := range c.send {
		// 按行发送
		if _, err := fmt.Fprintln(c.Conn, msg); err != nil {
			fmt.Printf("向用户[%s]发送消息失败：%v\n", c.User.Name, err)
			c.Conn.Close() // 同样触发 readLoop 退出，走统一清理
			return
		}
	}
}

// readLoop 专属读协程：读取客户端消息并触发广播
func (c *ClientConn) readLoop() {
	defer c.cleanup()

	for c.reader.Scan() {
		line := c.reader.Text()
		if strings.TrimSpace(line) == "" {
			continue
		}

		// 客户端报文只当「内容」来源：type 必须是 user，from 一律丢弃。
		// 信封由服务端用这条连接上登记的昵称重建，
		// 否则客户端能伪造 type=system 冒充服务端、或冒用他人昵称发言。
		in, err := utils.ParseUserMsg(line)
		if err != nil {
			fmt.Printf("丢弃用户[%s]的非法报文(%v)：%s\n", c.User.Name, err, line)
			continue
		}
		fmt.Printf("收到用户[%s]消息：%s\n", c.User.Name, in.Content)

		c.server.broadcast(utils.MsgUser(in.Content, c.User.Name), c.User.Name)
	}

	if err := c.reader.Err(); err != nil {
		fmt.Printf("用户[%s] 连接异常：%v\n", c.User.Name, err)
	}
}

// cleanup 连接结束时的统一收尾，只会真正执行一次
func (c *ClientConn) cleanup() {
	c.closeOnce.Do(func() {
		// 顺序不能反。removeClient 要拿写锁，而广播拿的是读锁，两者互斥：
		// 它一旦返回，就不可能还有广播正拿着从 clients 里取到的这个 c 去写 c.send。
		// 此时关闭通道才安全；反过来先关通道，就有概率
		// panic: send on closed channel，直接把整个服务端进程带走。
		c.server.removeClient(c)
		close(c.send) // 通知 writeLoop 退出
		c.Conn.Close()

		// 广播下线通知
		c.server.broadcast(utils.MsgSystem(
			fmt.Sprintf("用户[%s] 退出本聊天室，当前在线%d人", c.User.Name, c.server.OnlineCount()),
			c.server.name,
		), "")
		fmt.Printf("用户[%s] 离线：%s\n", c.User.Name, c.User.RemoteAddr)
	})
}
