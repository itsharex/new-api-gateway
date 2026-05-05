package evidence

import "github.com/prometheus/client_golang/prometheus"

var storeOpsTotal = prometheus.NewCounterVec(
	prometheus.CounterOpts{
		Name: "evidence_store_ops_total",
		Help: "Total evidence store operations by backend, operation, and status.",
	},
	[]string{"backend", "operation", "status"},
)

func init() {
	prometheus.MustRegister(storeOpsTotal)
}
