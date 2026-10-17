// Package store 负责用户账号的 MySQL 持久化。
//
// 只做两件事：建表、按用户名认证（不存在则自动注册）。
// 不引入 ORM、迁移框架或 repository 接口，保持与项目其余部分一致的轻量风格。
package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/go-sql-driver/mysql"
	"golang.org/x/crypto/bcrypt"
)

// ErrInvalidPassword 用户已存在，但密码不匹配。
// 调用方据此给客户端返回统一的「用户名或密码错误」，不暴露账号是否存在。
var ErrInvalidPassword = errors.New("用户名或密码错误")

// mysqlDuplicateEntry 是 MySQL 唯一键冲突（Duplicate entry）的错误码。
const mysqlDuplicateEntry = 1062

// Store 用户账号存储
type Store struct {
	db *sql.DB
}

// Open 建立 MySQL 连接池，并在 ctx 超时前反复 Ping，直到数据库可用。
//
// compose 里已经用 healthcheck 保证启动顺序，这里再留一层重试，
// 覆盖本地直接运行、以及数据库刚就绪时短暂拒绝连接的抖动。
func Open(ctx context.Context, dsn string) (*Store, error) {
	db, err := sql.Open("mysql", dsn)
	if err != nil {
		return nil, fmt.Errorf("解析 MySQL DSN 失败: %w", err)
	}

	db.SetMaxOpenConns(10)
	db.SetMaxIdleConns(5)
	db.SetConnMaxLifetime(3 * time.Minute)

	var pingErr error
	for {
		pingCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
		pingErr = db.PingContext(pingCtx)
		cancel()
		if pingErr == nil {
			return &Store{db: db}, nil
		}

		select {
		case <-ctx.Done():
			db.Close()
			return nil, fmt.Errorf("连接 MySQL 失败: %w", pingErr)
		case <-time.After(time.Second):
		}
	}
}

// EnsureSchema 建表（幂等）。服务启动时执行，省掉单独的初始化脚本。
func (s *Store) EnsureSchema(ctx context.Context) error {
	const schema = `
CREATE TABLE IF NOT EXISTS users (
    id            BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
    username      VARCHAR(64)     NOT NULL,
    password_hash VARCHAR(255)    NOT NULL,
    created_at    TIMESTAMP       NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (id),
    UNIQUE KEY uk_users_username (username)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin`

	if _, err := s.db.ExecContext(ctx, schema); err != nil {
		return fmt.Errorf("创建 users 表失败: %w", err)
	}
	return nil
}

// Authenticate 校验用户名和密码；用户不存在时自动注册。
//
// created 表示本次调用是否新建了账号，仅用于日志提示。
// 密码错误返回 ErrInvalidPassword；数据库故障返回包装后的其他错误。
func (s *Store) Authenticate(ctx context.Context, username, password string) (created bool, err error) {
	var hash string
	err = s.db.QueryRowContext(ctx,
		`SELECT password_hash FROM users WHERE username = ?`, username).Scan(&hash)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return s.register(ctx, username, password)
	case err != nil:
		return false, fmt.Errorf("查询用户失败: %w", err)
	}

	if err := bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)); err != nil {
		return false, ErrInvalidPassword
	}
	return false, nil
}

// register 创建新账号。bcrypt 自带随机盐，同一密码每次生成的哈希都不同。
func (s *Store) register(ctx context.Context, username, password string) (bool, error) {
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return false, fmt.Errorf("生成密码哈希失败: %w", err)
	}

	_, err = s.db.ExecContext(ctx,
		`INSERT INTO users (username, password_hash) VALUES (?, ?)`, username, hash)
	if err == nil {
		return true, nil
	}
	if !isDuplicateEntry(err) {
		return false, fmt.Errorf("创建用户失败: %w", err)
	}

	// 并发首次注册：另一条连接刚插入了同名用户。
	// 回读哈希后按普通登录校验，密码一致才放行。
	if err := s.verifyExisting(ctx, username, password); err != nil {
		return false, err
	}
	return false, nil
}

func (s *Store) verifyExisting(ctx context.Context, username, password string) error {
	var hash string
	if err := s.db.QueryRowContext(ctx,
		`SELECT password_hash FROM users WHERE username = ?`, username).Scan(&hash); err != nil {
		return fmt.Errorf("查询用户失败: %w", err)
	}
	if err := bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)); err != nil {
		return ErrInvalidPassword
	}
	return nil
}

// isDuplicateEntry 判断是否为唯一键冲突。
// 用驱动提供的错误码，而不是比对错误文案，避免受 MySQL 语言/版本影响。
func isDuplicateEntry(err error) bool {
	var me *mysql.MySQLError
	return errors.As(err, &me) && me.Number == mysqlDuplicateEntry
}

// Close 关闭连接池
func (s *Store) Close() error {
	return s.db.Close()
}
