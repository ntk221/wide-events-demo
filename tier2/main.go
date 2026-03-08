package main

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"time"

	_ "github.com/go-sql-driver/mysql"
)

var logger = slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
	Level: slog.LevelInfo,
}))

var db *sql.DB

func main() {
	dsn := envOrDefault("DB_DSN", "root:rootpass@tcp(localhost:3306)/demo")

	var err error
	for i := 0; i < 30; i++ {
		db, err = sql.Open("mysql", dsn)
		if err == nil {
			err = db.Ping()
			if err == nil {
				logger.Info("connected to MySQL")
				break
			}
		}
		logger.Info("waiting for MySQL...", "attempt", i+1)
		time.Sleep(2 * time.Second)
	}
	if err != nil {
		logger.Error("failed to connect to MySQL", "error", err)
		os.Exit(1)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/", handleRoot)
	mux.HandleFunc("/error", handleError)
	mux.HandleFunc("/healthcheck", handleHealthcheck)

	port := envOrDefault("PORT", "8080")
	logger.Info("tier2 starting on :" + port)
	if err := http.ListenAndServe(":"+port, mux); err != nil {
		logger.Error("server failed", "error", err)
		os.Exit(1)
	}
}

func handleRoot(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	start := time.Now()
	traceID := r.Header.Get("X-Trace-Id")

	dbOk, dbRows, dbDurationMs, dbErr := queryDB()

	durationMs := time.Since(start).Milliseconds()

	attrs := []any{
		slog.String("service", "tier2"),
		slog.String("trace_id", traceID),
		slog.Int64("duration_ms", durationMs),
		slog.Bool("db_ok", dbOk),
		slog.Int("db_rows", dbRows),
		slog.Int64("db_duration_ms", dbDurationMs),
	}

	if dbErr != nil {
		attrs = append(attrs,
			slog.Bool("error", true),
			slog.String("error_msg", dbErr.Error()),
		)
		logger.Log(context.Background(), slog.LevelError, "request", attrs...)
		w.WriteHeader(http.StatusInternalServerError)
		fmt.Fprintf(w, "error\n")
	} else {
		attrs = append(attrs, slog.Bool("error", false))
		logger.Log(context.Background(), slog.LevelInfo, "request", attrs...)
		w.WriteHeader(http.StatusOK)
		fmt.Fprintf(w, "ok\n")
	}
}

func handleError(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	traceID := r.Header.Get("X-Trace-Id")

	durationMs := time.Since(start).Milliseconds()

	attrs := []any{
		slog.String("service", "tier2"),
		slog.String("trace_id", traceID),
		slog.Int64("duration_ms", durationMs),
		slog.Bool("db_ok", false),
		slog.Int("db_rows", 0),
		slog.Int64("db_duration_ms", 0),
		slog.Bool("error", true),
		slog.String("error_msg", "injected error"),
	}
	logger.Log(context.Background(), slog.LevelError, "request", attrs...)

	w.WriteHeader(http.StatusInternalServerError)
	fmt.Fprintf(w, "injected error\n")
}

func queryDB() (bool, int, int64, error) {
	start := time.Now()
	rows, err := db.Query("SELECT * FROM test")
	if err != nil {
		return false, 0, time.Since(start).Milliseconds(), err
	}
	defer rows.Close()

	count := 0
	for rows.Next() {
		count++
	}
	if err := rows.Err(); err != nil {
		return false, 0, time.Since(start).Milliseconds(), err
	}

	return true, count, time.Since(start).Milliseconds(), nil
}

func handleHealthcheck(w http.ResponseWriter, r *http.Request) {
	if err := db.Ping(); err != nil {
		w.WriteHeader(http.StatusServiceUnavailable)
		fmt.Fprintf(w, "db unhealthy\n")
		return
	}
	w.WriteHeader(http.StatusOK)
	fmt.Fprintf(w, "ok\n")
}

func envOrDefault(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
