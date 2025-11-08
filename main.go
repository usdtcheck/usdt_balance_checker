package main

import (
	"flag"
	"usdt-balance-checker/view"

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
		view.RunCLI(*inputFile, *outputFile, *apiKey, *nodeURL, *rateLimit)
	} else {
		// GUI 模式
		myApp := app.NewWithID("usdt.balance.checker")

		view.ShowMainWindow(myApp)
		myApp.Run()
	}
}
