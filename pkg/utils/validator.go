package utils

import (
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"
)

const (
	// MinNameLen / MaxNameLen 昵称长度限制，单位是「字符数」而不是字节数。
	// 按字节数算的话，一个有 20 个字的中文昵称（60 字节）会被误判成超长。
	MinNameLen = 2
	MaxNameLen = 20
)

// VerifyName 校验昵称格式合法性。
//
// 服务端必须独立调用一次：客户端那侧的校验只是为了提前提示，
// 用裸 socket 连上来可以完全绕过它。
func VerifyName(name string) error {
	if n := utf8.RuneCountInString(name); n < MinNameLen || n > MaxNameLen {
		return fmt.Errorf("长度要求%d~%d个字符，当前%d个", MinNameLen, MaxNameLen, n)
	}
	if err := checkPrintable(name); err != nil {
		return err
	}
	return nil
}

// checkPrintable 拒绝空白字符与控制字符。
//
// 昵称和聊天内容都会被原样打印进服务端日志、以及其他用户的终端，
// 因此空白字符（会把昵称拆成两段）和控制字符（如 ANSI 转义 \x1b[2J 能清屏、
// 甚至伪造出以假乱真的输出）都要挡在入口。
func checkPrintable(s string) error {
	if strings.ContainsFunc(s, unicode.IsControl) {
		return fmt.Errorf("不能包含控制字符")
	}
	if strings.ContainsFunc(s, unicode.IsSpace) {
		return fmt.Errorf("不能包含空白字符")
	}
	return nil
}
