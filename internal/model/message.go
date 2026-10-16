package model

import "time"

const (
	MsgTypeSystem = "system" // 服务端系统广播
	MsgTypeUser   = "user"   // 用户聊天消息
	MsgTypeError  = "error"  // 服务端拒绝请求（如登录失败），随后会断开连接
)

// Message 结构化消息对象
//
// 约定：Type 与 From 由服务端生成或校正，客户端发上来的同名字段一律不可信。
// 具体见 utils.ParseUserMsg 与服务端 readLoop 的用法。
type Message struct {
	Type      string    `json:"type"`
	From      string    `json:"from"`
	Content   string    `json:"content"`
	Timestamp time.Time `json:"timestamp"`
}
