package core

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

const (
	// MaxQueriesPerKey 每个 API Key 的最大查询次数（10万次）
	MaxQueriesPerKey = 100000
	// StatsFileName API Key 使用记录文件名
	StatsFileName = "apikey_stats.json"
)

// KeyStatsFile 用于持久化的 Key 统计文件结构
type KeyStatsFile struct {
	Keys map[string]int `json:"keys"` // Key -> 已使用次数
}

// APIKeyManager API Key 管理器
type APIKeyManager struct {
	keys      []APIKeyInfo
	current   int
	mu        sync.RWMutex
	totalUsed int // 总使用次数
}

// APIKeyInfo API Key 信息
type APIKeyInfo struct {
	Key      string
	Used     int  // 已使用次数
	MaxLimit int  // 最大限额
	Enabled  bool // 是否启用
}

// NewAPIKeyManager 创建 API Key 管理器
func NewAPIKeyManager() *APIKeyManager {
	return &APIKeyManager{
		keys:    make([]APIKeyInfo, 0),
		current: 0,
	}
}

// LoadKeysFromFile 从文件加载 API Keys（每行一个）
func (m *APIKeyManager) LoadKeysFromFile(filepath string) error {
	file, err := os.Open(filepath)
	if err != nil {
		return errors.New("打开文件失败")
	}
	defer file.Close()

	keys := make([]APIKeyInfo, 0)
	seen := make(map[string]bool)

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}

		// 去重
		if seen[line] {
			continue
		}
		seen[line] = true

		keys = append(keys, APIKeyInfo{
			Key:      line,
			Used:     0,
			MaxLimit: MaxQueriesPerKey,
			Enabled:  true,
		})
	}

	if err := scanner.Err(); err != nil {
		return errors.New("读取文件失败")
	}

	if len(keys) == 0 {
		return errors.New("文件中没有找到有效的 API Key")
	}

	m.mu.Lock()
	// 加载之前保存的使用记录
	stats, err := m.loadStats()
	if err == nil {
		// 合并使用记录到新加载的 Key
		for i := range keys {
			if used, exists := stats.Keys[keys[i].Key]; exists {
				keys[i].Used = used
			}
		}
	}

	m.keys = keys
	m.current = 0
	m.mu.Unlock()

	// 保存更新后的记录
	m.saveStats()

	return nil
}

// RemoveKey 删除指定的 Key
func (m *APIKeyManager) RemoveKey(keyToRemove string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	found := false
	newKeys := make([]APIKeyInfo, 0, len(m.keys))
	for _, keyInfo := range m.keys {
		if keyInfo.Key != keyToRemove {
			newKeys = append(newKeys, keyInfo)
		} else {
			found = true
		}
	}

	if !found {
		return errors.New("未找到指定的 API Key")
	}

	m.keys = newKeys
	// 如果当前索引超出范围，重置为0
	if m.current >= len(m.keys) {
		m.current = 0
	}

	// 保存更新后的记录
	m.saveStats()

	return nil
}

// RemoveKeysByUsageThreshold 删除使用次数达到或超过阈值的 Key，返回删除的数量
func (m *APIKeyManager) RemoveKeysByUsageThreshold(threshold int) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if len(m.keys) == 0 {
		return 0, nil
	}

	newKeys := make([]APIKeyInfo, 0, len(m.keys))
	removedCount := 0

	for _, keyInfo := range m.keys {
		if keyInfo.Used >= threshold {
			removedCount++
		} else {
			newKeys = append(newKeys, keyInfo)
		}
	}

	m.keys = newKeys
	// 如果当前索引超出范围，重置为0
	if m.current >= len(m.keys) {
		m.current = 0
	}

	// 保存更新后的记录
	m.saveStats()

	return removedCount, nil
}

// GetNextKey 获取下一个可用的 API Key（循环切换）
// 如果只有一个Key，则一直用这个Key；如果有多个Key，则轮询使用
func (m *APIKeyManager) GetNextKey() (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if len(m.keys) == 0 {
		return "", errors.New("没有可用的 API Key")
	}

	// 如果只有一个Key，直接使用这个Key
	if len(m.keys) == 1 {
		keyInfo := &m.keys[0]
		if keyInfo.Enabled && keyInfo.Used < keyInfo.MaxLimit {
			keyInfo.Used++
			m.totalUsed++
			key := keyInfo.Key

			// 异步保存使用记录（不阻塞查询）
			go func() {
				if err := m.saveStats(); err != nil {
					// 静默失败，不影响查询流程
					_ = err
				}
			}()

			return key, nil
		}
		return "", errors.New("API Key 已达到使用上限")
	}

	// 多个Key时，轮询使用
	startIndex := m.current
	maxAttempts := len(m.keys)

	for i := 0; i < maxAttempts; i++ {
		keyInfo := &m.keys[m.current]

		if keyInfo.Enabled && keyInfo.Used < keyInfo.MaxLimit {
			keyInfo.Used++
			m.totalUsed++
			key := keyInfo.Key

			// 移动到下一个 Key（循环轮询）
			m.current = (m.current + 1) % len(m.keys)

			// 异步保存使用记录（不阻塞查询）
			go func() {
				if err := m.saveStats(); err != nil {
					// 静默失败，不影响查询流程
					_ = err
				}
			}()

			return key, nil
		}

		// 如果当前 Key 用完，移动到下一个
		m.current = (m.current + 1) % len(m.keys)

		// 如果回到起点，说明所有 Key 都用完了
		if m.current == startIndex {
			break
		}
	}

	return "", errors.New("所有 API Key 都已达到使用上限")
}

