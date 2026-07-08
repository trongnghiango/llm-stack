package main

import (
	"context"
	"log"
	"os"
	"time"

	"github.com/redis/go-redis/v9"
)

// RedisStore quản lý kết nối và tương tác với Redis để đồng bộ hóa trạng thái session.
type RedisStore struct {
	client *redis.Client
	ctx    context.Context
	active bool
}

// NewRedisStore khởi tạo kết nối tới Redis (sử dụng biến môi trường REDIS_URL hoặc mặc định localhost:6379).
func NewRedisStore() *RedisStore {
	addr := os.Getenv("REDIS_URL")
	if addr == "" {
		addr = "127.0.0.1:6379" // Mặc định local
	}

	opt, err := redis.ParseURL(addr)
	var client *redis.Client
	if err == nil {
		client = redis.NewClient(opt)
	} else {
		client = redis.NewClient(&redis.Options{
			Addr: addr,
		})
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	err = client.Ping(ctx).Err()
	if err != nil {
		log.Printf("⚠️ Cảnh báo: Không thể kết nối Redis tại %s (%v). Sử dụng bộ nhớ RAM cục bộ để lưu session.", addr, err)
		return &RedisStore{active: false}
	}

	log.Printf("🔌 Đã kết nối thành công tới Redis tại %s. Kích hoạt chế độ lưu trữ phân tán cho Session & Quota!", addr)
	return &RedisStore{
		client: client,
		ctx:    context.Background(),
		active: true,
	}
}
