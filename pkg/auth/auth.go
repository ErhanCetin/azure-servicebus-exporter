// pkg/auth/auth.go
package auth

import (
	"context"
	"errors"
	"fmt"
	"os"

	"azure-servicebus-exporter/pkg/config"

	"github.com/Azure/azure-sdk-for-go/sdk/messaging/azservicebus"
	"github.com/Azure/azure-sdk-for-go/sdk/messaging/azservicebus/admin"
	"github.com/sirupsen/logrus"
)

// AuthProvider provides authentication for Service Bus
type AuthProvider interface {
	// GetServiceBusClient returns a Service Bus client
	GetServiceBusClient(ctx context.Context) (*azservicebus.Client, error)

	// GetServiceBusAdminClient returns a Service Bus admin client
	GetServiceBusAdminClient(ctx context.Context) (*admin.Client, error)
}

// ConnectionStringAuthProvider provides auth via connection string
type ConnectionStringAuthProvider struct {
	connectionString string
	namespace        string
	log              *logrus.Logger
}

// NewAuthProvider creates an AuthProvider based on config
func NewAuthProvider(cfg *config.Config, log *logrus.Logger) (AuthProvider, error) {
	// Extract namespace from connection string if available
	namespace := ""
	if len(cfg.ServiceBus.Namespaces) > 0 {
		namespace = cfg.ServiceBus.Namespaces[0]
	}

	// Connection String auth
	if cfg.Auth.ConnectionString != "" {
		log.Info("Using connection string from configuration")
		return &ConnectionStringAuthProvider{
			connectionString: cfg.Auth.ConnectionString,
			namespace:        namespace,
			log:              log,
		}, nil
	}

	// Environment variable fallback
	envConnStr := os.Getenv("SB_CONNECTION_STRING")
	if envConnStr != "" {
		log.Info("Using connection string from environment variable")
		return &ConnectionStringAuthProvider{
			connectionString: envConnStr,
			namespace:        namespace,
			log:              log,
		}, nil
	}

	return nil, errors.New("connection string not found in configuration or environment variables")
}

// Connection String Auth Provider implementations

func (p *ConnectionStringAuthProvider) GetServiceBusClient(ctx context.Context) (*azservicebus.Client, error) {
	p.log.WithField("namespace", p.namespace).Info("Creating Service Bus client")
	client, err := azservicebus.NewClientFromConnectionString(p.connectionString, nil)
	if err != nil {
		p.log.WithError(err).Error("Failed to create Service Bus client")
		return nil, fmt.Errorf("failed to create Service Bus client: %w", err)
	}
	p.log.Info("Service Bus client created successfully")
	return client, nil
}

func (p *ConnectionStringAuthProvider) GetServiceBusAdminClient(ctx context.Context) (*admin.Client, error) {
	p.log.WithField("namespace", p.namespace).Info("Creating Service Bus admin client")
	client, err := admin.NewClientFromConnectionString(p.connectionString, nil)
	if err != nil {
		p.log.WithError(err).Error("Failed to create Service Bus admin client")
		return nil, fmt.Errorf("failed to create Service Bus admin client: %w", err)
	}
	p.log.Info("Service Bus admin client created successfully")
	return client, nil
}
