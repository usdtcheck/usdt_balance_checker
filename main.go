package main

import (
	"flag"
	"fmt"
	"os"
	"strings"

	"usdt-balance-checker/core"
	"usdt-balance-checker/gui"

	"fyne.io/fyne/v2/app"
)

func main() {
	cliMode := flag.Bool("cli", false, "运行在 CLI 模式")
	inputFile := flag.String("input", "", "输入文件路径 (TXT/CSV)")
	outputFile := flag.String("output", "results.csv", "输出文件路径 (CSV/Excel)")
	apiKey := flag.String("api-key", "", "TronGrid API Key (可选)")
	nodeURL := flag.String("node-url", "", "自定义 TRON 节点 URL (可选)")
	rateLimit := flag.Int("rate", 12, "每秒请求数 (默认: 12)")

	flag.Parse()

	if *cliMode {
		// CLI 模式
		runCLI(*inputFile, *outputFile, *apiKey, *nodeURL, *rateLimit)
	} else {
		// GUI 模式
		myApp := app.NewWithID("usdt.balance.checker")

		gui.ShowMainWindow(myApp)
		myApp.Run()
	}
}

func runCLI(inputFile, outputFile, apiKey, nodeURL string, rateLimit int) {
	// CLI 实现（基础版本）
	// 可以通过命令行参数指定输入文件和输出文件
	// 例如: ./usdt-balance-checker -cli -input addresses.txt -output results.csv -api-key YOUR_KEY

	if inputFile == "" {
		fmt.Println("错误: 请指定输入文件路径")
		fmt.Println("用法: ./usdt-balance-checker -cli -input addresses.txt -output results.csv")
		os.Exit(1)
	}

	// 加载地址
	addresses, err := core.LoadAddressesFromFile(inputFile)
	if err != nil {
		fmt.Printf("错误: 加载地址失败: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("已加载 %d 个地址，开始查询...\n", len(addresses))

	// 创建 API Key Manager（CLI 模式支持单个 Key）
	keyManager := core.NewAPIKeyManager()
	if apiKey != "" {
		// 创建临时文件添加单个 API Key
		tempKeyFile := "temp_cli_key.txt"
		if err := os.WriteFile(tempKeyFile, []byte(apiKey), 0644); err == nil {
			keyManager.LoadKeysFromFile(tempKeyFile)
			os.Remove(tempKeyFile) // 清理临时文件
		} else {
			fmt.Println("警告: 无法创建临时 Key 文件，查询可能被限流")
		}
	} else {
		fmt.Println("警告: 未提供 API Key，查询可能被限流")
	}

	// 创建查询管理器
	qm := core.NewQueryManager(keyManager, nodeURL)
	qm.SetRateLimit(rateLimit)

	// 查询
	qm.QueryAddresses(addresses, func(cur, total int) {
		fmt.Printf("\r进度: %d / %d (%.1f%%)", cur, total, float64(cur)/float64(total)*100)
	})
	fmt.Println() // 换行

	// 获取结果
	results := qm.GetResults()
	total, success, failed := qm.GetStats()

	fmt.Printf("查询完成! 总计: %d, 成功: %d, 失败: %d\n", total, success, failed)

	// 导出结果
	if strings.HasSuffix(strings.ToLower(outputFile), ".xlsx") {
		err = core.ExportToExcel(results, outputFile)
	} else {
		err = core.ExportToCSV(results, outputFile)
	}

	if err != nil {
		fmt.Printf("错误: 导出失败: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("结果已导出到: %s\n", outputFile)
}
