package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

// QueueItem 消息队列项。Except 指定广播时要跳过的发送者昵称。
type QueueItem struct {
	Payload string `json:"payload"`
	Except  string `json:"except"`
}

type RedisStore struct {
	client *redis.Client
}

func OpenRedis(ctx context.Context, addr string) (*RedisStore, error) {
	client := redis.NewClient(&redis.Options{
		Addr: addr,
	})

	pingCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	if err := client.Ping(pingCtx).Err(); err != nil {
		_ = client.Close()
		return nil, fmt.Errorf("连接 Redis 失败: %w", err)
	}
	return &RedisStore{client: client}, nil
}

func (r *RedisStore) Close() error {
	return r.client.Close()
}

// IncrActivity 用户活跃度 +1
func (r *RedisStore) IncrActivity(ctx context.Context, username string) error {
	return r.client.ZIncrBy(ctx, "activity:rank", 1, username).Err()
}

type ActivityEntry struct {
	Name  string
	Score int64
}

// GetTopActivity 按分数降序返回 Top N
func (r *RedisStore) GetTopActivity(ctx context.Context, count int) ([]ActivityEntry, error) {
	results, err := r.client.ZRevRangeWithScores(ctx, "activity:rank", 0, int64(count-1)).Result()
	if err != nil {
		return nil, fmt.Errorf("查询活跃度排名失败: %w", err)
	}
	entries := make([]ActivityEntry, len(results))
	for i, item := range results {
		name, _ := item.Member.(string)
		entries[i] = ActivityEntry{
			Name:  name,
			Score: int64(item.Score),
		}
	}
	return entries, nil
}

func (r *RedisStore) PushMessage(ctx context.Context, item QueueItem) error {
	data, err := json.Marshal(item)
	if err != nil {
		return fmt.Errorf("序列化队列消息失败: %w", err)
	}
	return r.client.LPush(ctx, "msg:queue", string(data)).Err()
}

// PopMessage 阻塞出队，超时返回空 QueueItem。
func (r *RedisStore) PopMessage(ctx context.Context, timeout time.Duration) (QueueItem, error) {
	result, err := r.client.BRPop(ctx, timeout, "msg:queue").Result()
	if err != nil {
		if errors.Is(err, redis.Nil) {
			return QueueItem{}, nil
		}
		return QueueItem{}, fmt.Errorf("消息队列出队失败: %w", err)
	}
	if len(result) < 2 {
		return QueueItem{}, fmt.Errorf("Redis BRPOP 返回数据异常")
	}
	var item QueueItem
	if err := json.Unmarshal([]byte(result[1]), &item); err != nil {
		return QueueItem{}, fmt.Errorf("解析队列消息失败: %w", err)
	}
	return item, nil
}

// GetUserHash 返回 (hash, true) 缓存命中，("", false) 未命中。
func (r *RedisStore) GetUserHash(ctx context.Context, username string) (string, bool, error) {
	val, err := r.client.Get(ctx, "user:hash:"+username).Result()
	if errors.Is(err, redis.Nil) {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("查询用户缓存失败: %w", err)
	}
	return val, true, nil
}

func (r *RedisStore) SetUserHash(ctx context.Context, username, hash string, ttl time.Duration) error {
	return r.client.Set(ctx, "user:hash:"+username, hash, ttl).Err()
}
