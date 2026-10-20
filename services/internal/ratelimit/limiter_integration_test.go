package ratelimit

import (
	"context"
	"errors"
	"github.com/jonluo94/deepseek-harness-cloud/services/internal/webapi"
	"github.com/redis/go-redis/v9"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestRedisLimitsConcurrencyAndFailsClosedAfterReconnect(t *testing.T) {
	path := os.Getenv("HC_TEST_DB_DSN_FILE")
	if path == "" {
		t.Skip("local Redis private configuration required")
	}
	password, err := os.ReadFile(filepath.Join(filepath.Dir(path), "redis-password"))
	if err != nil {
		t.Fatal("test Redis password unavailable")
	}
	options := &redis.Options{Addr: "127.0.0.1:56379", Password: strings.TrimSpace(string(password)), MaxRetries: -1, DialTimeout: 200 * time.Millisecond, ReadTimeout: 200 * time.Millisecond, WriteTimeout: 200 * time.Millisecond, ContextTimeoutEnabled: true}
	l := New(options)
	defer l.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	key := "test:" + webapi.ID()
	code := func(err error) string {
		var p *webapi.Problem
		if errors.As(err, &p) {
			return p.Code
		}
		return ""
	}
	if err := l.Allow(ctx, key, 5); code(err) != "DEPENDENCY_UNAVAILABLE" {
		t.Fatal("initial cooldown missing")
	}
	if err := l.Ready(ctx); code(err) != "DEPENDENCY_UNAVAILABLE" {
		t.Fatal("preflight bypassed initial cooldown")
	}
	l.coldUntil.Store(0)
	for range 10 {
		if err := l.Ready(ctx); err != nil {
			t.Fatal(err)
		}
	}
	var wg sync.WaitGroup
	errs := make(chan error, 20)
	for range 20 {
		wg.Add(1)
		go func() { defer wg.Done(); errs <- l.Allow(ctx, key, 5) }()
	}
	wg.Wait()
	close(errs)
	accepted, denied := 0, 0
	for err := range errs {
		if err == nil {
			accepted++
		} else if code(err) == "RATE_LIMITED" {
			denied++
		} else {
			t.Fatal(err)
		}
	}
	if accepted != 5 || denied != 15 {
		t.Fatalf("accepted=%d denied=%d", accepted, denied)
	}
	id, err := l.client.ClientID(ctx).Result()
	if err != nil {
		t.Fatal(err)
	}
	other := redis.NewClient(options)
	defer other.Close()
	if err := other.ClientKillByFilter(ctx, "ID", strconv.FormatInt(id, 10)).Err(); err != nil {
		t.Fatal(err)
	}
	_ = l.Allow(ctx, key, 5)
	if err := l.Allow(ctx, key, 5); code(err) != "DEPENDENCY_UNAVAILABLE" {
		t.Fatal("reconnected limiter bypassed cooldown")
	}
	if err := l.Ready(ctx); code(err) != "DEPENDENCY_UNAVAILABLE" {
		t.Fatal("preflight bypassed reconnect cooldown")
	}
}

func TestIdleLimiterKeepsItsConnectionWithoutRestartingRecoveryWindow(t *testing.T) {
	path := os.Getenv("HC_TEST_DB_DSN_FILE")
	if path == "" {
		t.Skip("local Redis private configuration required")
	}
	password, err := os.ReadFile(filepath.Join(filepath.Dir(path), "redis-password"))
	if err != nil {
		t.Fatal("test Redis password unavailable")
	}
	options := &redis.Options{Addr: "127.0.0.1:56379", Password: strings.TrimSpace(string(password)), MaxRetries: -1, DialTimeout: 200 * time.Millisecond, ReadTimeout: 200 * time.Millisecond, WriteTimeout: 200 * time.Millisecond, ContextTimeoutEnabled: true, ConnMaxIdleTime: time.Millisecond}
	l := New(options)
	defer l.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	id, err := l.client.ClientID(ctx).Result()
	if err != nil {
		t.Fatal("controlled Redis unavailable")
	}
	l.coldUntil.Store(0)
	time.Sleep(20 * time.Millisecond)
	if err := l.Allow(ctx, "test:idle:"+webapi.ID(), 5); err != nil {
		t.Fatal("ordinary idle interval restarted Redis recovery window", err)
	}
	after, err := l.client.ClientID(ctx).Result()
	if err != nil || id != after {
		t.Fatal("idle limiter replaced its healthy connection")
	}
}
