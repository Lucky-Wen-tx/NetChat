package model

import "time"

const (
	MsgTypeSystem = "system" // 系统消息
	MsgTypeUser   = "user"   // 用户聊天消息
)

// Message 结构化消息对象
type Message struct {
	Type      string    `json:"type"`
	From      string    `json:"from"`
	Content   string    `json:"content"`
	Timestamp time.Time `json:"timestamp"`
}
