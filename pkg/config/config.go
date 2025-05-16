package config

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/spf13/pflag"
	"github.com/spf13/viper"
)

// Config represents application configuration
type Config struct {
	// Server configuration
	Server struct {
		Listen  string        `mapstructure:"listen"`
		Timeout time.Duration `mapstructure:"timeout"`
	} `mapstructure:"server"`

	// Auth configuration
	Auth struct {
		ConnectionString string `mapstructure:"connection_string"`
	} `mapstructure:"auth"`

	// Metrics configuration
	Metrics struct {
		Namespace      string            `mapstructure:"namespace"`
		CacheDuration  time.Duration     `mapstructure:"cache_duration"`
		ScrapeInterval time.Duration     `mapstructure:"scrape_interval"`
		CustomLabels   map[string]string `mapstructure:"custom_labels"` // Optional custom labels
	} `mapstructure:"metrics"`

	// ServiceBus configuration
	ServiceBus struct {
		Namespaces       []string `mapstructure:"namespaces"`        // Optional for display purposes
		EntityFilter     string   `mapstructure:"entity_filter"`     // Entity filtering (regex)
		EntityTypes      []string `mapstructure:"entity_types"`      // "queue", "topic", "subscription"
		IncludeNamespace bool     `mapstructure:"include_namespace"` // Include namespace metrics
	} `mapstructure:"servicebus"`

	// Log configuration
	Logging struct {
		Level  string `mapstructure:"level"`
		Format string `mapstructure:"format"`
	} `mapstructure:"logging"`
}

// LoadConfig loads configuration from various sources (env vars, file, flags)
func LoadConfig() (*Config, error) {
	v := viper.New()

	// Set defaults
	setDefaults(v)

	// Read config file
	v.SetConfigName("config")
	v.SetConfigType("yaml")
	v.AddConfigPath(".")
	v.AddConfigPath("/etc/azure-servicebus-exporter/")

	// Enable environment variables
	v.AutomaticEnv()
	v.SetEnvPrefix("SB_EXPORTER")
	v.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))

	// Try to read config file, but continue if not found
	if err := v.ReadInConfig(); err != nil {
		if _, ok := err.(viper.ConfigFileNotFoundError); !ok {
			return nil, fmt.Errorf("error reading config file: %w", err)
		}
	}

	// Map CLI flags
	pflag.String("config", "", "Path to config file")
	pflag.Parse()

	if configFile, _ := pflag.CommandLine.GetString("config"); configFile != "" {
		v.SetConfigFile(configFile)
		if err := v.ReadInConfig(); err != nil {
			return nil, fmt.Errorf("error reading config file %s: %w", configFile, err)
		}
	}

	// Unmarshal config into struct
	var config Config
	if err := v.Unmarshal(&config); err != nil {
		return nil, fmt.Errorf("unable to decode config into struct: %w", err)
	}

	// Validate config
	if err := validateConfig(&config); err != nil {
		return nil, err
	}

	return &config, nil
}

// setDefaults sets default values for configuration
func setDefaults(v *viper.Viper) {
	// Server defaults
	v.SetDefault("server.listen", ":8080")
	v.SetDefault("server.timeout", "30s")

	// Metrics defaults
	v.SetDefault("metrics.namespace", "azure_servicebus")
	v.SetDefault("metrics.cacheDuration", "1m")
	v.SetDefault("metrics.scrapeInterval", "1m")

	// ServiceBus defaults
	v.SetDefault("servicebus.entity_filter", ".*")
	v.SetDefault("servicebus.entity_types", []string{"queue", "topic", "subscription"})
	v.SetDefault("servicebus.include_namespace", true)

	// Logging defaults
	v.SetDefault("logging.level", "info")
	v.SetDefault("logging.format", "text")
}

// validateConfig validates the configuration
func validateConfig(config *Config) error {
	// Validate auth configuration
	if config.Auth.ConnectionString == "" {
		return errors.New("connection_string must be provided in configuration")
	}

	// Validate server configuration
	if _, err := time.ParseDuration(config.Server.Timeout.String()); err != nil {
		return fmt.Errorf("invalid server.timeout: %w", err)
	}

	// Validate metrics configuration
	if _, err := time.ParseDuration(config.Metrics.CacheDuration.String()); err != nil {
		return fmt.Errorf("invalid metrics.cacheDuration: %w", err)
	}

	if _, err := time.ParseDuration(config.Metrics.ScrapeInterval.String()); err != nil {
		return fmt.Errorf("invalid metrics.scrapeInterval: %w", err)
	}

	// Validate servicebus configuration
	if _, err := regexp.Compile(config.ServiceBus.EntityFilter); err != nil {
		return fmt.Errorf("invalid servicebus.entityFilter regex: %w", err)
	}

	if len(config.ServiceBus.EntityTypes) == 0 {
		return errors.New("at least one entity type must be provided in servicebus.entityTypes")
	}

	for _, entityType := range config.ServiceBus.EntityTypes {
		if entityType != "queue" && entityType != "topic" && entityType != "subscription" {
			return fmt.Errorf("invalid entity type: %s, must be 'queue', 'topic', or 'subscription'", entityType)
		}
	}

	return nil
}
