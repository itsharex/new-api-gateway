package admin

import "strings"

func FormatAnomalyDisplayReasonZH(item AnomalySummary) string {
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
	default:
		return item.Reason
	}
}

func formatAnomalyDisplayNumber(value string) string {
	if value == "" {
		return value
	}
	sign := ""
	digits := value
	if strings.HasPrefix(digits, "-") {
		sign = "-"
		digits = digits[1:]
	}
	if digits == "" {
		return value
	}
	for _, r := range digits {
		if r < '0' || r > '9' {
			return value
		}
	}
	if len(digits) <= 3 {
		return sign + digits
	}

	var b strings.Builder
	b.Grow(len(value) + len(digits)/3)
	b.WriteString(sign)
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
