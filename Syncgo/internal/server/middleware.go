package server

import (
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"golang.org/x/time/rate"
)

// securityHeaders sets baseline browser security headers safe for video streaming.
func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "no-referrer")
		h.Set("X-Frame-Options", "SAMEORIGIN")
		h.Set("Permissions-Policy", "interest-cohort=()")
		h.Set("X-Robots-Tag", "noindex, nofollow")

		// CORS aberto para players IPTV (VLC, Kodi, ffmpeg, etc.)
		// e para o Cloudflare conseguir fazer preflight.
		h.Set("Access-Control-Allow-Origin", "*")
		h.Set("Access-Control-Allow-Methods", "GET, HEAD, OPTIONS")
		h.Set("Access-Control-Allow-Headers", "Range, If-None-Match")
		h.Set("Access-Control-Expose-Headers", "Content-Length, Content-Range, ETag, Accept-Ranges")

		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

type ipLimiter struct {
	mu        sync.Mutex
	limiters  map[string]*rate.Limiter
	rps       rate.Limit
	burst     int
	lastClean time.Time
	last      map[string]time.Time
}

func newIPLimiter(perMinute int) *ipLimiter {
	return &ipLimiter{
		limiters: make(map[string]*rate.Limiter),
		last:     make(map[string]time.Time),
		rps:      rate.Limit(float64(perMinute) / 60),
		burst:    perMinute / 4,
	}
}

func (l *ipLimiter) get(ip string) *rate.Limiter {
	l.mu.Lock()
	defer l.mu.Unlock()
	if time.Since(l.lastClean) > 5*time.Minute {
		cutoff := time.Now().Add(-10 * time.Minute)
		for ip, t := range l.last {
			if t.Before(cutoff) {
				delete(l.limiters, ip)
				delete(l.last, ip)
			}
		}
		l.lastClean = time.Now()
	}
	lim, ok := l.limiters[ip]
	if !ok {
		lim = rate.NewLimiter(l.rps, l.burst)
		l.limiters[ip] = lim
	}
	l.last[ip] = time.Now()
	return lim
}

// rateLimit denies requests above perMinute per client IP.
// Disabled when perMinute <= 0.
func rateLimit(perMinute int, next http.Handler) http.Handler {
	if perMinute <= 0 {
		return next
	}
	lim := newIPLimiter(perMinute)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ip := clientIP(r)
		if !lim.get(ip).Allow() {
			w.Header().Set("Retry-After", "60")
			http.Error(w, "rate limit exceeded", http.StatusTooManyRequests)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// clientIP extrai o IP real do cliente respeitando proxies confiáveis.
// Prioridade: CF-Connecting-IP (Cloudflare) > X-Real-IP > X-Forwarded-For > RemoteAddr.
// CF-Connecting-IP é injetado pelo Cloudflare e não pode ser falsificado pelo cliente.
func clientIP(r *http.Request) string {
	// Cloudflare injeta o IP real do visitante neste header.
	if cfIP := r.Header.Get("CF-Connecting-IP"); cfIP != "" {
		return strings.TrimSpace(cfIP)
	}
	if xrip := r.Header.Get("X-Real-IP"); xrip != "" {
		return strings.TrimSpace(xrip)
	}
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		// Pega o primeiro IP da lista (mais próximo do cliente).
		if comma := strings.IndexByte(xff, ','); comma > 0 {
			return strings.TrimSpace(xff[:comma])
		}
		return strings.TrimSpace(xff)
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}
