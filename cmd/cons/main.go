package main

import (
	"context"
	"database/sql"
	"log/slog"
	"math/rand"
	"os"
	"strings"
	"time"

	_ "github.com/go-sql-driver/mysql"
	amqp "github.com/rabbitmq/amqp091-go"
)

var logger = slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
	Level: slog.LevelInfo,
}))

var db *sql.DB

func goSlowP() bool {
	return rand.Float64()*100 > 95
}

func main() {
	queueURL := envOrDefault("QUEUE_URL", "amqp://guest:guest@localhost:5672/")
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

	var conn *amqp.Connection
	var ch *amqp.Channel
	for i := 0; i < 30; i++ {
		conn, err = amqp.Dial(queueURL)
		if err == nil {
			ch, err = conn.Channel()
			if err == nil {
				_, err = ch.QueueDeclare("test", true, false, false, false, nil)
				if err == nil {
					logger.Info("connected to RabbitMQ")
					break
				}
				ch.Close()
			}
			conn.Close()
		}
		logger.Info("waiting for RabbitMQ...", "attempt", i+1)
		time.Sleep(2 * time.Second)
	}
	if err != nil {
		logger.Error("failed to connect to RabbitMQ", "error", err)
		os.Exit(1)
	}
	defer conn.Close()
	defer ch.Close()

	msgs, err := ch.Consume("test", "", true, false, false, false, nil)
	if err != nil {
		logger.Error("failed to register consumer", "error", err)
		os.Exit(1)
	}

	logger.Info("cons started, waiting for messages...")

	for msg := range msgs {
		processMessage(msg)
	}
}

func processMessage(msg amqp.Delivery) {
	start := time.Now()

	parts := strings.Fields(string(msg.Body))
	traceID := ""
	if len(parts) >= 2 {
		traceID = parts[1]
	}

	slow := goSlowP()
	if slow {
		time.Sleep(1500 * time.Millisecond)
	}

	dbOk, dbRows, dbDurationMs, dbErr := queryDB()

	durationMs := time.Since(start).Milliseconds()

	attrs := []any{
		slog.String("service", "cons"),
		slog.String("trace_id", traceID),
		slog.Bool("was_slow", slow),
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
	} else {
		attrs = append(attrs, slog.Bool("error", false))
		logger.Log(context.Background(), slog.LevelInfo, "request", attrs...)
	}
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

func envOrDefault(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
