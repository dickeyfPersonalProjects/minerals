package api

import (
	"crypto/rand"
	"log/slog"
	"net"
	"net/http"
	"runtime/debug"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/oklog/ulid/v2"

	"github.com/dickeyfPersonalProjects/minerals/internal/auth"
)

// RequestID is the entry middleware: it stamps every request with a
// ULID, propagates it through context, and echoes it as the
// X-Request-Id response header. Inbound X-Request-Id values are
// honored if they parse as a ULID (per CONTRACT.md §14).
func RequestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := strings.TrimSpace(r.Header.Get("X-Request-Id"))
		if id != "" {
			if _, err := ulid.Parse(id); err != nil {
				id = newULID()
			}
		} else {
			id = newULID()
		}
		w.Header().Set("X-Request-Id", id)
		ctx := auth.WithRequestID(r.Context(), id)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func newULID() string {
	return ulid.MustNew(ulid.Timestamp(time.Now()), rand.Reader).String()
}

// loggingResponseWriter captures status and bytes for the per-request
// log line.
type loggingResponseWriter struct {
	http.ResponseWriter
	status    int
	bytes     int
	wroteHead bool
}

func (l *loggingResponseWriter) WriteHeader(code int) {
	if !l.wroteHead {
		l.status = code
		l.wroteHead = true
		l.ResponseWriter.WriteHeader(code)
	}
}

func (l *loggingResponseWriter) Write(b []byte) (int, error) {
	if !l.wroteHead {
		l.status = http.StatusOK
		l.wroteHead = true
	}
	n, err := l.ResponseWriter.Write(b)
	l.bytes += n
	return n, err
}

// Logging emits exactly one structured log line per request with the
// mandatory §14 fields. Levels: 2xx/3xx → info, 4xx → warn, 5xx →
// error.
func Logging(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		lw := &loggingResponseWriter{ResponseWriter: w, status: http.StatusOK}

		next.ServeHTTP(lw, r)

		dur := time.Since(start).Milliseconds()
		ctx := r.Context()
		user := auth.FromContext(ctx)
		userID := ""
		if user.ID != uuid.Nil {
			userID = user.ID.String()
		}
		attrs := []any{
			slog.String("request_id", auth.RequestID(ctx)),
			slog.String("method", r.Method),
			slog.String("path", r.URL.Path),
			slog.Int("status", lw.status),
			slog.Int64("duration_ms", dur),
			slog.String("user_id", userID),
			slog.Int("bytes_out", lw.bytes),
			slog.String("remote_ip", remoteIP(r)),
		}
		switch {
		case lw.status >= 500:
			slog.LogAttrs(ctx, slog.LevelError, "http request", toAttrs(attrs)...)
		case lw.status >= 400:
			slog.LogAttrs(ctx, slog.LevelWarn, "http request", toAttrs(attrs)...)
		default:
			slog.LogAttrs(ctx, slog.LevelInfo, "http request", toAttrs(attrs)...)
		}
	})
}

func toAttrs(in []any) []slog.Attr {
	out := make([]slog.Attr, 0, len(in))
	for _, v := range in {
		if a, ok := v.(slog.Attr); ok {
			out = append(out, a)
		}
	}
	return out
}

// remoteIP returns the best guess for the client IP (per §14). The
// X-Forwarded-For header is honored for log-attribution only — never
// for security decisions (per §17).
func remoteIP(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		if i := strings.IndexByte(xff, ','); i > 0 {
			return strings.TrimSpace(xff[:i])
		}
		return strings.TrimSpace(xff)
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// Recovery turns panics into a 500 with the §10 error envelope and
// logs the full stack at error level.
func Recovery(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if rec := recover(); rec != nil {
				slog.LogAttrs(r.Context(), slog.LevelError, "panic recovered",
					slog.Any("panic", rec),
					slog.String("stack", string(debug.Stack())),
					slog.String("request_id", auth.RequestID(r.Context())),
					slog.String("path", r.URL.Path),
				)
				writeError(w, http.StatusInternalServerError,
					"internal_error", "internal server error", nil)
			}
		}()
		next.ServeHTTP(w, r)
	})
}

// SecurityHeaders applies the §17 baseline response headers to every
// response. HSTS is conditional on X-Forwarded-Proto being "https"
// (set by the production ingress).
func SecurityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "no-referrer")
		h.Set("Permissions-Policy",
			"accelerometer=(), camera=(), geolocation=(), microphone=(), payment=(), usb=()")
		if strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https") {
			h.Set("Strict-Transport-Security", "max-age=63072000; includeSubDomains")
		}
		next.ServeHTTP(w, r)
	})
}

// csp is the §17 Content-Security-Policy applied to every response.
const csp = "default-src 'self'; " +
	"script-src 'self'; " +
	"style-src 'self' 'unsafe-inline'; " +
	"img-src 'self' data:; " +
	"font-src 'self'; " +
	"connect-src 'self'; " +
	"frame-ancestors 'none'; " +
	"base-uri 'self'; " +
	"form-action 'self'"

// CSP applies the §17 Content-Security-Policy. Kept separate from
// SecurityHeaders so a future per-route exception (e.g. relaxed CSP
// for /docs) is a one-line override without touching the rest.
func CSP(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Security-Policy", csp)
		next.ServeHTTP(w, r)
	})
}

// Chain composes middlewares right-to-left so the first listed runs
// outermost: Chain(h, A, B, C) == A(B(C(h))).
func Chain(h http.Handler, mws ...func(http.Handler) http.Handler) http.Handler {
	for i := len(mws) - 1; i >= 0; i-- {
		h = mws[i](h)
	}
	return h
}
