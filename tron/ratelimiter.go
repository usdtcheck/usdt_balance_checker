package tron

import (
	"sync"
	"time"
)

// RateLimiter 限流器
type RateLimiter struct {
	rate      int           // 每秒允许的请求数
	interval  time.Duration // 时间窗口
	tokens    int           // 当前令牌数
	maxTokens int           // 最大令牌数
	lastRefill time.Time    // 上次补充令牌的时间
	mu        sync.Mutex    // 互斥锁
}

// NewRateLimiter 创建新的限流器
func NewRateLimiter(rate int, interval time.Duration) *RateLimiter {
	return &RateLimiter{
		rate:       rate,
		interval:   interval,
		tokens:     rate,
		maxTokens:  rate,
		lastRefill: time.Now(),
	}
}

// Wait 等待直到可以获得令牌
func (rl *RateLimiter) Wait() {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	// 补充令牌
	now := time.Now()
	elapsed := now.Sub(rl.lastRefill)
	if elapsed > 0 {
		tokensToAdd := int(elapsed / rl.interval) * rl.rate
		if tokensToAdd > 0 {
			rl.tokens = rl.tokens + tokensToAdd
			if rl.tokens > rl.maxTokens {
				rl.tokens = rl.maxTokens
			}
			rl.lastRefill = now
		}
	}

	// 如果令牌不够，等待
	if rl.tokens <= 0 {
		waitTime := rl.interval / time.Duration(rl.rate)
		rl.mu.Unlock()
		time.Sleep(waitTime)
		rl.mu.Lock()
		// 重新补充
		now = time.Now()
		elapsed = now.Sub(rl.lastRefill)
		if elapsed > 0 {
			tokensToAdd := int(elapsed / rl.interval) * rl.rate
			rl.tokens = tokensToAdd
			if rl.tokens > rl.maxTokens {
				rl.tokens = rl.maxTokens
			}
			rl.lastRefill = now
		}
	}

	// 消耗一个令牌
	rl.tokens--
}
