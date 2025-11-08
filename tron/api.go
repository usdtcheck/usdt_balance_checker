package tron

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"strings"
	"time"
)

const (
	// USDT 合约地址
	USDTContractAddress = "TR7NHqjeKQxGTCi8q8ZY4pL8otSzgjLj6t"
	// TronGrid API 地址
	TronGridAPI = "https://api.trongrid.io/wallet/triggerconstantcontract"
	// balanceOf 函数签名（完整函数签名字符串）
	BalanceOfSelector = "balanceOf(address)"
)

// APIClient TronGrid API 客户端
type APIClient struct {
	APIKey      string
	BaseURL     string
	HTTPClient  *http.Client
	RateLimiter *RateLimiter
}

// NewAPIClient 创建新的 API 客户端
func NewAPIClient(apiKey string) *APIClient {
	return &APIClient{
		APIKey:  apiKey,
		BaseURL: TronGridAPI,
		HTTPClient: &http.Client{
			Timeout: 30 * time.Second,
		},
		RateLimiter: NewRateLimiter(12, time.Second), // 默认每秒12次
	}
}

// SetBaseURL 设置自定义 TRON 节点地址
func (c *APIClient) SetBaseURL(url string) {
	c.BaseURL = url
}

// TriggerConstantContractRequest 请求结构
type TriggerConstantContractRequest struct {
	OwnerAddress     string `json:"owner_address"`
	ContractAddress  string `json:"contract_address"`
	FunctionSelector string `json:"function_selector"`
	Parameter        string `json:"parameter"`
	Visible          bool   `json:"visible"`
}

// TriggerConstantContractResponse 响应结构（已移除，改为内联结构体解析）

// QueryBalance 查询 USDT 余额（兼容旧接口）
func (c *APIClient) QueryBalance(address string) (string, error) {
	return c.QueryBalanceWithContext(context.Background(), address)
}

// QueryBalanceWithContext 查询 USDT 余额（支持 context 取消）
func (c *APIClient) QueryBalanceWithContext(ctx context.Context, address string) (string, error) {
	// 等待限流
	c.RateLimiter.Wait()

	// 转换地址为参数格式（使用20字节地址主体）
	param, err := AddressToParameter(address)
	if err != nil {
		return "", errors.New("地址转换失败: %v")
	}

	// 构建请求
	// 根据实际测试，使用 Base58 格式的 owner_address 配合 visible=true
	// parameter 使用20字节地址主体的 ABI 编码（跳过版本字节）
	reqBody := TriggerConstantContractRequest{
		OwnerAddress:     address, // Base58 格式
		ContractAddress:  USDTContractAddress,
		FunctionSelector: BalanceOfSelector, // "balanceOf(address)"
		Parameter:        param,             // ABI 编码（20字节地址主体，64个hex字符）
		Visible:          true,              // true 表示地址使用 Base58 格式
	}

	jsonData, err := json.Marshal(reqBody)
	if err != nil {
		return "", errors.New("请求序列化失败: %v")
	}

	// 创建 HTTP 请求（使用 context 支持取消）
	req, err := http.NewRequestWithContext(ctx, "POST", c.BaseURL, bytes.NewBuffer(jsonData))
	if err != nil {
		return "", errors.New("创建请求失败: %v")
	}

	req.Header.Set("Content-Type", "application/json")
	if c.APIKey != "" {
		req.Header.Set("TRON-PRO-API-KEY", c.APIKey)
	}
	// 注意：根据 TronGrid 文档，主网请求强烈建议使用 API Key
	// 没有 API Key 时请求可能被拒绝或严格限流

	// 发送请求（带重试机制）
	var resp *http.Response
	var lastErr error
	maxRetries := 3
	for i := 0; i < maxRetries; i++ {
		// 检查 context 是否已取消
		if req.Context().Err() != nil {
			return "", errors.New("请求已取消")
		}
		resp, lastErr = c.HTTPClient.Do(req)
		if lastErr == nil && resp.StatusCode == http.StatusOK {
			break
		}

		if resp != nil && resp.StatusCode == http.StatusTooManyRequests {
			// 429 错误，延迟后重试
			waitTime := time.Duration(i+1) * 2 * time.Second
			time.Sleep(waitTime)
			if resp.Body != nil {
				resp.Body.Close()
			}
			continue
		}

		if lastErr != nil {
			if resp != nil && resp.Body != nil {
				resp.Body.Close()
			}
			if i < maxRetries-1 {
				time.Sleep(time.Duration(i+1) * time.Second)
				continue
			}
		}
	}

	if lastErr != nil {
		return "", errors.New("请求失败: %v")
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		_, _ = io.ReadAll(resp.Body)
		return "", errors.New("API 返回错误 (HTTP %d): %s")
	}

	// 读取响应体
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", errors.New("读取响应失败: %v")
	}

	// 解析响应（按照 test.go 的方法）
	var apiResp struct {
		ConstantResult []string `json:"constant_result"`
		Result         struct {
			Result  bool   `json:"result"`
			Code    string `json:"code,omitempty"`
			Message string `json:"message,omitempty"`
		} `json:"result"`
		// 某些错误情况下，错误信息可能在顶层
		Error            string `json:"Error,omitempty"`
		ErrorDescription string `json:"Error Description,omitempty"`
	}

	if err := json.Unmarshal(body, &apiResp); err != nil {
		return "", errors.New("解析响应失败: %v, 响应内容: %s")
	}

	// 检查顶层错误（某些 API 错误可能在这里）
	if apiResp.Error != "" {
		desc := apiResp.ErrorDescription
		if desc == "" {
			desc = apiResp.Error
		}
		return "", errors.New("API 错误: %s (完整响应: %s)")
	}

	// 检查结果
	if !apiResp.Result.Result {
		errorMsg := apiResp.Result.Message
		if errorMsg == "" {
			errorMsg = apiResp.Result.Code
		}
		if errorMsg == "" {
			errorMsg = "未知错误"
		}
		return "", errors.New("查询失败: result=false, code=%s, 完整响应: %s")
	}

	// 获取 constant_result（可能在 result 下，也可能在顶层）
	var constantResults []string
	if len(apiResp.ConstantResult) > 0 {
		constantResults = apiResp.ConstantResult
	} else {
		return "", errors.New("查询失败: 响应中没有 constant_result (完整响应: %s)")
	}

	// 解析余额（hex 转 decimal）
	balanceHex := constantResults[0]

	// 处理空字符串的情况
	balanceHex = strings.TrimSpace(balanceHex)
	if balanceHex == "" {
		balanceHex = "0"
	}

	// 解析余额（按照 test.go 的方法：直接使用 hex 字符串，不 trim 前导零）
	n := new(big.Int)
	if _, ok := n.SetString(balanceHex, 16); !ok {
		return "", errors.New("无法解析hex余额: %s")
	}

	// 格式化小数（按照 test.go 的方法）
	balance := formatDecimals(n, 6)
	return balance, nil
}

// formatDecimals 将大整数格式化为带小数点的字符串（按照 test.go 的方法）
func formatDecimals(n *big.Int, decimals int) string {
	if decimals == 0 {
		return n.String()
	}
	tenPow := new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(decimals)), nil)
	intPart := new(big.Int).Div(n, tenPow)
	fracPart := new(big.Int).Mod(n, tenPow)
	fracStr := fmt.Sprintf("%0*d", decimals, fracPart) // 左补0
	fracStr = strings.TrimRight(fracStr, "0")
	if fracStr == "" {
		return intPart.String()
	}
	return intPart.String() + "." + fracStr
}