// GetKeyStatus 获取所有 Key 的状态信息
func (m *APIKeyManager) GetKeyStatus() []APIKeyStatus {
	m.mu.RLock()
	defer m.mu.RUnlock()

	status := make([]APIKeyStatus, len(m.keys))
	for i, keyInfo := range m.keys {
		status[i] = APIKeyStatus{
			Key:         keyInfo.Key,
			Used:        keyInfo.Used,
			Remaining:   keyInfo.MaxLimit - keyInfo.Used,
			MaxLimit:    keyInfo.MaxLimit,
			Enabled:     keyInfo.Enabled,
			DisplayName: fmt.Sprintf("Key %d", i+1),
		}
	}
	return status
}

// APIKeyStatus Key 状态信息（用于界面显示）
type APIKeyStatus struct {
	Key         string
	Used        int
	Remaining   int
	MaxLimit    int
	Enabled     bool
	DisplayName string // 显示名称（如 "Key 1", "Key 2"）
}

// GetTotalUsed 获取总使用次数
func (m *APIKeyManager) GetTotalUsed() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.totalUsed
}

// GetKeyCount 获取 Key 总数
func (m *APIKeyManager) GetKeyCount() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return len(m.keys)
}

// GetStatsFilePath 获取统计文件路径（用于调试）
func (m *APIKeyManager) GetStatsFilePath() string {
	statsPath, err := getStatsPath()
	if err != nil {
		return "无法获取路径: " + err.Error()
	}
	return statsPath
}

// getStatsPath 获取统计文件的实际保存路径
func getStatsPath() (string, error) {
	exePath, err := os.Executable()
	if err != nil {
		return "", err
	}
	exeDir := filepath.Dir(exePath)

	// 检查是否在临时目录（go run 模式）
	// 如果在临时目录，使用当前工作目录
	if strings.Contains(exeDir, "Temp") || strings.Contains(exeDir, "go-build") {
		// 使用当前工作目录
		workDir, err := os.Getwd()
		if err != nil {
			return filepath.Join(exeDir, StatsFileName), nil
		}
		return filepath.Join(workDir, StatsFileName), nil
	}

	// 否则使用可执行文件所在目录
	return filepath.Join(exeDir, StatsFileName), nil
}

// LoadStatsIfExists 如果存在统计文件，加载之前的使用记录（用于程序启动时）
func (m *APIKeyManager) LoadStatsIfExists() error {
	stats, err := m.loadStats()
	if err != nil {
		return err
	}

	// 如果已经有加载的 Key，合并使用记录
	m.mu.Lock()
	for i := range m.keys {
		if used, exists := stats.Keys[m.keys[i].Key]; exists {
			m.keys[i].Used = used
		}
	}
	m.mu.Unlock()

	return nil
}

// loadStats 从文件加载 Key 使用统计
func (m *APIKeyManager) loadStats() (*KeyStatsFile, error) {
	// 获取统计文件路径
	statsPath, err := getStatsPath()
	if err != nil {
		return nil, err
	}

	file, err := os.Open(statsPath)
	if err != nil {
		return &KeyStatsFile{Keys: make(map[string]int)}, nil // 文件不存在时返回空记录
	}
	defer file.Close()

	var stats KeyStatsFile
	decoder := json.NewDecoder(file)
	if err := decoder.Decode(&stats); err != nil {
		return &KeyStatsFile{Keys: make(map[string]int)}, nil // 解析失败时返回空记录
	}

	if stats.Keys == nil {
		stats.Keys = make(map[string]int)
	}

	return &stats, nil
}

// saveStats 保存 Key 使用统计到文件
func (m *APIKeyManager) saveStats() error {
	m.mu.RLock()
	stats := KeyStatsFile{
		Keys: make(map[string]int),
	}
	for _, keyInfo := range m.keys {
		stats.Keys[keyInfo.Key] = keyInfo.Used
	}
	m.mu.RUnlock()

	// 获取统计文件路径
	statsPath, err := getStatsPath()
	if err != nil {
		return err
	}

	// 创建或覆盖文件
	file, err := os.Create(statsPath)
	if err != nil {
		return errors.New("创建统计文件失败")
	}
	defer file.Close()

	encoder := json.NewEncoder(file)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(stats); err != nil {
		return errors.New("保存统计文件失败")
	}

	return nil
}
