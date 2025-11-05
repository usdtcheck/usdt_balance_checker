package gui

import (
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"time"
	"usdt-balance-checker/resource"

	"usdt-balance-checker/core"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/widget"
)

var (
	queryManager        *core.QueryManager
	keyManager          *core.APIKeyManager
	isQuerying          bool
	isPaused            bool // 是否处于暂停状态
	queryCancel         func()
	addressList         []string
	currentQueryAddrs   []string           // 当前正在查询的完整地址列表
	resultData          []core.QueryResult // 所有原始数据
	filteredData        []core.QueryResult // 筛选后的数据
	displayData         []core.QueryResult // 当前页显示的数据
	currentPage         int                // 当前页码（从1开始）
	pageSize            int                // 每页显示数量
	totalPages          int                // 总页数
	filterMode          string             // 筛选模式："all", "withBalance", "address"
	filterText          string             // 筛选文本（地址搜索）
	pausedAddresses     []string           // 暂停时剩余的地址
	pausedStartIndex    int                // 暂停时已完成的索引
	pausedTotalProgress int                // 暂停时的总进度（用于累计显示）
)

// ShowMainWindow 显示主窗口
func ShowMainWindow(a fyne.App) {
	w := a.NewWindow("USDT 余额查询工具")

	// 加载logo.png文件
	logoFile := resource.ReadResourceFile("logo.png")

	// 创建一个静态资源来存储logo
	logoResource := fyne.NewStaticResource("logo", logoFile)

	// 设置窗口的图标
	w.SetIcon(logoResource)

	w.Resize(fyne.NewSize(1200, 700)) // 增大窗口尺寸，提供更好的显示空间
	w.CenterOnScreen()

	// 初始化 Key Manager
	keyManager = core.NewAPIKeyManager()
	// 尝试加载之前保存的使用记录（如果之前导入过 Key）
	statsPath := keyManager.GetStatsFilePath()
	if err := keyManager.LoadStatsIfExists(); err != nil {
		// 加载失败不影响使用，只是不显示之前的记录
		fmt.Printf("统计文件位置: %s\n", statsPath)
		fmt.Printf("加载统计记录失败（首次运行正常）: %v\n", err)
	} else {
		fmt.Printf("统计文件位置: %s\n", statsPath)
		fmt.Println("已加载历史使用记录")
	}

	// 使用 sync 保护的状态变量
	var mu sync.Mutex
	var lastProgress struct {
		current, total int
		stats          struct {
			total, success, failed int
		}
		results []core.QueryResult
		done    bool
	}

	// API Key 管理区域
	apiKeyStatusLabel := widget.NewLabel("未加载 API Key")
	apiKeyStatusLabel.Wrapping = fyne.TextWrapWord

	// Key 状态表格（先定义，后面会引用）
	keyStatusTable := widget.NewTable(
		func() (int, int) {
			return keyManager.GetKeyCount(), 4
		},
		func() fyne.CanvasObject {
			return widget.NewLabel("")
		},
		func(id widget.TableCellID, obj fyne.CanvasObject) {
			label := obj.(*widget.Label)
			status := keyManager.GetKeyStatus()
			if id.Row >= len(status) {
				return
			}
			keyStatus := status[id.Row]

			switch id.Col {
			case 0:
				label.SetText(keyStatus.DisplayName)
			case 1:
				label.SetText(fmt.Sprintf("%d / %d", keyStatus.Used, keyStatus.MaxLimit))
			case 2:
				label.SetText(fmt.Sprintf("%d", keyStatus.Remaining))
			case 3:
				if keyStatus.Enabled && keyStatus.Remaining > 0 {
					label.SetText("可用")
					label.Importance = widget.SuccessImportance
				} else {
					label.SetText("已用完")
					label.Importance = widget.DangerImportance
				}
			}
		})

	keyStatusTable.SetColumnWidth(0, 80)  // Key 名称
	keyStatusTable.SetColumnWidth(1, 120) // 已用/总额
	keyStatusTable.SetColumnWidth(2, 100) // 剩余
	keyStatusTable.SetColumnWidth(3, 80)  // 状态

	// Key 状态表头
	keyStatusHeader := container.NewGridWithColumns(4,
		widget.NewLabelWithStyle("Key", fyne.TextAlignCenter, fyne.TextStyle{Bold: true}),
		widget.NewLabelWithStyle("已用/总额", fyne.TextAlignCenter, fyne.TextStyle{Bold: true}),
		widget.NewLabelWithStyle("剩余", fyne.TextAlignCenter, fyne.TextStyle{Bold: true}),
		widget.NewLabelWithStyle("状态", fyne.TextAlignCenter, fyne.TextStyle{Bold: true}),
	)

	// 更新 Key 状态表格的辅助函数
	updateKeyStatusTable := func(table *widget.Table, manager *core.APIKeyManager) {
		// 强制刷新表格（重新计算行数并刷新所有单元格）
		fyne.Do(func() {
			table.Refresh()
			// 确保表格大小更新
			table.SetColumnWidth(0, 80)
			table.SetColumnWidth(1, 120)
			table.SetColumnWidth(2, 100)
			table.SetColumnWidth(3, 80)
		})
	}

	// API Key 导入按钮
	importKeyBtn := widget.NewButton("📁 导入 API Key", func() {
		dialog.ShowFileOpen(func(reader fyne.URIReadCloser, err error) {
			if err != nil {
				dialog.ShowError(err, w)
				return
			}
			if reader == nil {
				return
			}
			defer reader.Close()

			if err := keyManager.LoadKeysFromFile(reader.URI().Path()); err != nil {
				dialog.ShowError(err, w)
				return
			}

			keyCount := keyManager.GetKeyCount()
			totalUsed := keyManager.GetTotalUsed()
			apiKeyStatusLabel.SetText(fmt.Sprintf("已加载 %d 个 API Key", keyCount))

			// 强制刷新表格（在主线程中）
			fyne.Do(func() {
				keyStatusTable.Refresh()
			})

			// 显示统计信息
			statsMsg := fmt.Sprintf("已加载 %d 个 API Key\n历史总使用次数: %d", keyCount, totalUsed)
			if totalUsed > 0 {
				statsMsg += "\n\n已自动加载历史使用记录！"
			}
			dialog.ShowInformation("成功", statsMsg, w)
		}, w)
	})

	// 删除单个 Key 按钮
	deleteKeyBtn := widget.NewButton("删除指定 Key", func() {
		status := keyManager.GetKeyStatus()
		if len(status) == 0 {
			dialog.ShowError(fmt.Errorf("没有可删除的 Key"), w)
			return
		}

		// 创建选择列表
		keyOptions := make([]string, len(status))
		for i, keyStatus := range status {
			keyOptions[i] = fmt.Sprintf("%s (已用: %d/%d)", keyStatus.DisplayName, keyStatus.Used, keyStatus.MaxLimit)
		}

		keySelect := widget.NewSelect(keyOptions, func(selected string) {
			// 选择回调（暂时不使用，因为我们需要在确认时获取）
		})
		if len(keyOptions) > 0 {
			keySelect.SetSelected(keyOptions[0])
		}

		// 创建表单
		deleteForm := container.NewVBox(
			widget.NewLabel("选择要删除的 Key:"),
			keySelect,
		)

		// 使用新的对话框窗口
		deleteDialog := dialog.NewCustom("选择 Key", "确定", deleteForm, w)
		deleteDialog.Resize(fyne.NewSize(400, 150))

		// 创建一个确认按钮来处理实际删除
		confirmBtn := widget.NewButton("删除", func() {
			deleteDialog.Hide()

			selectedIndex := keySelect.SelectedIndex()
			if selectedIndex < 0 || selectedIndex >= len(status) {
				dialog.ShowError(fmt.Errorf("无效的选择"), w)
				return
			}

			keyToDelete := status[selectedIndex].Key
			displayName := status[selectedIndex].DisplayName

			// 确认对话框
			dialog.ShowConfirm("确认删除", fmt.Sprintf("确定要删除 %s 吗？\nKey: %s...", displayName, keyToDelete[:min(20, len(keyToDelete))]), func(confirmed bool) {
				if confirmed {
					if err := keyManager.RemoveKey(keyToDelete); err != nil {
						dialog.ShowError(err, w)
						return
					}

					keyCount := keyManager.GetKeyCount()
					apiKeyStatusLabel.SetText(fmt.Sprintf("已加载 %d 个 API Key", keyCount))

					fyne.Do(func() {
						keyStatusTable.Refresh()
					})

					dialog.ShowInformation("成功", fmt.Sprintf("已删除 %s", displayName), w)
				}
			}, w)
		})

		// 重新创建表单，包含确认按钮
		deleteFormWithBtn := container.NewVBox(
			widget.NewLabel("选择要删除的 Key:"),
			keySelect,
			container.NewHBox(
				widget.NewButton("取消", func() { deleteDialog.Hide() }),
				confirmBtn,
			),
		)

		deleteDialog = dialog.NewCustom("选择 Key", "关闭", deleteFormWithBtn, w)
		deleteDialog.Resize(fyne.NewSize(400, 150))
		deleteDialog.Show()
	})

	// 一键删除按钮（按使用次数阈值）
	batchDeleteBtn := widget.NewButton("批量删除", func() {
		// 创建输入对话框
		thresholdEntry := widget.NewEntry()
		thresholdEntry.SetText("99998")
		thresholdEntry.SetPlaceHolder("使用次数阈值（>=此值将被删除）")

		var batchDeleteDialog *dialog.CustomDialog

		// 创建确认按钮
		confirmBatchBtn := widget.NewButton("确定", func() {
			if batchDeleteDialog != nil {
				batchDeleteDialog.Hide()
			}

			thresholdStr := strings.TrimSpace(thresholdEntry.Text)
			threshold := 99998
			if thresholdStr != "" {
				if _, err := fmt.Sscanf(thresholdStr, "%d", &threshold); err != nil {
					dialog.ShowError(fmt.Errorf("无效的阈值: %s", thresholdStr), w)
					return
				}
			}

			// 获取将要删除的Key列表（预览）
			status := keyManager.GetKeyStatus()
			matchingKeys := make([]string, 0)
			for _, keyStatus := range status {
				if keyStatus.Used >= threshold {
					matchingKeys = append(matchingKeys, keyStatus.DisplayName)
				}
			}

			if len(matchingKeys) == 0 {
				dialog.ShowInformation("提示", fmt.Sprintf("没有使用次数 >= %d 的 Key", threshold), w)
				return
			}

			// 确认对话框
			previewText := fmt.Sprintf("以下 %d 个 Key 将被删除（使用次数 >= %d）：\n\n", len(matchingKeys), threshold)
			if len(matchingKeys) <= 10 {
				previewText += strings.Join(matchingKeys, "\n")
			} else {
				previewText += strings.Join(matchingKeys[:10], "\n") + fmt.Sprintf("\n... 还有 %d 个", len(matchingKeys)-10)
			}

			dialog.ShowConfirm("确认批量删除", previewText, func(confirmed bool) {
				if confirmed {
					removedCount, err := keyManager.RemoveKeysByUsageThreshold(threshold)
					if err != nil {
						dialog.ShowError(err, w)
						return
					}

					keyCount := keyManager.GetKeyCount()
					apiKeyStatusLabel.SetText(fmt.Sprintf("已加载 %d 个 API Key", keyCount))

					fyne.Do(func() {
						keyStatusTable.Refresh()
					})

					dialog.ShowInformation("成功", fmt.Sprintf("已删除 %d 个 Key", removedCount), w)
				}
			}, w)
		})

		// 创建包含确认按钮的表单
		deleteFormWithBtn := container.NewVBox(
			widget.NewLabel("删除使用次数达到或超过阈值的 Key"),
			widget.NewForm(
				widget.NewFormItem("使用次数阈值:", thresholdEntry),
			),
			container.NewHBox(
				widget.NewButton("取消", func() {
					if batchDeleteDialog != nil {
						batchDeleteDialog.Hide()
					}
				}),
				confirmBatchBtn,
			),
		)

		// 创建自定义对话框
		batchDeleteDialog = dialog.NewCustom("批量删除 Key", "关闭", deleteFormWithBtn, w)
		batchDeleteDialog.Resize(fyne.NewSize(400, 150))
		batchDeleteDialog.Show()
	})

	// 自定义节点 URL（可选）
	nodeURLEntry := widget.NewEntry()
	nodeURLEntry.SetPlaceHolder("自定义 TRON 节点 URL（留空使用 TronGrid）")

	// 限流设置
	rateLimitEntry := widget.NewEntry()
	rateLimitEntry.SetText("12")
	rateLimitEntry.SetPlaceHolder("每秒请求数 (10-15)")

	// 线程数设置
	threadCountEntry := widget.NewEntry()
	threadCountEntry.SetText("1")
	threadCountEntry.SetPlaceHolder("并发线程数 (1-20)")

	// 线程数说明
	threadHelpLabel := widget.NewLabel("💡 多线程说明：设置N个线程，将同时使用N个不同的API Key并发查询，避免单个Key请求过多。Key达到10万次后自动跳过。")
	threadHelpLabel.Wrapping = fyne.TextWrapWord
	threadHelpLabel.TextStyle = fyne.TextStyle{Italic: true}

	// 地址输入区域
	addressInput := widget.NewMultiLineEntry()
	addressInput.SetPlaceHolder("支持批量输入，格式如下：\n1. 每行一个地址\n2. 用逗号分隔：地址1,地址2,地址3\n3. 用空格分隔：地址1 地址2 地址3\n4. 或点击下方按钮导入文件（TXT/CSV）")
	addressInput.Wrapping = fyne.TextWrapOff // 关闭自动换行，确保地址正确显示（每行一个地址）

	// 导入文件按钮（清空按钮会在后面定义，因为这些控件需要先创建）
	importFileBtn := widget.NewButton("📁 导入地址", func() {
		dialog.ShowFileOpen(func(reader fyne.URIReadCloser, err error) {
			if err != nil {
				dialog.ShowError(err, w)
				return
			}
			if reader == nil {
				return
			}
			defer reader.Close()

			addresses, err := core.LoadAddressesFromFile(reader.URI().Path())
			if err != nil {
				dialog.ShowError(err, w)
				return
			}

			addressList = addresses
			// 构建所有地址的文本（每行一个地址）
			addressText := strings.Join(addresses, "\n")
			// 确保所有地址都被设置（使用fyne.Do确保在主线程更新）
			fyne.Do(func() {
				addressInput.SetText(addressText)
				addressInput.Refresh() // 强制刷新
				// 滚动到顶部，确保能看到第一个地址
				addressInput.CursorRow = 0
				addressInput.CursorColumn = 0
				// 再次刷新，确保滚动位置正确
				addressInput.Refresh()
			})
			dialog.ShowInformation("成功", fmt.Sprintf("已加载 %d 个地址", len(addresses)), w)
		}, w)
	})

	// 查询按钮 - 添加图标使界面更友好
	queryBtn := widget.NewButton("▶ 开始查询", nil)
	pauseBtn := widget.NewButton("⏸ 暂停", nil)
	pauseBtn.Disable()
	stopBtn := widget.NewButton("⏹ 停止", nil)
	stopBtn.Disable()

	// 进度条
	progressBar := widget.NewProgressBar()
	progressLabel := widget.NewLabel("等待开始...")

	// 状态栏
	statusLabel := widget.NewLabel("就绪")

	// 初始化分页和筛选变量
	currentPage = 1
	pageSize = 10000 // 每页10000条
	filterMode = "all"
	filterText = ""

	// 筛选和分页函数
	applyFilter := func() {
		if resultData == nil || len(resultData) == 0 {
			filteredData = make([]core.QueryResult, 0)
			displayData = make([]core.QueryResult, 0)
			totalPages = 1
			currentPage = 1
			return
		}

		// 应用筛选
		filteredData = make([]core.QueryResult, 0)
		for _, result := range resultData {
			match := true

			// 按筛选模式筛选
			if filterMode == "withBalance" {
				// 只显示有余额的（余额>0）
				balanceStr := result.Balance
				if balanceStr == "" {
					balanceStr = "0"
				}
				// 解析余额字符串（去除逗号等）
				balanceStr = strings.ReplaceAll(balanceStr, ",", "")
				if len(balanceStr) > 0 {
					// 检查是否为0
					var balance float64
					fmt.Sscanf(balanceStr, "%f", &balance)
					if balance <= 0 {
						match = false
					}
				} else {
					match = false
				}
			}

			// 按地址文本筛选
			if match && filterText != "" {
				if !strings.Contains(strings.ToLower(result.Address), strings.ToLower(filterText)) {
					match = false
				}
			}

			if match {
				filteredData = append(filteredData, result)
			}
		}

		// 计算分页
		totalPages = (len(filteredData) + pageSize - 1) / pageSize
		if totalPages == 0 {
			totalPages = 1
		}
		if currentPage > totalPages {
			currentPage = totalPages
		}

		// 获取当前页数据
		start := (currentPage - 1) * pageSize
		end := start + pageSize
		if end > len(filteredData) {
			end = len(filteredData)
		}
		if start < len(filteredData) {
			displayData = filteredData[start:end]
		} else {
			displayData = nil
		}
	}

	// 结果表格（改进样式 - 显示当前页数据）
	// 使用局部变量快照避免并发访问问题
	resultTable := widget.NewTable(
		func() (int, int) {
			if displayData == nil {
				return 0, 4
			}
			return len(displayData), 4
		},
		func() fyne.CanvasObject {
			label := widget.NewLabel("")
			label.Wrapping = fyne.TextWrapWord
			return label
		},
		func(id widget.TableCellID, obj fyne.CanvasObject) {
			label := obj.(*widget.Label)
			// 使用局部快照避免滚动时数据变化
			dataSnapshot := displayData
			if dataSnapshot == nil || id.Row >= len(dataSnapshot) {
				label.SetText("")
				return
			}

			result := dataSnapshot[id.Row]
			switch id.Col {
			case 0: // 地址列 - 左对齐，不换行
				label.SetText(result.Address)
				label.Alignment = fyne.TextAlignLeading
				label.Wrapping = fyne.TextWrapOff // 地址不换行，避免对齐问题
			case 1: // 余额列 - 右对齐
				if result.Balance == "" {
					label.SetText("0.000000")
				} else {
					label.SetText(result.Balance)
				}
				label.Alignment = fyne.TextAlignTrailing
			case 2: // 状态列 - 居中对齐
				switch result.Status {
				case "success":
					label.SetText("成功")
					label.Importance = widget.SuccessImportance
				case "error":
					label.SetText("失败")
					label.Importance = widget.DangerImportance
				case "cancelled":
					label.SetText("已取消")
					label.Importance = widget.MediumImportance
				case "pending":
					label.SetText("待查询")
					label.Importance = widget.MediumImportance
				default:
					label.SetText("查询中...")
					label.Importance = widget.MediumImportance
				}
				label.Alignment = fyne.TextAlignCenter
			case 3: // 错误信息列 - 左对齐，允许换行（错误信息可能较长）
				label.SetText(result.Error)
				label.Alignment = fyne.TextAlignLeading
				label.Wrapping = fyne.TextWrapWord // 错误信息可以换行
			}
		})

	resultTable.SetColumnWidth(0, 420) // 地址列（确保完整显示34字符的TRON地址）
	resultTable.SetColumnWidth(1, 120) // 余额列
	resultTable.SetColumnWidth(2, 80)  // 状态列
	resultTable.SetColumnWidth(3, 250) // 错误信息列

	// 分页控件（先定义，因为筛选控件会用到）
	pageInfoLabel := widget.NewLabel("第 1 页 / 共 1 页 (共 0 条)")

	// 更新分页信息的辅助函数
	updatePageInfo := func() {
		pageInfoLabel.SetText(fmt.Sprintf("第 %d 页 / 共 %d 页 (共 %d 条，显示 %d-%d 条)",
			currentPage, totalPages, len(filteredData),
			func() int {
				if len(filteredData) == 0 {
					return 0
				}
				return (currentPage-1)*pageSize + 1
			}(),
			min(currentPage*pageSize, len(filteredData))))
	}

	// 筛选控件
	filterModeSelect := widget.NewSelect([]string{"全部", "有余额", "按地址搜索"}, func(selected string) {
		switch selected {
		case "全部":
			filterMode = "all"
		case "有余额":
			filterMode = "withBalance"
		case "按地址搜索":
			filterMode = "address"
		}
		applyFilter()
		resultTable.Refresh()
		updatePageInfo()
	})
	filterModeSelect.SetSelected("全部")

	addressSearchEntry := widget.NewEntry()
	addressSearchEntry.SetPlaceHolder("输入地址关键词搜索...")
	addressSearchEntry.OnChanged = func(text string) {
		filterText = text
		applyFilter()
		resultTable.Refresh()
		updatePageInfo()
	}
	prevPageBtn := widget.NewButton("上一页", func() {
		if currentPage > 1 {
			currentPage--
			applyFilter()
			resultTable.Refresh()
			updatePageInfo()
		}
	})
	nextPageBtn := widget.NewButton("下一页", func() {
		if currentPage < totalPages {
			currentPage++
			applyFilter()
			resultTable.Refresh()
			updatePageInfo()
		}
	})

	jumpPageEntry := widget.NewEntry()
	jumpPageEntry.SetPlaceHolder("页码")
	jumpPageEntry.Resize(fyne.NewSize(60, 0)) // 设置跳转输入框的宽度
	jumpPageBtn := widget.NewButton("跳转", func() {
		pageStr := strings.TrimSpace(jumpPageEntry.Text)
		if pageStr != "" {
			var page int
			if _, err := fmt.Sscanf(pageStr, "%d", &page); err == nil && page >= 1 && page <= totalPages {
				currentPage = page
				applyFilter()
				resultTable.Refresh()
				updatePageInfo()
				jumpPageEntry.SetText("")
			} else {
				dialog.ShowError(fmt.Errorf("无效的页码，请输入 1-%d 之间的数字", totalPages), w)
			}
		}
	})

	// 删除地址按钮
	deleteAddressBtn := widget.NewButton("删除选中地址", func() {
		dialog.ShowInformation("提示", "删除功能开发中...", w)
	})

	// 筛选控件区域 - 使用Border让搜索框占据主要空间
	filterContainer := container.NewBorder(
		nil, nil,
		container.NewHBox(
			widget.NewLabel("筛选:"),
			filterModeSelect,
		),
		nil,
		addressSearchEntry, // 搜索框占据中间的主要空间，自动扩展
	)

	// 表头（放在筛选下面）- 使用GridWithColumns自动对齐表格列
	headerContainer := container.NewGridWithColumns(4,
		widget.NewLabelWithStyle("地址", fyne.TextAlignCenter, fyne.TextStyle{Bold: true}),
		widget.NewLabelWithStyle("余额 (USDT)", fyne.TextAlignCenter, fyne.TextStyle{Bold: true}),
		widget.NewLabelWithStyle("状态", fyne.TextAlignCenter, fyne.TextStyle{Bold: true}),
		widget.NewLabelWithStyle("错误信息", fyne.TextAlignCenter, fyne.TextStyle{Bold: true}),
	)

	// 导出按钮
	exportCSVBtn := widget.NewButton("📄 导出 CSV", nil)
	exportExcelBtn := widget.NewButton("📊 导出 Excel", nil)
	exportCSVBtn.Disable()
	exportExcelBtn.Disable()

	// 使用 channel 将更新请求发送到主线程
	updateChan := make(chan struct{}, 1)
	go func() {
		for range updateChan {
			mu.Lock()
			progress := lastProgress
			mu.Unlock()

			if progress.current > 0 || progress.done {
				fyne.Do(func() {
					// 计算剩余数量
					remaining := progress.total - progress.current

					progressBar.SetValue(float64(progress.current) / float64(progress.total))
					// 显示进度：已完成/总数，剩余X个
					progressLabel.SetText(fmt.Sprintf("已完成: %d / %d | 剩余: %d 个", progress.current, progress.total, remaining))

					if progress.stats.total > 0 {
						// 计算有余额和没有余额的数量
						withBalance := 0
						withoutBalance := 0
						for _, result := range progress.results {
							if result.Status == "success" {
								balanceStr := result.Balance
								if balanceStr == "" {
									balanceStr = "0"
								}
								balanceStr = strings.ReplaceAll(balanceStr, ",", "")
								var balance float64
								if _, err := fmt.Sscanf(balanceStr, "%f", &balance); err == nil {
									if balance > 0 {
										withBalance++
									} else {
										withoutBalance++
									}
								} else {
									withoutBalance++
								}
							}
						}
						statusText := fmt.Sprintf("总计: %d | 成功: %d | 失败: %d | 有余额: %d | 无余额: %d",
							progress.stats.total, progress.stats.success, progress.stats.failed, withBalance, withoutBalance)
						statusLabel.SetText(statusText)
					}

					// 更新结果表格（确保显示所有结果，包括空结果）
					// 创建结果数据的副本，避免引用问题
					if len(progress.results) > 0 {
						resultData = make([]core.QueryResult, len(progress.results))
						copy(resultData, progress.results)
					} else if progress.total > 0 {
						// 如果结果为空但总数大于0，确保至少显示与地址数量对应的空行
						if resultData == nil || len(resultData) != progress.total {
							resultData = make([]core.QueryResult, progress.total)
						}
					}
					// 应用筛选和分页
					applyFilter()
					// 更新分页信息
					updatePageInfo()
					// 强制刷新表格，确保所有行都显示
					resultTable.Refresh()

					// 更新 Key 状态
					updateKeyStatusTable(keyStatusTable, keyManager)

					if progress.done {
						isQuerying = false
						isPaused = false
						pausedAddresses = nil
						pausedStartIndex = 0
						pausedTotalProgress = 0
						// 不清空 currentQueryAddrs，以便用户可以重新查询
						queryBtn.Enable()
						queryBtn.SetText("▶ 开始查询")
						pauseBtn.Disable()
						stopBtn.Disable()
						importFileBtn.Enable()
						exportCSVBtn.Enable()
						exportExcelBtn.Enable()

						// 计算有余额和没有余额的数量
						withBalance := 0
						withoutBalance := 0
						for _, result := range progress.results {
							if result.Status == "success" {
								balanceStr := result.Balance
								if balanceStr == "" {
									balanceStr = "0"
								}
								balanceStr = strings.ReplaceAll(balanceStr, ",", "")
								var balance float64
								if _, err := fmt.Sscanf(balanceStr, "%f", &balance); err == nil {
									if balance > 0 {
										withBalance++
									} else {
										withoutBalance++
									}
								} else {
									withoutBalance++
								}
							}
						}

						finalStatus := fmt.Sprintf("完成！总计: %d | 成功: %d | 失败: %d | 有余额: %d | 无余额: %d",
							progress.total, progress.stats.success, progress.stats.failed, withBalance, withoutBalance)
						statusLabel.SetText(finalStatus)
						progressLabel.SetText(fmt.Sprintf("完成：%d / %d（剩余: 0 个）", progress.total, progress.total))
					}
				})
			}
		}
	}()

	// 使用定时器触发更新检查
	updateTicker := time.NewTicker(200 * time.Millisecond)
	go func() {
		for range updateTicker.C {
			select {
			case updateChan <- struct{}{}:
			default:
			}
		}
	}()

	// 查询按钮点击事件
	queryBtn.OnTapped = func() {
		// 检查是否有 API Key
		if keyManager.GetKeyCount() == 0 {
			dialog.ShowError(fmt.Errorf("请先导入 API Key 文件"), w)
			return
		}

		var addresses []string
		var startOffset int = 0 // 本次查询的起始偏移量（用于合并结果）
		var isContinue bool = false

		// 如果是继续之前暂停的查询
		if isPaused && pausedAddresses != nil && len(pausedAddresses) > 0 {
			addresses = pausedAddresses
			startOffset = pausedTotalProgress
			isContinue = true
			isPaused = false
			queryBtn.SetText("▶ 开始查询")
			statusLabel.SetText(fmt.Sprintf("继续查询，从第 %d 个开始，剩余 %d 个地址...", startOffset+1, len(addresses)))
			fmt.Printf("[DEBUG] 继续查询: startOffset=%d, 剩余地址=%d\n", startOffset, len(addresses))
		} else {
			// 新查询
			text := strings.TrimSpace(addressInput.Text)
			if text == "" {
				dialog.ShowError(fmt.Errorf("请先输入或导入地址"), w)
				return
			}

			// 加载地址
			var err error
			if addressList != nil && len(addressList) > 0 {
				addresses = addressList
			} else {
				addresses, err = core.LoadAddressesFromText(text)
				if err != nil {
					dialog.ShowError(fmt.Errorf("地址解析失败: %v\n\n提示：\n- 每行一个地址\n- 或用逗号/空格分隔：地址1,地址2 地址3\n- 或使用导入文件功能", err), w)
					return
				}
			}

			if len(addresses) == 0 {
				dialog.ShowError(fmt.Errorf("没有找到有效的地址\n\n请检查：\n1. 地址格式是否正确（34个字符，以T开头）\n2. 是否使用了正确的分隔符（换行、逗号、空格）\n3. 或使用导入文件功能"), w)
				return
			}

			// 显示加载的地址数量
			if len(addresses) > 1 {
				statusLabel.SetText(fmt.Sprintf("已加载 %d 个地址，准备查询...", len(addresses)))
			}

			// 如果之前有查询，先取消它（避免状态混乱）
			if queryManager != nil && isQuerying {
				queryManager.Cancel()
			}

			// 初始化结果（新查询）
			currentQueryAddrs = addresses
			resultData = make([]core.QueryResult, len(addresses))
			resultTable.Refresh()
			pausedTotalProgress = 0
			startOffset = 0
			fmt.Printf("[DEBUG] 新查询: 总地址=%d\n", len(addresses))
		}

		// 创建查询管理器
		nodeURL := strings.TrimSpace(nodeURLEntry.Text)
		queryManager = core.NewQueryManager(keyManager, nodeURL)

		// 设置线程数
		threadCountText := strings.TrimSpace(threadCountEntry.Text)
		if threadCountText == "" {
			threadCountText = "1"
		}
		var threadCount int
		_, err := fmt.Sscanf(threadCountText, "%d", &threadCount)
		if err != nil || threadCount < 1 {
			threadCount = 1
		}
		if threadCount > 20 {
			threadCount = 20
		}
		queryManager.SetMaxConcurrent(threadCount)

		// 开始查询
		isQuerying = true
		queryBtn.Disable()
		pauseBtn.Enable() // 确保暂停按钮可用
		stopBtn.Enable()  // 启用停止按钮
		importFileBtn.Disable()
		importKeyBtn.Disable()
		exportCSVBtn.Disable()
		exportExcelBtn.Disable()
		if !isContinue {
			progressBar.SetValue(0)
			progressLabel.SetText(fmt.Sprintf("0 / %d", len(currentQueryAddrs)))
		}

		// 在新 goroutine 中查询（使用闭包捕获 startOffset 和 isContinue）
		go func(offset int, isCont bool) {
			queryCancel = queryManager.Cancel
			fmt.Printf("[DEBUG] Goroutine 开始: offset=%d, isCont=%v\n", offset, isCont)

			queryManager.QueryAddresses(addresses, func(current, total int) {
				mu.Lock()
				// 如果是继续查询，需要累加之前的进度
				if isCont {
					lastProgress.current = offset + current
					lastProgress.total = len(currentQueryAddrs)
					fmt.Printf("[DEBUG] 继续进度更新: current=%d (offset=%d + %d), total=%d\n",
						lastProgress.current, offset, current, lastProgress.total)
				} else {
					lastProgress.current = current
					lastProgress.total = total
				}
				lastProgress.stats.total, lastProgress.stats.success, lastProgress.stats.failed = queryManager.GetStats()

				// 获取当前批次的结果
				currentResults := queryManager.GetResults()

				// 如果是继续查询，需要合并到之前的结果中
				if isCont {
					// 将新结果合并到 resultData 的对应位置
					for i, result := range currentResults {
						if offset+i < len(resultData) {
							resultData[offset+i] = result
						}
					}
					// lastProgress.results 保持为完整的 resultData
					lastProgress.results = make([]core.QueryResult, len(resultData))
					copy(lastProgress.results, resultData)
				} else {
					// 新查询，直接使用结果
					lastProgress.results = make([]core.QueryResult, len(currentResults))
					copy(lastProgress.results, currentResults)
				}
				mu.Unlock()
				// 触发更新
				select {
				case updateChan <- struct{}{}:
				default:
				}
			})

			// 查询完成或被取消
			mu.Lock()
			// 检查是否被取消
			wasCancelled := (queryManager.Ctx().Err() != nil)
			if !wasCancelled {
				lastProgress.done = true
				fmt.Printf("[DEBUG] 查询正常完成\n")
			} else {
				fmt.Printf("[DEBUG] 查询被取消，不设置 done=true\n")
			}

			if isCont {
				// 合并最终结果
				currentResults := queryManager.GetResults()
				for i, result := range currentResults {
					if offset+i < len(resultData) {
						resultData[offset+i] = result
					}
				}
				lastProgress.results = make([]core.QueryResult, len(resultData))
				copy(lastProgress.results, resultData)
				if !wasCancelled {
					lastProgress.current = len(currentQueryAddrs)
					lastProgress.total = len(currentQueryAddrs)
					fmt.Printf("[DEBUG] 继续查询完成: total=%d\n", lastProgress.current)
				}
			} else {
				results := queryManager.GetResults()
				lastProgress.results = make([]core.QueryResult, len(results))
				copy(lastProgress.results, results)
				if !wasCancelled {
					lastProgress.current = len(addresses)
					lastProgress.total = len(addresses)
					fmt.Printf("[DEBUG] 新查询完成: total=%d\n", lastProgress.current)
				}
			}
			lastProgress.stats.total, lastProgress.stats.success, lastProgress.stats.failed = queryManager.GetStats()
			mu.Unlock()
			// 触发最终更新
			select {
			case updateChan <- struct{}{}:
			default:
			}
		}(startOffset, isContinue)
	}

	// 暂停按钮（保留未完成的地址，可以继续）
	pauseBtn.OnTapped = func() {
		if queryManager != nil && isQuerying {
			// 取消当前查询
			queryManager.Cancel()

			// 等待一小段时间确保查询已停止
			time.Sleep(200 * time.Millisecond)

			// 获取当前进度
			mu.Lock()
			currentProgress := lastProgress.current
			totalAddresses := lastProgress.total
			mu.Unlock()

			fmt.Printf("[DEBUG] 暂停: currentProgress=%d, totalAddresses=%d\n", currentProgress, totalAddresses)

			// 保存剩余未查询的地址（从 currentQueryAddrs 获取）
			if currentProgress < totalAddresses && currentQueryAddrs != nil && len(currentQueryAddrs) > 0 {
				if currentProgress < len(currentQueryAddrs) {
					pausedAddresses = currentQueryAddrs[currentProgress:]
					pausedTotalProgress = currentProgress
					fmt.Printf("[DEBUG] 保存暂停状态: pausedTotalProgress=%d, 剩余地址=%d\n",
						pausedTotalProgress, len(pausedAddresses))
				}
			}

			isQuerying = false
			isPaused = true

			// 重要：使用 fyne.Do 确保 UI 更新在主线程
			fyne.Do(func() {
				queryBtn.Enable()
				queryBtn.SetText("▶ 继续查询")
				pauseBtn.Disable()
				stopBtn.Disable()
				importFileBtn.Enable()
				importKeyBtn.Enable()
				deleteKeyBtn.Enable()
				batchDeleteBtn.Enable()
			})

			finalTotal, finalSuccess, finalFailed := queryManager.GetStats()
			// 计算有余额和无余额数量
			withBalance := 0
			withoutBalance := 0
			for _, result := range resultData {
				if result.Status == "success" {
					balanceStr := result.Balance
					if balanceStr == "" {
						balanceStr = "0"
					}
					balanceStr = strings.ReplaceAll(balanceStr, ",", "")
					var balance float64
					if _, err := fmt.Sscanf(balanceStr, "%f", &balance); err == nil {
						if balance > 0 {
							withBalance++
						} else {
							withoutBalance++
						}
					} else {
						withoutBalance++
					}
				}
			}
			remainingCount := 0
			if totalAddresses > currentProgress {
				remainingCount = totalAddresses - currentProgress
			}
			statusText := fmt.Sprintf("已暂停 | 总计: %d | 成功: %d | 失败: %d | 有余额: %d | 无余额: %d | 剩余: %d",
				finalTotal, finalSuccess, finalFailed, withBalance, withoutBalance, remainingCount)
			statusLabel.SetText(statusText)
		}
	}

	// 停止按钮（清空所有状态，不能继续）
	stopBtn.OnTapped = func() {
		if queryManager != nil && isQuerying {
			queryManager.Cancel()

			// 等待查询停止
			time.Sleep(200 * time.Millisecond)

			isQuerying = false
			isPaused = false
			pausedAddresses = nil
			pausedStartIndex = 0
			pausedTotalProgress = 0
			currentQueryAddrs = nil

			fmt.Printf("[DEBUG] 停止查询\n")

			// 使用 fyne.Do 确保 UI 更新在主线程
			fyne.Do(func() {
				queryBtn.Enable()
				queryBtn.SetText("▶ 开始查询")
				pauseBtn.Disable()
				stopBtn.Disable()
				importFileBtn.Enable()
				importKeyBtn.Enable()
				deleteKeyBtn.Enable()
				batchDeleteBtn.Enable()
			})

			finalTotal, finalSuccess, finalFailed := queryManager.GetStats()
			// 计算有余额和无余额数量
			withBalance := 0
			withoutBalance := 0
			for _, result := range resultData {
				if result.Status == "success" {
					balanceStr := result.Balance
					if balanceStr == "" {
						balanceStr = "0"
					}
					balanceStr = strings.ReplaceAll(balanceStr, ",", "")
					var balance float64
					if _, err := fmt.Sscanf(balanceStr, "%f", &balance); err == nil {
						if balance > 0 {
							withBalance++
						} else {
							withoutBalance++
						}
					} else {
						withoutBalance++
					}
				}
			}
			statusText := fmt.Sprintf("已停止 | 总计: %d | 成功: %d | 失败: %d | 有余额: %d | 无余额: %d",
				finalTotal, finalSuccess, finalFailed, withBalance, withoutBalance)
			statusLabel.SetText(statusText)
		}
	}

	// 导出 CSV
	exportCSVBtn.OnTapped = func() {
		if resultData == nil || len(resultData) == 0 {
			dialog.ShowError(fmt.Errorf("没有可导出的数据"), w)
			return
		}

		dialog.ShowFileSave(func(writer fyne.URIWriteCloser, err error) {
			if err != nil {
				dialog.ShowError(err, w)
				return
			}
			if writer == nil {
				return
			}
			defer writer.Close()

			filepath := writer.URI().Path()
			if !strings.HasSuffix(strings.ToLower(filepath), ".csv") {
				filepath += ".csv"
			}

			if err := core.ExportToCSV(resultData, filepath); err != nil {
				dialog.ShowError(err, w)
				return
			}

			dialog.ShowInformation("成功", fmt.Sprintf("已导出到: %s", filepath), w)
		}, w)
	}

	// 导出 Excel
	exportExcelBtn.OnTapped = func() {
		if resultData == nil || len(resultData) == 0 {
			dialog.ShowError(fmt.Errorf("没有可导出的数据"), w)
			return
		}

		dialog.ShowFileSave(func(writer fyne.URIWriteCloser, err error) {
			if err != nil {
				dialog.ShowError(err, w)
				return
			}
			if writer == nil {
				return
			}
			defer writer.Close()

			filepath := writer.URI().Path()
			if !strings.HasSuffix(strings.ToLower(filepath), ".xlsx") {
				filepath += ".xlsx"
			}

			if err := core.ExportToExcel(resultData, filepath); err != nil {
				dialog.ShowError(err, w)
				return
			}

			dialog.ShowInformation("成功", fmt.Sprintf("已导出到: %s", filepath), w)
		}, w)
	}

	// 清空地址按钮（定义在导出按钮之后，以便可以访问所有控件）
	clearAddressBtn := widget.NewButton("清空地址", func() {
		fyne.Do(func() {
			// 清空输入框
			addressInput.SetText("")
			addressList = nil

			// 清空所有结果数据
			resultData = nil
			filteredData = nil
			displayData = nil

			// 重置分页和筛选
			currentPage = 1
			totalPages = 1
			filterMode = "all"
			filterText = ""
			if filterModeSelect != nil {
				filterModeSelect.SetSelected("全部")
			}
			if addressSearchEntry != nil {
				addressSearchEntry.SetText("")
			}

			// 应用筛选（会更新 filteredData 和 displayData）
			applyFilter()

			// 强制刷新表格和分页信息
			if resultTable != nil {
				resultTable.Refresh()
			}
			if updatePageInfo != nil {
				updatePageInfo()
			}

			// 禁用导出按钮
			if exportCSVBtn != nil {
				exportCSVBtn.Disable()
			}
			if exportExcelBtn != nil {
				exportExcelBtn.Disable()
			}

			// 重置进度
			if progressBar != nil {
				progressBar.SetValue(0)
			}
			if progressLabel != nil {
				progressLabel.SetText("等待开始...")
			}
			if statusLabel != nil {
				statusLabel.SetText("就绪")
			}
		})
	})

	// 布局
	// Key 状态表格容器（使用滚动容器以便显示更多内容）
	keyTableScroll := container.NewScroll(keyStatusTable)
	keyTableScroll.SetMinSize(fyne.NewSize(0, 80)) // 设置合适的最小高度，避免空白太多

	apiKeyContainer := widget.NewCard("API Key 管理", "",
		container.NewVBox(
			apiKeyStatusLabel,
			importKeyBtn,
			container.NewHBox(deleteKeyBtn, batchDeleteBtn),
			keyStatusHeader,
			keyTableScroll,
		),
	)

	// 左侧配置区域布局
	configContainer := container.NewVBox(
		apiKeyContainer,
		widget.NewCard("网络配置", "",
			container.NewVBox(
				widget.NewForm(
					widget.NewFormItem("并发线程数:", threadCountEntry),
					widget.NewFormItem("自定义节点 URL:", nodeURLEntry),
					widget.NewFormItem("每秒请求数:", rateLimitEntry),
				),
				threadHelpLabel,
			),
		),
		widget.NewCard("地址输入", "",
			container.NewVBox(
				// 地址输入框，使用Border布局让其占据Card内的主要空间
				container.NewBorder(
					nil, nil, nil, nil,
					addressInput,
				),
				container.NewHBox(importFileBtn, clearAddressBtn),
			),
		),
		widget.NewSeparator(), // 添加分隔线，使布局更清晰
		widget.NewCard("查询控制", "",
			container.NewVBox(
				container.NewHBox(queryBtn, pauseBtn, stopBtn),
				progressBar,
				progressLabel,
				statusLabel,
			),
		),
	)

	// 底部控件（分页和导出）- 优化布局，使分页信息更清晰
	paginationControls := container.NewBorder(
		nil, nil,
		container.NewHBox(prevPageBtn, nextPageBtn),
		container.NewHBox(
			widget.NewLabel("跳转:"),
			jumpPageEntry,
			jumpPageBtn,
		),
		pageInfoLabel, // 分页信息居中显示
	)

	bottomControls := container.NewVBox(
		paginationControls,
		container.NewHBox(
			exportCSVBtn,
			exportExcelBtn,
			deleteAddressBtn,
		),
	)

	// 使用Border布局，将分页和导出固定在底部
	resultContainer := container.NewBorder(
		container.NewVBox(filterContainer, headerContainer), // Top: 筛选和表头
		bottomControls,                                      // Bottom: 分页和导出（固定在最底部）
		nil,                                                 // Left: 无
		nil,                                                 // Right: 无
		container.NewScroll(resultTable),                    // Center: 表格（可扩展）
	)

	split := container.NewHSplit(configContainer, resultContainer)
	split.SetOffset(0.32) // 调整左右分栏比例，左侧更紧凑，右侧表格有更多空间

	w.SetContent(split)

	// 设置拖拽功能
	w.SetOnDropped(func(pos fyne.Position, uris []fyne.URI) {
		if len(uris) == 0 {
			return
		}

		for _, uri := range uris {
			filePath := uri.Path()
			ext := strings.ToLower(filepath.Ext(filePath))

			// 只支持 TXT 和 CSV 文件
			if ext != ".txt" && ext != ".csv" {
				dialog.ShowError(fmt.Errorf("不支持的文件类型: %s\n请拖入 TXT 或 CSV 文件", ext), w)
				continue
			}

			// 尝试读取文件内容，判断是 Key 文件还是地址文件
			addresses, addrErr := core.LoadAddressesFromFile(filePath)

			// 判断是否为地址文件：如果成功加载了地址，则认为是地址文件
			if addrErr == nil && len(addresses) > 0 {
				// 这是地址文件
				addressList = addresses
				// 构建所有地址的文本（每行一个地址）
				addressText := strings.Join(addresses, "\n")
				// 确保所有地址都被设置（使用fyne.Do确保在主线程更新）
				fyne.Do(func() {
					addressInput.SetText(addressText)
					addressInput.Refresh() // 强制刷新，确保文本正确显示
					// 滚动到顶部，确保能看到第一个地址
					addressInput.CursorRow = 0
					addressInput.CursorColumn = 0
				})

				// 在结果表格中显示这些地址（初始状态：待查询）
				resultData = make([]core.QueryResult, len(addresses))
				for i, addr := range addresses {
					resultData[i] = core.QueryResult{
						Address: addr,
						Status:  "pending",
						Balance: "",
						Error:   "",
					}
				}
				// 重置到第一页并应用筛选
				currentPage = 1
				filterMode = "all"
				filterText = ""
				filterModeSelect.SetSelected("全部")
				addressSearchEntry.SetText("")
				applyFilter()
				fyne.Do(func() {
					updatePageInfo()
					resultTable.Refresh()
				})

				statusLabel.SetText(fmt.Sprintf("已导入 %d 个地址（拖拽）", len(addresses)))
				dialog.ShowInformation("成功", fmt.Sprintf("已导入 %d 个地址\n地址已显示在右侧表格中", len(addresses)), w)
			} else {
				// 尝试作为 API Key 文件导入
				if err := keyManager.LoadKeysFromFile(filePath); err != nil {
					dialog.ShowError(fmt.Errorf("无法识别文件类型\n既不是有效的地址文件，也不是有效的 Key 文件\n地址错误: %v\nKey错误: %v", addrErr, err), w)
					continue
				}

				// Key 导入成功
				keyCount := keyManager.GetKeyCount()
				apiKeyStatusLabel.SetText(fmt.Sprintf("已加载 %d 个 API Key", keyCount))

				fyne.Do(func() {
					keyStatusTable.Refresh()
				})

				dialog.ShowInformation("成功", fmt.Sprintf("已导入 %d 个 API Key（拖拽）", keyCount), w)
			}
		}
	})

	w.Show()
}
