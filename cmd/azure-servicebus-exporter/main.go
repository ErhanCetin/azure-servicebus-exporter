package main

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/sirupsen/logrus"
	"github.com/spf13/cobra"

	"azure-servicebus-exporter/pkg/auth"
	"azure-servicebus-exporter/pkg/collector"
	"azure-servicebus-exporter/pkg/config"
	"azure-servicebus-exporter/pkg/server"
	"azure-servicebus-exporter/pkg/servicebus"
)

var (
	configFile string
	logLevel   string
	logFormat  string
)

func main() {
	cmd := &cobra.Command{
		Use:   "azure-servicebus-exporter",
		Short: "Prometheus exporter for Azure Service Bus",
		Run:   run,
	}

	// Command line flags
	cmd.PersistentFlags().StringVar(&configFile, "config", "", "Configuration file path")
	cmd.PersistentFlags().StringVar(&logLevel, "log-level", "info", "Log level")
	cmd.PersistentFlags().StringVar(&logFormat, "log-format", "text", "Log format (text or json)")

	if err := cmd.Execute(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

func run(cmd *cobra.Command, args []string) {
	// Create logger
	log := logrus.New()

	// Set log level
	level, err := logrus.ParseLevel(logLevel)
	if err != nil {
		log.WithError(err).Fatal("Invalid log level")
	}
	log.SetLevel(level)

	// Set log format
	if logFormat == "json" {
		log.SetFormatter(&logrus.JSONFormatter{})
	}

	// Load configuration
	cfg, err := config.LoadConfig()
	if err != nil {
		log.WithError(err).Fatal("Failed to load configuration")
	}

	// Environment variables for connection string
	if envConnStr := os.Getenv("SB_CONNECTION_STRING"); envConnStr != "" && cfg.Auth.ConnectionString == "" {
		cfg.Auth.ConnectionString = envConnStr
		log.Info("Using connection string from environment variable")
	}

	// Create auth provider
	authProvider, err := auth.NewAuthProvider(cfg, log)
	if err != nil {
		log.WithError(err).Fatal("Failed to create auth provider")
	}

	// Create Service Bus client
	sbClient, err := servicebus.NewClient(cfg, authProvider, log)
	if err != nil {
		log.WithError(err).Fatal("Failed to create Service Bus client")
	}

	// Create collector (without azuremonitor client)
	col := collector.NewServiceBusCollector(cfg, log, sbClient)

	// Create HTTP server
	srv := server.NewServer(cfg, log, col)
	srv.Setup()

	// Start HTTP server in a goroutine
	go func() {
		log.WithField("address", cfg.Server.Listen).Info("Starting HTTP server")
		if err := srv.Start(); err != nil && err != http.ErrServerClosed {
			log.WithError(err).Fatal("Failed to start HTTP server")
		}
	}()

	// Wait for shutdown signal
	shutdown := make(chan os.Signal, 1)
	signal.Notify(shutdown, os.Interrupt, syscall.SIGTERM)

	<-shutdown
	log.Info("Shutdown signal received")

	// Create a deadline for graceful shutdown
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Gracefully shut down the server
	if err := srv.Stop(ctx); err != nil {
		log.WithError(err).Error("Server shutdown failed")
	}

	log.Info("Server stopped")
}
