package ratelimit

import (
	"context"
	"github.com/jonluo94/deepseek-harness-cloud/services/internal/webapi"
	"github.com/redis/go-redis/v9"
	"sync/atomic"
	"time"
)

type Limiter struct {
	client    *redis.Client
	coldUntil atomic.Int64
}

func New(options *redis.Options) *Limiter {
	l := &Limiter{}
	copy := *options
	copy.PoolSize = 1
	copy.MinIdleConns = 0
	// Keep the single healthy connection across quiet periods. Reaping it after
	// the client's default 30-minute idle timeout would restart the recovery
	// window for the next user even though Redis and its counters never changed.
	copy.ConnMaxIdleTime = -1
	copy.OnConnect = func(context.Context, *redis.Conn) error {
		l.coldUntil.Store(time.Now().Add(120 * time.Second).UnixNano())
		return nil
	}
	l.client = redis.NewClient(&copy)
	l.coldUntil.Store(time.Now().Add(120 * time.Second).UnixNano())
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	_ = l.client.Ping(ctx).Err()
	return l
}
func (l *Limiter) Close() error { return l.client.Close() }

var counter = redis.NewScript(`local now=redis.call('TIME'); local bucket=math.floor(tonumber(now[1])/60); local key=KEYS[1]..':'..bucket; local count=redis.call('INCR',key); if count==1 then redis.call('EXPIRE',key,120) end; return count`)

func (l *Limiter) Allow(ctx context.Context, key string, limit int) error {
	ctx, cancel := context.WithTimeout(ctx, 200*time.Millisecond)
	defer cancel()
	if err := l.Ready(ctx); err != nil {
		return err
	}
	count, err := counter.Run(ctx, l.client, []string{"hc:rate:" + key}).Int64()
	if err != nil {
		return webapi.Fail(503, "DEPENDENCY_UNAVAILABLE", "无法确认请求流量限制，请稍后重试")
	}
	if count > int64(limit) {
		return webapi.Fail(429, "RATE_LIMITED", "操作过于频繁，请稍后重试")
	}
	return nil
}

// Readiness checks the same connection/cooldown used by costly acceptance,
// without consuming a rate bucket just to display the preflight screen.
func (l *Limiter) Ready(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, 200*time.Millisecond)
	defer cancel()
	// Establish/check the connection before the cooldown test: reconnecting resets
	// the local safety window even if the previous Redis counter was lost.
	if err := l.client.Ping(ctx).Err(); err != nil {
		return webapi.Fail(503, "DEPENDENCY_UNAVAILABLE", "无法确认请求流量限制，请稍后重试")
	}
	if time.Now().UnixNano() < l.coldUntil.Load() {
		return webapi.Fail(503, "DEPENDENCY_UNAVAILABLE", "限流服务恢复中，请稍后重试")
	}
	return nil
}
