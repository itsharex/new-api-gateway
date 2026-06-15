package admin

import "strings"

func formatAnomalyDisplayReasonZH(item AnomalySummary) string {
	observed := formatAnomalyDisplayNumber(item.ObservedValue)
	threshold := formatAnomalyDisplayNumber(item.ThresholdValue)

	switch item.AnomalyType {
	case "high_trace_tokens":
		return "本次请求有效 token 消耗 " + observed + "，超过阈值 " + threshold + "。"
	case "long_output_anomaly":
		return "本次输出 token 为 " + observed + "，超过阈值 " + threshold + "。"
	case "off_hours_high_usage":
		return "夜间时段（23:00-07:00）本次有效 token 消耗 " + observed + "，超过阈值 " + threshold + "。"
	case "non_work_use":
		return "检测到明确非工作用途内容。"
	case "multivariate_anomaly":
		return "多变量异常检测标记本次请求为异常（Isolation Forest）。"
	default:
		return item.Reason
	}
}

func formatAnomalyDisplayNumber(value string) string {
	if value == "" {
		return value
	}
	sign := ""
	rest := value
	if strings.HasPrefix(rest, "-") || strings.HasPrefix(rest, "+") {
		sign = rest[:1]
		rest = rest[1:]
	}
	if rest == "" {
		return value
	}

	parts := strings.Split(rest, ".")
	if len(parts) > 2 {
		return value
	}
	intPart := parts[0]
	if intPart == "" || !isDecimalDigits(intPart) {
		return value
	}

	fracPart := ""
	if len(parts) == 2 {
		fracPart = parts[1]
		if fracPart != "" && !isDecimalDigits(fracPart) {
			return value
		}
		fracPart = strings.TrimRight(fracPart, "0")
	}

	groupedInt := groupDecimalDigits(intPart)
	if fracPart == "" {
		return sign + groupedInt
	}
	return sign + groupedInt + "." + fracPart
}

func groupDecimalDigits(digits string) string {
	var b strings.Builder
	b.Grow(len(digits) + len(digits)/3)
	head := len(digits) % 3
	if head == 0 {
		head = 3
	}
	b.WriteString(digits[:head])
	for i := head; i < len(digits); i += 3 {
		b.WriteByte(',')
		b.WriteString(digits[i : i+3])
	}
	return b.String()
}

func isDecimalDigits(value string) bool {
	for _, r := range value {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}
