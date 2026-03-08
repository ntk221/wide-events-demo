package main

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"math/rand"
	"net/http"
	"os"
	"sync"
	"time"

	"github.com/google/uuid"
	amqp "github.com/rabbitmq/amqp091-go"
)

var logger = slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
	Level: slog.LevelInfo,
}))

// eventAttrs holds wide event fields accumulated during request processing.
type eventAttrs struct {
	mu    sync.Mutex
	attrs []slog.Attr
}

func (e *eventAttrs) Add(attrs ...slog.Attr) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.attrs = append(e.attrs, attrs...)
}

type eventKey struct{}

func goSlowP() bool {
	return rand.Float64()*100 > 95
}

// WideEventMiddleware creates the event, attaches it to context, and emits it after the handler runs.
func WideEventMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		traceID := uuid.New().String()
		event := &eventAttrs{}
		event.Add(
			slog.String("service", "tier1"),
			slog.String("trace_id", traceID),
			slog.String("route", r.URL.Path),
		)
		ctx := context.WithValue(r.Context(), eventKey{}, event)
		ctx = context.WithValue(ctx, traceIDKey{}, traceID)

		next.ServeHTTP(w, r.WithContext(ctx))

		durationMs := time.Since(start).Milliseconds()
		event.Add(slog.Int64("duration_ms", durationMs))

		event.mu.Lock()
		hasError := false
		for _, a := range event.attrs {
			if a.Key == "error" && a.Value.Bool() {
				hasError = true
				break
			}
		}
		level := slog.LevelInfo
		if hasError {
			level = slog.LevelError
		}
		args := make([]any, len(event.attrs))
		for i, a := range event.attrs {
			args[i] = a
		}
		event.mu.Unlock()

		logger.Log(context.Background(), level, "request", args...)
	})
}

type traceIDKey struct{}

var (
	tier2URL string
	queueURL string
	saasURL  string
	amqpConn *amqp.Connection
	amqpCh   *amqp.Channel
)

func main() {
	tier2URL = envOrDefault("TIER2_URL", "http://localhost:8400")
	queueURL = envOrDefault("QUEUE_URL", "amqp://guest:guest@localhost:5672/")
	saasURL = envOrDefault("SAAS_URL", "https://www.githubstatus.com/api/v2/status.json")

	connectQueue()

	mux := http.NewServeMux()
	mux.HandleFunc("/fast", handleRequest)
	mux.HandleFunc("/slow", handleRequest)
	mux.HandleFunc("/random", handleRequest)
	mux.HandleFunc("/error", handleRequest)
	mux.HandleFunc("/healthcheck", handleHealthcheck)

	handler := WideEventMiddleware(mux)

	port := envOrDefault("PORT", "8080")
	logger.Info("tier1 starting on :" + port)
	if err := http.ListenAndServe(":"+port, handler); err != nil {
		logger.Error("server failed", "error", err)
		os.Exit(1)
	}
}

func connectQueue() {
	for i := 0; i < 30; i++ {
		conn, err := amqp.Dial(queueURL)
		if err == nil {
			ch, err := conn.Channel()
			if err == nil {
				_, err = ch.QueueDeclare("test", true, false, false, false, nil)
				if err == nil {
					amqpConn = conn
					amqpCh = ch
					logger.Info("connected to RabbitMQ")
					return
				}
				ch.Close()
			}
			conn.Close()
		}
		logger.Info("waiting for RabbitMQ...", "attempt", i+1)
		time.Sleep(2 * time.Second)
	}
	logger.Error("failed to connect to RabbitMQ after retries")
	os.Exit(1)
}

