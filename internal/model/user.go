package model

import "time"

// User 用户业务对象
type User struct {
	Name       string    `json:"name"`
	JoinTime   time.Time `json:"join_time"`
	RemoteAddr string    `json:"-"`
}
