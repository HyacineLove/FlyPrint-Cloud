package business

import "errors"

// ErrInvalidPrintQuotaInput 表示页数、份数或打印模式无法参与额度计算。
var ErrInvalidPrintQuotaInput = errors.New("invalid print quota input")

// QuotaUsage 按实体纸张数计算一次完整打印需要的额度点数。
func QuotaUsage(pageCount, copies int, duplexMode, colorMode string) (int, int, error) {
	if pageCount < 1 || copies < 1 {
		return 0, 0, ErrInvalidPrintQuotaInput
	}

	sheetsPerCopy := pageCount
	switch duplexMode {
	case "simplex":
	case "longedge", "shortedge":
		sheetsPerCopy = (pageCount + 1) / 2
	default:
		return 0, 0, ErrInvalidPrintQuotaInput
	}

	multiplier := 1
	switch colorMode {
	case "mono":
	case "color":
		multiplier = 2
	default:
		return 0, 0, ErrInvalidPrintQuotaInput
	}

	sheets := sheetsPerCopy * copies
	return sheets, sheets * multiplier, nil
}

// SettledQuotaUsage 将 IPP 已完成印面数换算为实体纸张和实际额度。
// 双面打印按每份文件分别取整，奇数页不会跨份合并到同一张纸。
func SettledQuotaUsage(pageCount, copies, impressionsCompleted int, duplexMode, colorMode string) (int, int, error) {
	if pageCount < 1 || copies < 1 || impressionsCompleted < 0 ||
		impressionsCompleted > pageCount*copies {
		return 0, 0, ErrInvalidPrintQuotaInput
	}

	if duplexMode != "simplex" && duplexMode != "longedge" && duplexMode != "shortedge" {
		return 0, 0, ErrInvalidPrintQuotaInput
	}
	if colorMode != "mono" && colorMode != "color" {
		return 0, 0, ErrInvalidPrintQuotaInput
	}

	sheets := impressionsCompleted
	if duplexMode != "simplex" {
		completeCopies := impressionsCompleted / pageCount
		remainingImpressions := impressionsCompleted % pageCount
		sheets = completeCopies*((pageCount+1)/2) + (remainingImpressions+1)/2
	}

	multiplier := 1
	if colorMode == "color" {
		multiplier = 2
	}
	return sheets, sheets * multiplier, nil
}
