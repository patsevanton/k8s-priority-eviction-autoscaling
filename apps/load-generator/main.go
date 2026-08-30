package main

import (
	"context"
	"io"
	"log"
	"math"
	"math/rand"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"
)

const (
	minRPS  = 0
	maxRPS  = 100
	modeRPS = 60
)

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func envDurationOr(key string, def time.Duration) time.Duration {
	if v := os.Getenv(key); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			return d
		}
	}
	return def
}

// rpsAt возвращает целевой RPS в момент elapsed после старта.
// RPS плавно растёт от minRPS до maxRPS по S-образной кривой — обратной
// функции распределения закона Симпсона (треугольное распределение, мода modeRPS):
// медленно в начале, быстрее в середине, снова медленно к концу.
// После CYCLE удерживается максимум maxRPS.
func rpsAt(elapsed, cycle time.Duration) float64 {
	if cycle <= 0 {
		return maxRPS
	}
	u := float64(elapsed) / float64(cycle)
	if u >= 1 {
		return maxRPS
	}
	mode := float64(modeRPS-minRPS) / float64(maxRPS-minRPS)
	if u <= mode {
		return minRPS + math.Sqrt(u*float64((maxRPS-minRPS)*(modeRPS-minRPS)))
	}
	return maxRPS - math.Sqrt((1-u)*float64((maxRPS-minRPS)*(maxRPS-modeRPS)))
}

type stats struct {
	mu      sync.Mutex
	total   int
	success int
	failed  int
}

func (s *stats) record(ok bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.total++
	if ok {
		s.success++
	} else {
		s.failed++
	}
}

func (s *stats) snapshot() (total, success, failed int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.total, s.success, s.failed
}

func send(ctx context.Context, client *http.Client, targetURL string, s *stats) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, targetURL, nil)
	if err != nil {
		s.record(false)
		return
	}
	resp, err := client.Do(req)
	if err != nil {
		s.record(false)
		return
	}
	io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	s.record(resp.StatusCode >= 200 && resp.StatusCode < 400)
}

func main() {
	targetURL := envOr("TARGET_URL", "http://business-app:8080/")
	cycle := envDurationOr("CYCLE", 20*time.Minute)

	client := &http.Client{Timeout: envDurationOr("REQUEST_TIMEOUT", 10*time.Second)}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	log.Printf("load-generator: цель=%s cycle=%s minRPS=%d maxRPS=%d modeRPS=%d", targetURL, cycle, minRPS, maxRPS, modeRPS)

	s := &stats{}
	start := time.Now()
	var lastTick time.Time
	for ctx.Err() == nil {
		rps := rpsAt(time.Since(start), cycle)
		if rps <= 0 {
			select {
			case <-time.After(time.Second):
			case <-ctx.Done():
			}
			continue
		}

		interval := time.Duration(float64(time.Second) / rps)
		if lastTick.IsZero() {
			lastTick = time.Now()
		}

		// Ровный темп отправки со случайным джиттером ±20%,
		// чтобы трафик выглядел как реальный, а не метроном.
		jitter := interval / 5
		delay := interval + time.Duration(rand.Int63n(int64(2*jitter)+1)) - jitter
		if wait := time.Until(lastTick.Add(delay)); wait > 0 {
			select {
			case <-time.After(wait):
			case <-ctx.Done():
			}
		}
		lastTick = time.Now()

		go send(ctx, client, targetURL, s)
	}

	total, success, failed := s.snapshot()
	log.Printf("получен сигнал завершения — останавливаемся: запросов=%d успех=%d ошибок=%d", total, success, failed)
}
