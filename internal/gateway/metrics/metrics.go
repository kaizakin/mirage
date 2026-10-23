// Package metrics exposes the gateway's tool calls and upstream health to Prometheus.
//
//	mirage_tool_calls_total{server, tool, outcome}        every tool call, by how it ended
//	mirage_tool_call_duration_seconds{server, outcome}    how long calls took
//	mirage_upstream_up{server}                            1 while connected, 0 otherwise
//	mirage_upstream_status{server, status}                1 for the upstream's current status
//	mirage_upstream_tools{server}                         tools the upstream offers
package metrics

import (
	"context"
	"net/http"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/kaizakin/mirage/internal/gateway/audit"
	"github.com/kaizakin/mirage/internal/gateway/upstream"
)

// Snapshotter reports each upstream's current state.
type Snapshotter interface {
	Snapshot() []upstream.State
}

// Metrics owns the gateway's registry.
type Metrics struct {
	registry *prometheus.Registry
	calls    *prometheus.CounterVec
	duration *prometheus.HistogramVec
}

// New returns Metrics that read upstream health from ups at scrape time.
func New(ups Snapshotter) *Metrics {
	m := &Metrics{
		registry: prometheus.NewRegistry(),
		calls: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "mirage_tool_calls_total", Help: "Tool calls through the gateway, by server, tool, and outcome.",
		}, []string{"server", "tool", "outcome"}),
		duration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name: "mirage_tool_call_duration_seconds", Help: "Tool call latency, by server and outcome.",
			Buckets: []float64{.01, .05, .1, .25, .5, 1, 2.5, 5, 10, 20},
		}, []string{"server", "outcome"}),
	}
	m.registry.MustRegister(m.calls, m.duration, upstreams{ups},
		collectors.NewGoCollector(), collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}))
	return m
}

// Handler serves the metrics in Prometheus's format.
func (m *Metrics) Handler() http.Handler {
	return promhttp.HandlerFor(m.registry, promhttp.HandlerOpts{})
}

// Recorder counts every call it records, then passes it to next. Wrapping the audit
// recorder means everything audited is also measured, with no second code path.
func (m *Metrics) Recorder(next audit.Recorder) audit.Recorder {
	return recorder{m: m, next: next}
}

type recorder struct {
	m    *Metrics
	next audit.Recorder
}

func (r recorder) RecordCall(ctx context.Context, e audit.Entry) error {
	r.m.calls.WithLabelValues(e.ServerSlug, e.Tool, string(e.Outcome)).Inc()
	r.m.duration.WithLabelValues(e.ServerSlug, string(e.Outcome)).Observe(e.Duration.Seconds())
	return r.next.RecordCall(ctx, e)
}

// upstreams reports upstream health as it is at scrape time.
type upstreams struct{ s Snapshotter }

var (
	upDesc     = prometheus.NewDesc("mirage_upstream_up", "1 while the upstream is connected.", []string{"server"}, nil)
	statusDesc = prometheus.NewDesc("mirage_upstream_status", "1 for the upstream's current status.", []string{"server", "status"}, nil)
	toolsDesc  = prometheus.NewDesc("mirage_upstream_tools", "Tools the upstream offers.", []string{"server"}, nil)
)

func (upstreams) Describe(ch chan<- *prometheus.Desc) {
	ch <- upDesc
	ch <- statusDesc
	ch <- toolsDesc
}

func (u upstreams) Collect(ch chan<- prometheus.Metric) {
	for _, st := range u.s.Snapshot() {
		up := 0.0
		if st.Status == upstream.Connected {
			up = 1
		}
		ch <- prometheus.MustNewConstMetric(upDesc, prometheus.GaugeValue, up, st.Slug)
		ch <- prometheus.MustNewConstMetric(statusDesc, prometheus.GaugeValue, 1, st.Slug, string(st.Status))
		ch <- prometheus.MustNewConstMetric(toolsDesc, prometheus.GaugeValue, float64(len(st.Tools)), st.Slug)
	}
}
