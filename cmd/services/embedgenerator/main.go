package main

import (
	"context"
	"embed"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/caarlos0/env/v10"
	"github.com/genshinsim/gcsim/internal/services/embedgenerator"
	"github.com/redis/go-redis/v9"
)

//go:embed dist/*
var content embed.FS

type config struct {
	Host        string `env:"HOST"`
	Port        string `env:"PORT"         envDefault:"3000"`
	LauncherURL string `env:"LAUNCHER_URL" envDefault:"ws://launcher:7317"`
	AuthKey     string `env:"AUTH_KEY"`
	PreviewURL  string `env:"PREVIEW_URL"  envDefault:"http://preview:3000"`
	// proxy is always used
	ProxyTO     string `env:"PROXY_TO"     envDefault:"https://gcsim.app"`
	ProxyPrefix string `env:"PROXY_PREFIX" envDefault:"/api"`
	// this is for local image assets
	LocalAssets string `env:"ASSETS_PATH"`
	AssetPrefix string `env:"ASSETS_PREFIX" envDefault:"/api/assets"`
	// redis options
	RedisURL        []string `env:"REDIS_URL"         envDefault:"redis:6379" envSeparator:""`
	RedisDB         int      `env:"REDIS_DB"          envDefault:"0"`
	RedisMasterName string   `env:"REDIS_MASTER_NAME"`
	// timeouts
	GenerateTimeoutInSec int `env:"GENERATE_TIMEOUT_IN_SEC"`
	CacheTTLInSec        int `env:"CACHE_TTL_IN_SEC"`
}

func main() {
	var err error

	cfg := config{}
	if err := env.Parse(&cfg); err != nil {
		fmt.Printf("%+v\n", err)
	}

	fmt.Printf("%+v\n", cfg)

	if cfg.AuthKey == "" {
		log.Println("WARNING: no AUTH_KEY set, running without auth key check")
	}
	log.Println("running with config ", cfg)

	server, err := embedgenerator.New(content, redis.UniversalOptions{
		Addrs:      cfg.RedisURL,
		DB:         cfg.RedisDB,
		MasterName: cfg.RedisMasterName,
	}, cfg.LauncherURL, cfg.PreviewURL, cfg.AuthKey)

	panicErr(err)

	err = server.SetOpts(
		embedgenerator.WithProxy(cfg.ProxyPrefix, cfg.ProxyTO),
		embedgenerator.WithSkipTLSVerify(),
	)
	panicErr(err)

	if cfg.LocalAssets != "" {
		panicErr(server.SetOpts(embedgenerator.WithLocalAssets(cfg.AssetPrefix, cfg.LocalAssets)))
	}

	if cfg.GenerateTimeoutInSec > 0 {
		panicErr(server.SetOpts(embedgenerator.WithGenerateTimeout(cfg.GenerateTimeoutInSec)))
	}

	if cfg.CacheTTLInSec > 0 {
		panicErr(server.SetOpts(embedgenerator.WithCacheTTL(cfg.CacheTTLInSec)))
	}

	err = server.Init()
	if err != nil {
		log.Fatal(err)
	}

	httpServer := &http.Server{
		Addr:    cfg.Host + ":" + cfg.Port,
		Handler: server,
	}

	go func() {
		if err := httpServer.ListenAndServe(); !errors.Is(err, http.ErrServerClosed) {
			log.Fatalf("HTTP server error: %v", err)
		}
		log.Println("Stopped serving new connections.")
	}()

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
	<-sigChan

	shutdownCtx, shutdownRelease := context.WithTimeout(context.Background(), 10*time.Second)
	defer shutdownRelease()
	defer func() {
		log.Println("Shutting down browsers: ", server.Shutdown())
	}()

	if err := httpServer.Shutdown(shutdownCtx); err != nil {
		log.Println("HTTP graceful shutdown encountered error, forcing shutdown")
		// force shut down
		err := httpServer.Close()
		log.Println("Force shut down completed with error: ", err)
		log.Panicf("HTTP shutdown error: %v", err)
	}
	log.Println("Graceful shutdown complete.")
}

func panicErr(err error) {
	if err != nil {
		panic(err)
	}
}