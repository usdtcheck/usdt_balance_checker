package core

import (
	"bufio"
	"encoding/csv"
	"fmt"
	"os"
	"strings"

	"usdt-balance-checker/tron"

	"github.com/xuri/excelize/v2"
)

// LoadAddressesFromFile 从文件加载地址列表
func LoadAddressesFromFile(filepath string) ([]string, error) {
	file, err := os.Open(filepath)
	if err != nil {
		return nil, fmt.Errorf("打开文件失败: %v", err)
	}
	defer file.Close()

	addresses := make([]string, 0)
	seen := make(map[string]bool)

	// 判断文件类型
	ext := strings.ToLower(filepath[len(filepath)-4:])

	if ext == ".csv" {
		// 读取 CSV 文件
		reader := csv.NewReader(file)
		records, err := reader.ReadAll()
		if err != nil {
			return nil, fmt.Errorf("读取 CSV 失败: %v", err)
		}

		for _, record := range records {
			for _, field := range record {
				addr := strings.TrimSpace(field)
				if addr != "" && !seen[addr] {
					if tron.ValidateAddress(addr) {
						addresses = append(addresses, addr)
						seen[addr] = true
					}
				}
			}
		}
	} else {
		// 读取 TXT 文件（每行一个地址）
		scanner := bufio.NewScanner(file)
		for scanner.Scan() {
			line := strings.TrimSpace(scanner.Text())
			if line == "" {
				continue
			}

			// 支持 CSV 格式（逗号分隔）
			if strings.Contains(line, ",") {
				parts := strings.Split(line, ",")
				for _, part := range parts {
					addr := strings.TrimSpace(part)
					if addr != "" && !seen[addr] {
						if tron.ValidateAddress(addr) {
							addresses = append(addresses, addr)
							seen[addr] = true
						}
					}
				}
			} else {
				if !seen[line] {
					if tron.ValidateAddress(line) {
						addresses = append(addresses, line)
						seen[line] = true
					}
				}
			}
		}

		if err := scanner.Err(); err != nil {
			return nil, fmt.Errorf("读取文件失败: %v", err)
		}
	}

	if len(addresses) == 0 {
		return nil, fmt.Errorf("文件中没有找到有效的 TRON 地址。\nTRON 地址应该是 34 个字符，以 T 开头，并且通过校验码验证")
	}

	return addresses, nil
}

// LoadAddressesFromText 从文本加载地址（支持换行、逗号、空格分隔）
func LoadAddressesFromText(text string) ([]string, error) {
	addresses := make([]string, 0)
	seen := make(map[string]bool)

	// 按行分割
	lines := strings.Split(text, "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		// 支持多种分隔符：逗号、空格、制表符
		separators := []string{",", " ", "\t", ";"}
		parts := []string{line}

		for _, sep := range separators {
			newParts := make([]string, 0)
			for _, part := range parts {
				if strings.Contains(part, sep) {
					newParts = append(newParts, strings.Split(part, sep)...)
				} else {
					newParts = append(newParts, part)
				}
			}
			parts = newParts
		}

		for _, part := range parts {
			addr := strings.TrimSpace(part)
			if addr != "" && !seen[addr] {
				if err := tron.ValidateAddressWithError(addr); err == nil {
					addresses = append(addresses, addr)
					seen[addr] = true
				}
				// 如果验证失败，跳过该地址（已在错误信息中说明）
			}
		}
	}

	if len(addresses) == 0 {
		return nil, fmt.Errorf("没有找到有效的 TRON 地址。\nTRON 地址应该是 34 个字符，以 T 开头。\n如果地址格式正确但仍报错，可能是校验码错误（地址本身无效）")
	}

	return addresses, nil
}

// ExportToCSV 导出结果到 CSV
func ExportToCSV(results []QueryResult, filepath string) error {
	file, err := os.Create(filepath)
	if err != nil {
		return fmt.Errorf("创建文件失败: %v", err)
	}
	defer file.Close()

	writer := csv.NewWriter(file)
	defer writer.Flush()

	// 写入表头
	if err := writer.Write([]string{"地址", "余额", "状态", "错误信息"}); err != nil {
		return fmt.Errorf("写入表头失败: %v", err)
	}

	// 写入数据
	for _, result := range results {
		status := "成功"
		if result.Status == "error" {
			status = "失败"
		} else if result.Status == "cancelled" {
			status = "已取消"
		}

		balance := result.Balance
		if balance == "" {
			balance = "0.000000"
		}

		record := []string{
			result.Address,
			balance,
			status,
			result.Error,
		}

		if err := writer.Write(record); err != nil {
			return fmt.Errorf("写入数据失败: %v", err)
		}
	}

	return nil
}

// ExportToExcel 导出结果到 Excel
func ExportToExcel(results []QueryResult, filepath string) error {
	f := excelize.NewFile()
	defer func() {
		if err := f.Close(); err != nil {
			fmt.Printf("关闭文件失败: %v\n", err)
		}
	}()

	// 使用默认的 Sheet1
	sheetName := "Sheet1"
	f.SetActiveSheet(0)

	// 写入表头
	headers := []string{"地址", "余额", "状态", "错误信息"}
	for i, header := range headers {
		cell := fmt.Sprintf("%c1", 'A'+i)
		f.SetCellValue(sheetName, cell, header)
	}

	// 设置表头样式
	headerStyle, err := f.NewStyle(&excelize.Style{
		Font: &excelize.Font{Bold: true},
		Fill: excelize.Fill{Type: "pattern", Color: []string{"#E0E0E0"}, Pattern: 1},
	})
	if err == nil {
		f.SetCellStyle(sheetName, "A1", "D1", headerStyle)
	}

	// 写入数据
	for i, result := range results {
		row := i + 2

		status := "成功"
		if result.Status == "error" {
			status = "失败"
		} else if result.Status == "cancelled" {
			status = "已取消"
		}

		balance := result.Balance
		if balance == "" {
			balance = "0.000000"
		}

		f.SetCellValue(sheetName, fmt.Sprintf("A%d", row), result.Address)
		f.SetCellValue(sheetName, fmt.Sprintf("B%d", row), balance)
		f.SetCellValue(sheetName, fmt.Sprintf("C%d", row), status)
		f.SetCellValue(sheetName, fmt.Sprintf("D%d", row), result.Error)
	}

	// 设置列宽
	f.SetColWidth(sheetName, "A", "A", 50) // 地址列
	f.SetColWidth(sheetName, "B", "B", 20) // 余额列
	f.SetColWidth(sheetName, "C", "C", 10) // 状态列
	f.SetColWidth(sheetName, "D", "D", 50) // 错误信息列

	// 保存文件
	if err := f.SaveAs(filepath); err != nil {
		return fmt.Errorf("保存文件失败: %v", err)
	}

	return nil
}
