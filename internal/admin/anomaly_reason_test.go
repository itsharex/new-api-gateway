package admin

import "testing"

func TestFormatAnomalyDisplayReasonZH(t *testing.T) {
	tests := []struct {
		name string
		item AnomalySummary
		want string
	}{
		{
			name: "high trace tokens",
			item: AnomalySummary{
				AnomalyType:    "high_trace_tokens",
				ObservedValue:  "48200",
				ThresholdValue: "40000",
				Reason:         "raw reason should stay unchanged",
			},
			want: "本次请求有效 token 消耗 48,200，超过阈值 40,000。",
		},
		{
			name: "long output anomaly",
			item: AnomalySummary{
				AnomalyType:    "long_output_anomaly",
				ObservedValue:  "18300",
				ThresholdValue: "16000",
				Reason:         "raw reason should stay unchanged",
			},
			want: "本次输出 token 为 18,300，超过阈值 16,000。",
		},
		{
			name: "off hours high usage",
			item: AnomalySummary{
				AnomalyType:    "off_hours_high_usage",
				ObservedValue:  "22500",
				ThresholdValue: "20000",
				Reason:         "raw reason should stay unchanged",
			},
			want: "夜间时段（23:00-07:00）本次有效 token 消耗 22,500，超过阈值 20,000。",
		},
		{
			name: "non work use",
			item: AnomalySummary{
				AnomalyType: "non_work_use",
				Reason:      "raw reason should stay unchanged",
			},
			want: "检测到明确非工作用途内容。",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := FormatAnomalyDisplayReasonZH(tt.item); got != tt.want {
				t.Fatalf("FormatAnomalyDisplayReasonZH() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestFormatAnomalyDisplayReasonZHFallsBackForUnknownType(t *testing.T) {
	item := AnomalySummary{
		AnomalyType:    "unexpected_type",
		ObservedValue:  "7",
		ThresholdValue: "3",
		Reason:         "keep the original reason",
	}

	if got := FormatAnomalyDisplayReasonZH(item); got != item.Reason {
		t.Fatalf("FormatAnomalyDisplayReasonZH() = %q, want fallback %q", got, item.Reason)
	}
}
