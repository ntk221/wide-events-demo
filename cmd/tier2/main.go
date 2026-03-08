package main

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"net"
	"os"
	"sync"
	"time"

	_ "github.com/go-sql-driver/mysql"
	pb "wide-events-demo/pb"
	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
)

var logger = slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
	Level: slog.LevelInfo,
}))

var db *sql.DB

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

// WideEventInterceptor is a gRPC UnaryServerInterceptor.
// Same role as HTTP middleware: create event → let handler add fields → emit 1 line.
func WideEventInterceptor(
	ctx context.Context,
	req interface{},
	info *grpc.UnaryServerInfo,
	handler grpc.UnaryHandler,
) (interface{}, error) {
	start := time.Now()

	traceID := ""
	if md, ok := metadata.FromIncomingContext(ctx); ok {
		if vals := md.Get("x-trace-id"); len(vals) > 0 {
			traceID = vals[0]
		}
	}

	event := &eventAttrs{}
	event.Add(
		slog.String("service", "tier2"),
		slog.String("trace_id", traceID),
		slog.String("method", info.FullMethod),
	)
	ctx = context.WithValue(ctx, eventKey{}, event)

	resp, err := handler(ctx, req)

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
	return resp, err
}

type tier2Server struct {
	pb.UnimplementedTier2ServiceServer
}

func (s *tier2Server) Query(ctx context.Context, req *pb.QueryRequest) (*pb.QueryResponse, error) {
	event := ctx.Value(eventKey{}).(*eventAttrs)

	dbOk, dbRows, dbDurationMs, dbErr := queryDB()
	event.Add(
		slog.Bool("db_ok", dbOk),
		slog.Int("db_rows", dbRows),
		slog.Int64("db_duration_ms", dbDurationMs),
	)

	if dbErr != nil {
		event.Add(
			slog.Bool("error", true),
			slog.String("error_msg", dbErr.Error()),
		)
		return &pb.QueryResponse{DbOk: false, DbRows: 0, DbDurationMs: dbDurationMs}, nil
	}

	event.Add(slog.Bool("error", false))
	return &pb.QueryResponse{
		DbOk:         true,
		DbRows:       int32(dbRows),
		DbDurationMs: dbDurationMs,
	}, nil
}

func (s *tier2Server) InjectError(ctx context.Context, req *pb.ErrorRequest) (*pb.ErrorResponse, error) {
	event := ctx.Value(eventKey{}).(*eventAttrs)
	event.Add(
		slog.Bool("db_ok", false),
		slog.Int("db_rows", 0),
		slog.Int64("db_duration_ms", 0),
		slog.Bool("error", true),
		slog.String("error_msg", "injected error"),
	)
	return &pb.ErrorResponse{ErrorMsg: "injected error"}, nil
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

func main() {
	dsn := envOrDefault("DB_DSN", "root:rootpass@tcp(127.0.0.1:3306)/demo")

	var err error
	for i := 0; i < 30; i++ {
		db, err = sql.Open("mysql", dsn)
		if err == nil {
			if err = db.Ping(); err == nil {
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

	port := envOrDefault("PORT", "9400")
	lis, err := net.Listen("tcp", ":"+port)
	if err != nil {
		logger.Error("failed to listen", "error", err)
		os.Exit(1)
	}

	srv := grpc.NewServer(
		grpc.UnaryInterceptor(WideEventInterceptor),
	)
	pb.RegisterTier2ServiceServer(srv, &tier2Server{})

	logger.Info(fmt.Sprintf("tier2 gRPC server starting on :%s", port))
	if err := srv.Serve(lis); err != nil {
		logger.Error("server failed", "error", err)
		os.Exit(1)
	}
}

func envOrDefault(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
