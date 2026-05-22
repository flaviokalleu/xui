package server

import (
	"net/http"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

type metrics struct {
	bytesStreamed    prometheus.Counter
	streamRequests   *prometheus.CounterVec
	streamErrors     *prometheus.CounterVec
	telegramAPICalls *prometheus.CounterVec
}

func newMetrics() *metrics {
	m := &metrics{
		bytesStreamed: prometheus.NewCounter(prometheus.CounterOpts{
			Namespace: "syncgo",
			Name:      "bytes_streamed_total",
			Help:      "Total bytes proxied from Telegram to HTTP clients.",
		}),
		streamRequests: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: "syncgo",
			Name:      "stream_requests_total",
			Help:      "HTTP streaming requests by status.",
		}, []string{"route", "status"}),
		streamErrors: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: "syncgo",
			Name:      "stream_errors_total",
			Help:      "Streaming errors by kind.",
		}, []string{"kind"}),
		telegramAPICalls: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: "syncgo",
			Name:      "telegram_api_calls_total",
			Help:      "Telegram API calls by method and result.",
		}, []string{"method", "result"}),
	}
	prometheus.MustRegister(m.bytesStreamed, m.streamRequests, m.streamErrors, m.telegramAPICalls)
	return m
}

func metricsHandler() http.Handler {
	return promhttp.Handler()
}
