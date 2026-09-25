package utils

import (
	"fmt"
	"time"
	"encoding/json"
	"netChat/internal/model"
)

// 按消息类型格式化输出
func FormatPrintln(msg *model.Message) {	
	switch msg.Type {
	case model.MsgTypeSystem:
		fmt.Printf("【系统】%s\n", msg.Content)
	case model.MsgTypeUser:
		fmt.Printf("%s：%s\n", msg.From, msg.Content)
	}
}

// 打包系统消息
func MsgSystem(content string, from string) (string, error){
	msg := model.Message{
		Type:      model.MsgTypeSystem,
		From:			 from,
		Content:   content,
		Timestamp: time.Now(),
	}
	data, err := json.Marshal(msg)

	return string(data), err
}

// 打包用户消息
func MsgUser(content string, from string) (string, error){
	msg := model.Message{
		Type:      model.MsgTypeUser,
		From:			 from,
		Content:   content,
		Timestamp: time.Now(),
	}
	data, err := json.Marshal(msg)
	
	return string(data), err
}
