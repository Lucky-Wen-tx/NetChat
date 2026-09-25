package utils

import (
	"fmt"
	"strings"
)

// VerifyName 校验昵称格式合法性
func VerifyName(name string) error {
	if len(name) < 2 || len(name) > 20 {
		return fmt.Errorf("长度要求2~20")
	}
	if strings.Contains(name, " ") {
		return fmt.Errorf("不能包含空格")
	}
	return nil
}
