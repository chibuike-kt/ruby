package main

import (
	"context"
	"errors"
	"log"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/chibuike-kt/ruby/internal/config"
	"github.com/chibuike-kt/ruby/internal/customer"
	"github.com/chibuike-kt/ruby/internal/db"
	"github.com/chibuike-kt/ruby/internal/debt"
	"github.com/chibuike-kt/ruby/internal/httpserver"
	"github.com/chibuike-kt/ruby/internal/payment"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("config error: %v", err)
	}

	ctx := context.Background()

	pool, err := db.NewPool(ctx, cfg.DatabaseURL)
	if err != nil {
		log.Fatalf("postgres connection error: %v", err)
	}
	defer pool.Close()

	redisClient, err := db.NewRedisClient(cfg.RedisURL)
	if err != nil {
		log.Fatalf("redis connection error: %v", err)
	}
	defer func() { _ = redisClient.Close() }()

	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))

	server := &httpserver.Server{
		Pool:               pool,
		Redis:              redisClient,
		Customers:          customer.NewService(pool),
		Debts:              debt.NewService(pool),
		Payments:           payment.NewService(pool),
		RateLimitPerMinute: cfg.RateLimitPerMinute,
		Logger:             logger,
	}

	httpSrv := &http.Server{
		Addr:              ":" + cfg.Port,
		Handler:           httpserver.NewRouter(server),
		ReadHeaderTimeout: 5 * time.Second,
	}

	go func() {
		log.Printf("listening on :%s", cfg.Port)
		if err := httpSrv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatalf("server error: %v", err)
		}
	}()

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)
	<-stop

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := httpSrv.Shutdown(shutdownCtx); err != nil {
		log.Fatalf("shutdown error: %v", err)
	}
}
