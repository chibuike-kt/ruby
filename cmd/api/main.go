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

	"github.com/chibuike-kt/ruby/internal/ai"
	"github.com/chibuike-kt/ruby/internal/ai/ffmpeg"
	"github.com/chibuike-kt/ruby/internal/ai/openai"
	"github.com/chibuike-kt/ruby/internal/config"
	"github.com/chibuike-kt/ruby/internal/customer"
	"github.com/chibuike-kt/ruby/internal/db"
	"github.com/chibuike-kt/ruby/internal/debt"
	"github.com/chibuike-kt/ruby/internal/httpserver"
	"github.com/chibuike-kt/ruby/internal/payment"
	"github.com/chibuike-kt/ruby/internal/whatsapp"
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

	customers := customer.NewService(pool)
	debts := debt.NewService(pool)
	payments := payment.NewService(pool)

	whatsappService := whatsapp.NewService(pool, redisClient, cfg.WhatsAppAppSecret, cfg.WhatsAppVerifyToken,
		cfg.WhatsAppAccessToken, cfg.WhatsAppPhoneNumberID, logger)

	// The AI processor needs whatsappService (to send replies / download
	// media) and whatsappService needs the processor (to handle inbound
	// messages) — a genuine two-way wiring dependency, broken by
	// deferring the handler's own reference until after processor is
	// assigned. The closure is never invoked until a real webhook
	// arrives, long after both are fully constructed. See plan decision
	// #10 in docs/BRIEF-ai-intent.md's implementation.
	var processor *ai.Processor
	whatsappService.SetHandler(func(ctx context.Context, msg whatsapp.StoredMessage) {
		userID := int64(0)
		if msg.UserID != nil {
			userID = *msg.UserID
		}
		// Handle already logs failures and sends the reply itself
		// (via Sender); the returned values here are for tests only.
		_, _ = processor.Handle(ctx, ai.ToInboundMessage(userID, msg.ProviderMessageID, msg.MessageType, msg.ContentReference))
	})

	processor = ai.NewProcessor(ai.Config{
		Extractor:   openai.NewExtractor(cfg.AIProviderAPIKey, cfg.AIModel, cfg.DefaultTimezone),
		Transcriber: openai.NewTranscriber(cfg.AIProviderAPIKey),
		Transcoder:  ffmpeg.New(),
		Phraser:     openai.NewPhraser(cfg.AIProviderAPIKey),
		Sender:      whatsappService,
		Media:       whatsappService,
		Pool:        pool,
		Redis:       redisClient,
		Customers:   customers,
		Debts:       debts,
		Payments:    payments,
		Logger:      logger,
	})

	server := &httpserver.Server{
		Pool:               pool,
		Redis:              redisClient,
		Customers:          customers,
		Debts:              debts,
		Payments:           payments,
		Webhooks:           whatsappService,
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
