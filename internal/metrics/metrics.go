package metrics

import (
	"net/http"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

type Registry struct {
	reg      *prometheus.Registry
	Requests *prometheus.CounterVec
	Latency  *prometheus.HistogramVec
	Runs     *prometheus.CounterVec
}

func New() *Registry {
	reg := prometheus.NewRegistry()
	requests := prometheus.NewCounterVec(prometheus.CounterOpts{Name: "agentgate_tool_requests_total", Help: "Logical gateway decisions recorded by the workspace."}, []string{"service", "decision"})
	latency := prometheus.NewHistogramVec(prometheus.HistogramOpts{Name: "agentgate_tool_latency_seconds", Help: "Gateway duration including downstream time.", Buckets: []float64{0.01, 0.05, 0.1, 0.25, 0.5, 1, 2, 5}}, []string{"service"})
	runs := prometheus.NewCounterVec(prometheus.CounterOpts{Name: "agentgate_model_runs_total", Help: "Agent runs by terminal status."}, []string{"outcome"})
	reg.MustRegister(requests, latency, runs)
	return &Registry{reg: reg, Requests: requests, Latency: latency, Runs: runs}
}

func (r *Registry) Handler() http.Handler { return promhttp.HandlerFor(r.reg, promhttp.HandlerOpts{}) }
