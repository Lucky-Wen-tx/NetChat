package utils

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"netChat/internal/model"
)

// MaxContentLen 单条消息最大长度（字符数）。bufio.Scanner 还有 64KB 硬上限兜底，这里再加一道业务限制。
const MaxContentLen = 2000

// FormatPrintln 按消息类型格式化输出到客户端控制台
func FormatPrintln(msg *model.Message) {
	switch msg.Type {
	case model.MsgTypeSystem:
		fmt.Printf("【系统】%s\n", msg.Content)
	case model.MsgTypeUser:
		// 时间戳由服务端在重建消息时打上，这里换算成本地时区显示
		fmt.Printf("[%s] %s：%s\n", msg.Timestamp.Local().Format("15:04:05"), msg.From, msg.Content)
	case model.MsgTypeError:
		fmt.Printf("【错误】%s\n", msg.Content)
	default:
		// 兜底：万一服务端协议新增了类型而客户端还没同步，
		// 至少能看见内容，而不是静默把消息丢掉
		fmt.Printf("【未知消息类型:%s】%s\n", msg.Type, msg.Content)
	}
}

// ParseUserMsg 解析客户端报文。服务端只取 Content：Type 必须显式是 user，
// From 一律丢弃（由服务端按登录昵称重填），否则客户端能伪造 system 广播或冒用他人昵称。
func ParseUserMsg(line string) (*model.Message, error) {
	var msg model.Message
	if err := json.Unmarshal([]byte(line), &msg); err != nil {
		return nil, fmt.Errorf("不是合法的 JSON 报文")
	}
	if msg.Type != model.MsgTypeUser {
		return nil, fmt.Errorf("消息类型[%s]不被允许", msg.Type)
	}
	if msg.Content == "" {
		return nil, fmt.Errorf("消息内容为空")
	}
	if n := utf8.RuneCountInString(msg.Content); n > MaxContentLen {
		return nil, fmt.Errorf("消息过长：%d 字，上限 %d 字", n, MaxContentLen)
	}
	if strings.ContainsFunc(msg.Content, unicode.IsControl) {
		return nil, fmt.Errorf("消息内容不能包含控制字符")
	}
	return &msg, nil
}

// MsgSystem 打包服务端系统消息。这几个 Msg* 函数只返回 string 不返回 error：
// Message 字段全是可序列化类型，json.Marshal 不可能失败。
func MsgSystem(content string, from string) string {
	return mustMarshal(model.Message{
		Type:      model.MsgTypeSystem,
		From:      from,
		Content:   content,
		Timestamp: time.Now(),
	})
}

// MsgUser 打包用户聊天消息
func MsgUser(content string, from string) string {
	return mustMarshal(model.Message{
		Type:      model.MsgTypeUser,
		From:      from,
		Content:   content,
		Timestamp: time.Now(),
	})
}

// MsgError 打包错误消息，用于服务端在断开连接前告诉客户端原因
func MsgError(content string, from string) string {
	return mustMarshal(model.Message{
		Type:      model.MsgTypeError,
		From:      from,
		Content:   content,
		Timestamp: time.Now(),
	})
}

// mustMarshal 理论上不可达（见 MsgSystem 注释）。真出现了也不能发半截报文，
// 宁可发一条明确的系统提示。
func mustMarshal(msg model.Message) string {
	data, err := json.Marshal(msg)
	if err != nil {
		// 理论上不可达（见 MsgSystem 的注释），
		// 真出现了也不能把半截报文发给对端，宁可发一条明确的系统提示
		return MsgSystem(fmt.Sprintf("服务端内部错误：消息序列化失败(%v)", err), "")
	}
	return string(data)
}
