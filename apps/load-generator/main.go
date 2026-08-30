package main

import (
	"context"
	"io"
	"log"
	"math/rand"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"sync"
	"syscall"
	"time"
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

func envFloatOr(key string, def float64) float64 {
	if v := os.Getenv(key); v != "" {
		if f, err := strconv.ParseFloat(v, 64); err == nil {
			return f
		}
	}
	return def
}

// smoothstep — гладкая ступенька 0→1 без излома в начале и конце.
func smoothstep(t float64) float64 {
	if t <= 0 {
		return 0
	}
	if t >= 1 {
		return 1
	}
	return 6*t*t*t*t*t - 15*t*t*t*t + 10*t*t*t
}

// rpsAt возвращает целевой RPS в момент elapsed после старта.
// RPS плавно растёт от minRPS до maxRPS по гладкой S-образной кривой smoothstep
// (без излома): медленно в начале, быстрее в середине, снова медленно к концу.
// midpoint задаёт точку перегиба в долях от CYCLE (0.5 — симметричный подъём:
// 50% подъёма ровно в середине; меньше 0.5 — ранний подъём, больше 0.5 — поздний).
// После CYCLE удерживается максимум maxRPS.
func rpsAt(elapsed, cycle time.Duration, minRPS, maxRPS, midpoint float64) float64 {
	if cycle <= 0 {
		return maxRPS
	}
	u := float64(elapsed) / float64(cycle)
	if u >= 1 {
		return maxRPS
	}
	if u <= midpoint {
		if midpoint <= 0 {
			return minRPS
		}
		return minRPS + (maxRPS-minRPS)*0.5*smoothstep(u/midpoint)
	}
	if midpoint >= 1 {
		return minRPS
	}
	return minRPS + (maxRPS-minRPS)*0.5*(1+smoothstep((u-midpoint)/(1-midpoint)))
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
	cycle := envDurationOr("CYCLE", 30*time.Minute)
	minRPS := envFloatOr("MIN_RPS", 0)
	maxRPS := envFloatOr("MAX_RPS", 600)
	midpoint := envFloatOr("MIDPOINT", 0.5)

	client := &http.Client{Timeout: envDurationOr("REQUEST_TIMEOUT", 10*time.Second)}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	log.Printf("load-generator: цель=%s cycle=%s minRPS=%v maxRPS=%v midpoint=%v", targetURL, cycle, minRPS, maxRPS, midpoint)

	s := &stats{}
	start := time.Now()
	var lastTick time.Time
	for ctx.Err() == nil {
		rps := rpsAt(time.Since(start), cycle, minRPS, maxRPS, midpoint)
		if rps < 1 {
			select {
			case <-time.After(time.Second):
			case <-ctx.Done():
			}
			continue
		}

		interval := time.Duration(float64(time.Second) / rps)
		if interval > time.Second {
			interval = time.Second
		}
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
