package main

import (
	"context"
	"encoding/json"
	"log"
	"math/rand"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

var (
	httpRequestsTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "business_app_http_requests_total",
			Help: "Общее количество HTTP-запросов по коду ответа и роуту",
		},
		[]string{"code", "route"},
	)
	httpRequestDuration = promauto.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "business_app_http_request_duration_seconds",
			Help:    "Длительность обработки HTTP-запросов",
			Buckets: []float64{0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5},
		},
		[]string{"route"},
	)
)

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func envIntOr(key string, def int) int {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return def
}

type handler struct {
	latency time.Duration
}

func (h *handler) observe(route string, start time.Time, code int) {
	httpRequestsTotal.WithLabelValues(strconv.Itoa(code), route).Inc()
	httpRequestDuration.WithLabelValues(route).Observe(time.Since(start).Seconds())
}

func (h *handler) handleRoot(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	route := "root"

	if r.URL.Path != "/" {
		h.observe(route, start, http.StatusNotFound)
		http.NotFound(w, r)
		return
	}

	// Имитация полезной работы CPU-bound обработчика бизнес-логики.
	jitter := time.Duration(rand.Int63n(int64(h.latency) + 1))
	time.Sleep(jitter)

	h.observe(route, start, http.StatusOK)
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "ok", "app": "business-app"})
}

func main() {
	h := &handler{latency: time.Duration(envIntOr("REQUEST_LATENCY_MS", 30)) * time.Millisecond}

	mux := http.NewServeMux()
	mux.HandleFunc("/", h.handleRoot)
	mux.Handle("/metrics", promhttp.Handler())

	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("ok"))
	})
	mux.HandleFunc("/readyz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("ok"))
	})

	addr := envOr("LISTEN_ADDR", ":8080")
	srv := &http.Server{
		Addr:              addr,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	errCh := make(chan error, 1)
	go func() {
		log.Printf("business-app слушает %s (latency=%s)", addr, h.latency)
		errCh <- srv.ListenAndServe()
	}()

	select {
	case err := <-errCh:
		if err != nil && err != http.ErrServerClosed {
			log.Fatalf("http server: %v", err)
		}
	case <-ctx.Done():
		log.Printf("получен сигнал завершения — останавливаемся")
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := srv.Shutdown(shutdownCtx); err != nil {
			log.Printf("graceful shutdown: %v", err)
		}
	}
}
