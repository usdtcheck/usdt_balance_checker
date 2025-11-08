package core

import (
	"context"
	"sync"

	"usdt-balance-checker/tron"
)

// QueryResult 查询结果
type QueryResult struct {
	Address string
	Balance string
	Status  string // "success", "error"
	Error   string
}

// QueryManager 查询管理器
type QueryManager struct {
	keyManager    *APIKeyManager
	baseURL       string
	results       []QueryResult
	mu            sync.RWMutex
	cancel        context.CancelFunc
	ctx           context.Context
	maxConcurrent int // 最大并发数
}

// NewQueryManager 创建查询管理器（支持多 Key）
func NewQueryManager(keyManager *APIKeyManager, baseURL string) *QueryManager {
	ctx, cancel := context.WithCancel(context.Background())

	return &QueryManager{
		keyManager:    keyManager,
		baseURL:       baseURL,
		results:       make([]QueryResult, 0),
		ctx:           ctx,
		cancel:        cancel,
		maxConcurrent: 1, // 默认1个线程
	}
}

// SetMaxConcurrent 设置最大并发数
func (qm *QueryManager) SetMaxConcurrent(max int) {
	if max < 1 {
		max = 1
	}
	if max > 50 {
		max = 50 // 限制最大并发数
	}
	qm.mu.Lock()
	qm.maxConcurrent = max
	qm.mu.Unlock()
}

// SetRateLimit 设置限流（每秒请求数）- 现在由每个客户端独立管理
func (qm *QueryManager) SetRateLimit(rate int) {
	// 限流由每个 APIClient 独立管理，这里保留接口兼容性
}

// QueryAddresses 批量查询地址余额（支持多线程并发）
func (qm *QueryManager) QueryAddresses(addresses []string, progressCallback func(current, total int)) {
	qm.mu.Lock()
	qm.results = make([]QueryResult, len(addresses))
	// 初始化所有结果为待查询状态，确保地址能正确显示
	for i, addr := range addresses {
		qm.results[i] = QueryResult{
			Address: addr,
			Status:  "pending",
			Balance: "",
			Error:   "",
		}
	}
	maxConcurrent := qm.maxConcurrent
	qm.mu.Unlock()

	// 检查是否有 KEY
	keyCount := qm.keyManager.GetKeyCount()
	if keyCount == 0 {
		// 没有 KEY，无法查询
		for i := range addresses {
			qm.mu.Lock()
			qm.results[i] = QueryResult{
				Address: addresses[i],
				Status:  "error",
				Error:   "没有可用的 API Key",
			}
			qm.mu.Unlock()
		}
		if progressCallback != nil {
			progressCallback(len(addresses), len(addresses))
		}
		return
	}

	// 使用 worker pool 模式实现多线程查询
	// 使用无缓冲 channel，这样可以在取消时立即停止发送新任务
	jobs := make(chan int)
	var wg sync.WaitGroup
	var progressMu sync.Mutex
	completedCount := 0

	// 启动 worker goroutines
	for w := 0; w < maxConcurrent; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range jobs {
				// 检查是否取消
				select {
				case <-qm.ctx.Done():
					qm.mu.Lock()
					qm.results[i] = QueryResult{
						Address: addresses[i],
						Status:  "cancelled",
						Error:   "已取消",
					}
					qm.mu.Unlock()
					// 更新进度
					progressMu.Lock()
					completedCount++
					current := completedCount
					progressMu.Unlock()
					if progressCallback != nil {
						progressCallback(current, len(addresses))
					}
					continue
				default:
				}

				// 获取下一个可用的 API Key（轮询使用）
				apiKey, err := qm.keyManager.GetNextKey()
				if err != nil {
					qm.mu.Lock()
					qm.results[i] = QueryResult{
						Address: addresses[i],
						Status:  "error",
						Error:   "API Key 获取失败: " + err.Error(),
					}
					qm.mu.Unlock()
					// 更新进度
					progressMu.Lock()
					completedCount++
					current := completedCount
					progressMu.Unlock()
					if progressCallback != nil {
						progressCallback(current, len(addresses))
					}
					continue
				}

				// 创建客户端
				client := tron.NewAPIClient(apiKey)
				if qm.baseURL != "" {
					client.SetBaseURL(qm.baseURL)
				}

				// 查询余额（传入 context 以支持取消）
				balance, err := client.QueryBalanceWithContext(qm.ctx, addresses[i])

				// 更新结果
				qm.mu.Lock()
				if err != nil {
					qm.results[i] = QueryResult{
						Address: addresses[i],
						Status:  "error",
						Error:   err.Error(),
					}
				} else {
					qm.results[i] = QueryResult{
						Address: addresses[i],
						Balance: balance,
						Status:  "success",
					}
				}
				qm.mu.Unlock()

				// 更新进度
				progressMu.Lock()
				completedCount++
				current := completedCount
				progressMu.Unlock()
				if progressCallback != nil {
					progressCallback(current, len(addresses))
				}
			}
		}()
	}

	// 发送任务到 jobs channel，并检查是否取消
	go func() {
		defer close(jobs)
		for i := range addresses {
			// 检查是否取消
			select {
			case <-qm.ctx.Done():
				// 取消了，停止发送新任务
				return
			case jobs <- i:
				// 成功发送任务
			}
		}
	}()

	// 等待所有 worker 完成
	wg.Wait()
}

// GetResults 获取查询结果
func (qm *QueryManager) GetResults() []QueryResult {
	qm.mu.RLock()
	defer qm.mu.RUnlock()

	// 返回副本
	result := make([]QueryResult, len(qm.results))
	copy(result, qm.results)
	return result
}

// Cancel 取消查询
func (qm *QueryManager) Cancel() {
	if qm.cancel != nil {
		qm.cancel()
	}
}

// Ctx 返回 context
func (qm *QueryManager) Ctx() context.Context {
	return qm.ctx
}

// GetStats 获取统计信息
func (qm *QueryManager) GetStats() (total, success, failed int) {
	qm.mu.RLock()
	defer qm.mu.RUnlock()

	total = len(qm.results)
	for _, r := range qm.results {
		if r.Status == "success" {
			success++
		} else if r.Status == "error" {
			failed++
		}
	}
	return
}
