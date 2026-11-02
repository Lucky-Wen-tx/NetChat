package utils

import (
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"
)

const (
	// MinNameLen / MaxNameLen 昵称长度限制，按字符数算而非字节数——
	// 否则一个 20 字中文昵称（60 字节）会被误判超长。
	MinNameLen = 2
	MaxNameLen = 20

	// MinPasswordLen 密码最小长度（字符数）
	MinPasswordLen = 6

	// MaxPasswordBytes 密码最大长度（字节数）。bcrypt 硬限制：超过 72 字节会直接报错。
	MaxPasswordBytes = 72
)

// VerifyPassword 校验密码格式。客户端和服务端都要调用：客户端只为了提前提示，
// 服务端调用才是安全边界，裸 socket 可以完全绕过客户端校验。
func VerifyPassword(password string) error {
	if n := utf8.RuneCountInString(password); n < MinPasswordLen {
		return fmt.Errorf("长度至少%d个字符，当前%d个", MinPasswordLen, n)
	}
	if n := len(password); n > MaxPasswordBytes {
		return fmt.Errorf("长度不能超过%d字节，当前%d字节", MaxPasswordBytes, n)
	}
	if err := checkPrintable(password); err != nil {
		return err
	}
	return nil
}

// VerifyName 校验昵称格式。服务端必须独立调用一次：客户端那侧的校验只是为了提前提示，
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

// checkPrintable 拒绝空白字符和控制字符。昵称和聊天内容都会被原样打印进服务端日志
// 和其他用户终端，空白字符会把昵称拆成两段，控制字符能伪造输出（如 ANSI 转义清屏）。
func checkPrintable(s string) error {
	if strings.ContainsFunc(s, unicode.IsControl) {
		return fmt.Errorf("不能包含控制字符")
	}
	if strings.ContainsFunc(s, unicode.IsSpace) {
		return fmt.Errorf("不能包含空白字符")
	}
	return nil
}