func handleRequest(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	event := ctx.Value(eventKey{}).(*eventAttrs)
	traceID := ctx.Value(traceIDKey{}).(string)
	route := r.URL.Path

	slow := goSlowP()
	if route == "/slow" {
		slow = true
	}
	event.Add(slog.Bool("was_slow", slow))

	if slow {
		time.Sleep(1500 * time.Millisecond)
	}

	hasError := false

	// 1. Queue
	doQueue(ctx, traceID, route, &hasError)

	// 2. SaaS
	doSaas(ctx, &hasError)

	// 3. tier2
	tier2Path := "/"
	if route == "/error" {
		tier2Path = "/error"
	}
	doTier2(ctx, traceID, tier2Path, &hasError)

	if hasError {
		event.Add(slog.Bool("error", true))
		w.WriteHeader(http.StatusInternalServerError)
		fmt.Fprintf(w, "error\n")
	} else {
		w.WriteHeader(http.StatusOK)
		fmt.Fprintf(w, "ok\n")
	}
}

func doQueue(ctx context.Context, traceID, route string, hasError *bool) {
	start := time.Now()
	event := ctx.Value(eventKey{}).(*eventAttrs)

	body := fmt.Sprintf("%s %s %d.%03d",
		route, traceID,
		time.Now().Unix(), time.Now().Nanosecond()/1e6)

	err := amqpCh.PublishWithContext(ctx, "", "test", false, false, amqp.Publishing{
		ContentType: "text/plain",
		Body:        []byte(body),
	})

	durationMs := time.Since(start).Milliseconds()
	if err != nil {
		event.Add(
			slog.Bool("queue_ok", false),
			slog.Int64("queue_duration_ms", durationMs),
		)
		*hasError = true
		event.Add(slog.String("error_msg", "queue publish failed: "+err.Error()))
	} else {
		event.Add(
			slog.Bool("queue_ok", true),
			slog.Int64("queue_duration_ms", durationMs),
		)
	}
}

func doSaas(ctx context.Context, hasError *bool) {
	start := time.Now()
	event := ctx.Value(eventKey{}).(*eventAttrs)

	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Get(saasURL)
	durationMs := time.Since(start).Milliseconds()

	if err != nil {
		event.Add(
			slog.Bool("saas_ok", false),
			slog.Int("saas_status", 0),
			slog.Int64("saas_duration_ms", durationMs),
		)
		*hasError = true
		event.Add(slog.String("error_msg", "saas call failed: "+err.Error()))
		return
	}
	io.Copy(io.Discard, resp.Body)
	resp.Body.Close()

	ok := resp.StatusCode >= 200 && resp.StatusCode < 300
	event.Add(
		slog.Bool("saas_ok", ok),
		slog.Int("saas_status", resp.StatusCode),
		slog.Int64("saas_duration_ms", durationMs),
	)
	if !ok {
		*hasError = true
		event.Add(slog.String("error_msg", fmt.Sprintf("saas returned %d", resp.StatusCode)))
	}
}

func doTier2(ctx context.Context, traceID, path string, hasError *bool) {
	start := time.Now()
	event := ctx.Value(eventKey{}).(*eventAttrs)

	req, _ := http.NewRequestWithContext(ctx, "GET", tier2URL+path, nil)
	req.Header.Set("X-Trace-Id", traceID)

	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Do(req)
	durationMs := time.Since(start).Milliseconds()

	if err != nil {
		event.Add(
			slog.Bool("tier2_ok", false),
			slog.Int("tier2_status", 0),
			slog.Int64("tier2_duration_ms", durationMs),
		)
		*hasError = true
		event.Add(slog.String("error_msg", "tier2 call failed: "+err.Error()))
		return
	}
	io.Copy(io.Discard, resp.Body)
	resp.Body.Close()

	ok := resp.StatusCode >= 200 && resp.StatusCode < 300
	event.Add(
		slog.Bool("tier2_ok", ok),
		slog.Int("tier2_status", resp.StatusCode),
		slog.Int64("tier2_duration_ms", durationMs),
	)
	if !ok {
		*hasError = true
		event.Add(slog.String("error_msg", fmt.Sprintf("tier2 returned %d", resp.StatusCode)))
	}
}

func handleHealthcheck(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
	fmt.Fprintf(w, "ok\n")
}

func envOrDefault(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
