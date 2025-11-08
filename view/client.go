package view

import (
	"os"
	"strings"
	"usdt-balance-checker/core"

	"github.com/ethereum/go-ethereum/log"
)

func RunCLI(inputFile, outputFile, apiKey, nodeURL string, rateLimit int) {
	// CLI 实现（基础版本）
	// 可以通过命令行参数指定输入文件和输出文件
	// 例如: ./usdt-balance-checker -cli -input addresses.txt -output results.csv -api-key YOUR_KEY

	if inputFile == "" {
		os.Exit(1)
	}

	// 加载地址
	addresses, err := core.LoadAddressesFromFile(inputFile)
	if err != nil {
		log.Error("错误: 加载地址失败: %v\n", err)
		os.Exit(1)
	}

	log.Info("已加载 %d 个地址，开始查询...\n", len(addresses))

	// 创建 API Key Manager（CLI 模式支持单个 Key）
	keyManager := core.NewAPIKeyManager()
	if apiKey != "" {
		// 创建临时文件添加单个 API Key
		tempKeyFile := "temp_cli_key.txt"
		if err := os.WriteFile(tempKeyFile, []byte(apiKey), 0644); err == nil {
			keyManager.LoadKeysFromFile(tempKeyFile)
			os.Remove(tempKeyFile) // 清理临时文件
		} else {
			log.Info("警告: 无法创建临时 Key 文件，查询可能被限流")
		}
	} else {
		log.Info("警告: 未提供 API Key，查询可能被限流")
	}

	// 创建查询管理器
	qm := core.NewQueryManager(keyManager, nodeURL)
	qm.SetRateLimit(rateLimit)

	// 查询
	qm.QueryAddresses(addresses, func(cur, total int) {
		log.Info("\r进度: %d / %d (%.1f%%)", cur, total, float64(cur)/float64(total)*100)
	})
	log.Info("\n") // 换行

	// 获取结果
	results := qm.GetResults()
	total, success, failed := qm.GetStats()

	log.Info("查询完成! 总计: %d, 成功: %d, 失败: %d\n", total, success, failed)

	// 导出结果
	if strings.HasSuffix(strings.ToLower(outputFile), ".xlsx") {
		err = core.ExportToExcel(results, outputFile)
	} else {
		err = core.ExportToCSV(results, outputFile)
	}

	if err != nil {
		log.Error("错误: 导出失败: %v\n", err)
		os.Exit(1)
	}

	log.Info("结果已导出到: %s\n", outputFile)
}
