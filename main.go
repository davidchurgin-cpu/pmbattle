package main

import (
	"context"
	"embed"
	"errors"
	"io/fs"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/davidchurgin-cpu/pmbattle/internal/app"
	"github.com/davidchurgin-cpu/pmbattle/internal/domain"
	"github.com/davidchurgin-cpu/pmbattle/internal/fixed"
	"github.com/davidchurgin-cpu/pmbattle/internal/kalshi"
	"github.com/davidchurgin-cpu/pmbattle/internal/orders"
	"github.com/davidchurgin-cpu/pmbattle/internal/server"
	"github.com/davidchurgin-cpu/pmbattle/internal/storage"
)

//go:embed web/dist/*
var webAssets embed.FS

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	slog.SetDefault(logger)
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	dbPath := env("PMBATTLE_DB", "pmbattle.db")
	store, err := storage.Open(dbPath)
	if err != nil {
		slog.Error("open database", "error", err)
		os.Exit(1)
	}
	defer store.Close()
	interval, err := time.ParseDuration(env("PMBATTLE_SCHEDULE_INTERVAL", "30s"))
	if err != nil {
		interval = 30 * time.Second
	}
	marketInterval, err := time.ParseDuration(env("PMBATTLE_MARKET_INTERVAL", "5m"))
	if err != nil {
		marketInterval = 5 * time.Minute
	}
	simulated := strings.EqualFold(env("PMBATTLE_SIMULATED", "true"), "true")
	kalshiEnvironment := env("PMBATTLE_KALSHI_ENV", "demo")
	tradingRequested := strings.EqualFold(env("PMBATTLE_TRADING_ENABLED", "false"), "true")
	if tradingRequested && simulated {
		slog.Error("refusing to enable order entry in simulated mode")
		os.Exit(1)
	}
	if tradingRequested && !strings.EqualFold(kalshiEnvironment, "demo") && !strings.EqualFold(kalshiEnvironment, "production") {
		slog.Error("refusing to enable order entry for an unknown Kalshi environment")
		os.Exit(1)
	}
	tradingEnabled := tradingRequested && !simulated
	maxCashRisk := domain.Money(0)
	if raw := env("PMBATTLE_MAX_CASH_RISK", ""); raw != "" {
		parsed, err := fixed.Parse(raw)
		if err != nil || parsed < domain.Dollar || parsed > orders.DefaultMaxCashRisk {
			slog.Error("PMBATTLE_MAX_CASH_RISK must be a dollar amount between 1 and 20000", "value", raw)
			os.Exit(1)
		}
		maxCashRisk = parsed
	}
	kalshiClient, err := kalshi.New(kalshi.Config{Environment: kalshiEnvironment, KeyID: os.Getenv("PMBATTLE_KALSHI_KEY_ID"), PrivateKeyPath: os.Getenv("PMBATTLE_KALSHI_PRIVATE_KEY_PATH"), TradingEnabled: tradingEnabled})
	if err != nil {
		slog.Error("configure Kalshi", "error", err)
		os.Exit(1)
	}
	service := app.New(app.Config{ScheduleURL: env("PMBATTLE_SCHEDULE_URL", "http://linefeednew.spankodds.com/supportSystem/rawschedule_v2_expanded.xml"), ScheduleInterval: interval, MarketInterval: marketInterval, ExchangeEnvironment: kalshiEnvironment, Simulated: simulated, TradingEnabled: tradingEnabled, MaxCashRisk: maxCashRisk}, store, kalshiClient)
	static, _ := fs.Sub(webAssets, "web/dist")
	httpServer := &http.Server{Addr: env("PMBATTLE_ADDR", ":8080"), Handler: server.New(service, static).Handler(), ReadHeaderTimeout: 5 * time.Second, IdleTimeout: 60 * time.Second}
	go service.Run(ctx)
	go func() {
		slog.Info("PMBattle listening", "address", httpServer.Addr, "simulated", simulated, "trading_enabled", tradingEnabled)
		if err := httpServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			slog.Error("server stopped", "error", err)
			stop()
		}
	}()
	<-ctx.Done()
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_ = httpServer.Shutdown(shutdownCtx)
}

func env(key, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(key)); value != "" {
		return value
	}
	return fallback
}
