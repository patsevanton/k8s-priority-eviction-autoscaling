package main

import (
	"context"
	"fmt"
	"io"
	"log"
	"math/rand"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
)

// step — одна ступень лестницы нагрузки: целевой RPS на duration.
type step struct {
	rps      int
	duration time.Duration
}

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

func envDurationOr(key string, def time.Duration) time.Duration {
	if v := os.Getenv(key); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			return d
		}
	}
	return def
}

// parseSteps разбирает профиль лестницы в формате "rps/длительность;rps/длительность;..."
// Например: "0/2m;5/3m;20/3m;60/3m;100/5m;20/3m;0/2m" — ночь, рост, пик, спад, ночь.
func parseSteps(s string) ([]step, error) {
	var steps []step
	for _, part := range strings.Split(s, ";") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		kv := strings.SplitN(part, "/", 2)
		if len(kv) != 2 {
			return nil, fmt.Errorf("неверный формат ступени %q: ожидается rps/длительность", part)
		}
		rps, err := strconv.Atoi(strings.TrimSpace(kv[0]))
		if err != nil {
			return nil, fmt.Errorf("неверный RPS в ступени %q: %w", part, err)
		}
		d, err := time.ParseDuration(strings.TrimSpace(kv[1]))
		if err != nil {
			return nil, fmt.Errorf("неверная длительность в ступени %q: %w", part, err)
		}
		steps = append(steps, step{rps: rps, duration: d})
	}
	if len(steps) == 0 {
		return nil, fmt.Errorf("пустой профиль нагрузки")
	}
	return steps, nil
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

func runStep(ctx context.Context, client *http.Client, targetURL string, st step, s *stats) {
	if st.rps <= 0 {
		log.Printf("ступень: 0 RPS (%s) — пауза, ночь", st.duration)
		select {
		case <-time.After(st.duration):
		case <-ctx.Done():
		}
		return
	}

	interval := time.Second / time.Duration(st.rps)
	log.Printf("ступень: %d RPS на %s (интервал между запросами %s)", st.rps, st.duration, interval)

	stepStart := time.Now()
	var lastTick time.Time
	for {
		if ctx.Err() != nil {
			return
		}
		if time.Since(stepStart) >= st.duration {
			return
		}

		// Ровный темп отправки со случайным джиттером ±20%,
		// чтобы трафик выглядел как реальный, а не метроном.
		if lastTick.IsZero() {
			lastTick = time.Now()
		}
		jitter := interval / 5
		delay := interval + time.Duration(rand.Int63n(int64(2*jitter)+1)) - jitter
		if wait := time.Until(lastTick.Add(delay)); wait > 0 {
			select {
			case <-time.After(wait):
			case <-ctx.Done():
				return
			}
		}
		lastTick = time.Now()

		go func() {
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
		}()
	}
}

func main() {
	targetURL := envOr("TARGET_URL", "http://business-app:8080/")
	profile := envOr("LOAD_PROFILE", "0/2m;5/3m;20/3m;60/3m;100/5m;20/3m;0/2m")
	repeat := envIntOr("REPEAT", 0) // 0 — бесконечно

	steps, err := parseSteps(profile)
	if err != nil {
		log.Fatalf("LOAD_PROFILE: %v", err)
	}

	client := &http.Client{Timeout: envDurationOr("REQUEST_TIMEOUT", 10*time.Second)}

	log.Printf("load-generator: цель=%s профиль=%q repeat=%d", targetURL, profile, repeat)
	iteration := 0
	for {
		iteration++
		s := &stats{}
		for _, st := range steps {
			ctx, cancel := context.WithTimeout(context.Background(), st.duration+time.Minute)
			runStep(ctx, client, targetURL, st, s)
			cancel()
		}
		total, success, failed := s.snapshot()
		log.Printf("итерация %d завершена: запросов=%d успех=%d ошибок=%d", iteration, total, success, failed)
		if repeat > 0 && iteration >= repeat {
			log.Printf("достигнут лимит итераций (%d) — завершаемся", repeat)
			return
		}
	}
}
