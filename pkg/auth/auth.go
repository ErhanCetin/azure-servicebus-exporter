package auth

import (
    "context"
    "errors"
    "fmt"
    
    "github.com/Azure/azure-sdk-for-go/sdk/azcore"
    "github.com/Azure/azure-sdk-for-go/sdk/azidentity"
    "github.com/Azure/azure-sdk-for-go/sdk/messaging/azservicebus"
    "github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/monitor/armmonitor"
    "github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/servicebus/armservicebus"
    "azure-servicebus-exporter/pkg/config"
)

// AuthProvider provides authentication for various Azure services
type AuthProvider interface {
    // GetServiceBusClient returns a Service Bus client
    GetServiceBusClient(ctx context.Context) (*azservicebus.Client, error)
    
    // GetMonitorClient returns an Azure Monitor client
    GetMonitorClient(ctx context.Context) (*armmonitor.MetricsClient, error)
    
    // GetServiceBusManagementClient returns a Service Bus management client
    GetServiceBusManagementClient(ctx context.Context) (*armservicebus.NamespacesClient, error)
    
    // GetQueuesClient returns a Service Bus queues management client
    GetQueuesClient(ctx context.Context) (*armservicebus.QueuesClient, error)
    
    // GetTopicsClient returns a Service Bus topics management client
    GetTopicsClient(ctx context.Context) (*armservicebus.TopicsClient, error)
    
    // GetSubscriptionsClient returns a Service Bus subscriptions management client
    GetSubscriptionsClient(ctx context.Context) (*armservicebus.SubscriptionsClient, error)
}

// ConnectionStringAuthProvider provides auth via connection string
type ConnectionStringAuthProvider struct {
    connectionString string
    namespace        string
}

// AzureAuthProvider provides auth via Azure credentials
type AzureAuthProvider struct {
    tenantID       string
    clientID       string
    clientSecret   string
    useManaged     bool
    subscriptionID string
    namespace      string
}

// NewAuthProvider creates an AuthProvider based on config
func NewAuthProvider(cfg *config.Config) (AuthProvider, error) {
    // Extract namespace from config if available
    namespace := ""
    if len(cfg.ServiceBus.Namespaces) > 0 {
        namespace = cfg.ServiceBus.Namespaces[0]
    }
    
    // Connection String kimlik doğrulama
    if cfg.Auth.Mode == "connection_string" {
        if cfg.Auth.ConnectionString != "" {
            return &ConnectionStringAuthProvider{
                connectionString: cfg.Auth.ConnectionString,
                namespace:        namespace,
            }, nil
        }
        
        // Config'den connection string bulunamadı
        return nil, errors.New("connection string not found in configuration")
    }
    
    // Azure kimlik bilgileri ile kimlik doğrulama
    if cfg.Auth.Mode == "azure_auth" {
        // En az bir subscription ID gerekiyor
        if len(cfg.AzureMonitor.SubscriptionIDs) == 0 {
            return nil, errors.New("at least one subscription ID is required")
        }
        
        return &AzureAuthProvider{
            tenantID:       cfg.Auth.TenantID,
            clientID:       cfg.Auth.ClientID,
            clientSecret:   cfg.Auth.ClientSecret,
            useManaged:     cfg.Auth.UseManagedIdentity,
            subscriptionID: cfg.AzureMonitor.SubscriptionIDs[0],
            namespace:      namespace,
        }, nil
    }
    
    return nil, errors.New("invalid auth mode or missing credentials")
}

// Implement AuthProvider for ConnectionStringAuthProvider
func (p *ConnectionStringAuthProvider) GetServiceBusClient(ctx context.Context) (*azservicebus.Client, error) {
    // Connection string ile Service Bus client'ı oluştur
    client, err := azservicebus.NewClientFromConnectionString(p.connectionString, nil)
    if err != nil {
        return nil, fmt.Errorf("failed to create Service Bus client: %w", err)
    }
    return client, nil
}

func (p *ConnectionStringAuthProvider) GetMonitorClient(ctx context.Context) (*armmonitor.MetricsClient, error) {
    // Connection string ile Monitor API'ye erişilemez
    return nil, errors.New("cannot use Azure Monitor API with connection string auth")
}

func (p *ConnectionStringAuthProvider) GetServiceBusManagementClient(ctx context.Context) (*armservicebus.NamespacesClient, error) {
    // Connection string ile Management API'ye erişilemez
    return nil, errors.New("cannot use Service Bus Management API with connection string auth")
}

func (p *ConnectionStringAuthProvider) GetQueuesClient(ctx context.Context) (*armservicebus.QueuesClient, error) {
    // Connection string ile Queues Management API'ye erişilemez
    return nil, errors.New("cannot use Queues Management API with connection string auth")
}

func (p *ConnectionStringAuthProvider) GetTopicsClient(ctx context.Context) (*armservicebus.TopicsClient, error) {
    // Connection string ile Topics Management API'ye erişilemez
    return nil, errors.New("cannot use Topics Management API with connection string auth")
}

func (p *ConnectionStringAuthProvider) GetSubscriptionsClient(ctx context.Context) (*armservicebus.SubscriptionsClient, error) {
    // Connection string ile Subscriptions Management API'ye erişilemez
    return nil, errors.New("cannot use Subscriptions Management API with connection string auth")
}

// Implement AuthProvider for AzureAuthProvider
func (p *AzureAuthProvider) GetServiceBusClient(ctx context.Context) (*azservicebus.Client, error) {
    var cred azcore.TokenCredential
    var err error
    
    if p.useManaged {
        // Managed Identity kullan
        cred, err = azidentity.NewDefaultAzureCredential(nil)
        if err != nil {
            return nil, fmt.Errorf("failed to create managed identity credential: %w", err)
        }
    } else {
        // Service Principal kullan
        options := &azidentity.ClientSecretCredentialOptions{}
        cred, err = azidentity.NewClientSecretCredential(p.tenantID, p.clientID, p.clientSecret, options)
        if err != nil {
            return nil, fmt.Errorf("failed to create service principal credential: %w", err)
        }
    }
    
    // Namespace ve credential ile client oluştur
    fullyQualifiedNamespace := fmt.Sprintf("%s.servicebus.windows.net", p.namespace)
    client, err := azservicebus.NewClient(fullyQualifiedNamespace, cred, nil)
    if err != nil {
        return nil, fmt.Errorf("failed to create service bus client: %w", err)
    }
    
    return client, nil
}

func (p *AzureAuthProvider) GetMonitorClient(ctx context.Context) (*armmonitor.MetricsClient, error) {
    var cred azcore.TokenCredential
    var err error
    
    if p.useManaged {
        // Managed Identity kullan
        cred, err = azidentity.NewDefaultAzureCredential(nil)
        if err != nil {
            return nil, fmt.Errorf("failed to create managed identity credential: %w", err)
        }
    } else {
        // Service Principal kullan
        options := &azidentity.ClientSecretCredentialOptions{}
        cred, err = azidentity.NewClientSecretCredential(p.tenantID, p.clientID, p.clientSecret, options)
        if err != nil {
            return nil, fmt.Errorf("failed to create service principal credential: %w", err)
        }
    }
    
    // ARM Monitor client oluştur
    client, err := armmonitor.NewMetricsClient(p.subscriptionID, cred, nil)
    if err != nil {
        return nil, fmt.Errorf("failed to create metrics client: %w", err)
    }
    
    return client, nil
}

func (p *AzureAuthProvider) GetServiceBusManagementClient(ctx context.Context) (*armservicebus.NamespacesClient, error) {
    var cred azcore.TokenCredential
    var err error
    
    if p.useManaged {
        // Managed Identity kullan
        cred, err = azidentity.NewDefaultAzureCredential(nil)
        if err != nil {
            return nil, fmt.Errorf("failed to create managed identity credential: %w", err)
        }
    } else {
        // Service Principal kullan
        options := &azidentity.ClientSecretCredentialOptions{}
        cred, err = azidentity.NewClientSecretCredential(p.tenantID, p.clientID, p.clientSecret, options)
        if err != nil {
            return nil, fmt.Errorf("failed to create service principal credential: %w", err)
        }
    }
    
    // Service Bus Management client oluştur
    client, err := armservicebus.NewNamespacesClient(p.subscriptionID, cred, nil)
    if err != nil {
        return nil, fmt.Errorf("failed to create service bus namespaces client: %w", err)
    }
    
    return client, nil
}

func (p *AzureAuthProvider) GetQueuesClient(ctx context.Context) (*armservicebus.QueuesClient, error) {
    var cred azcore.TokenCredential
    var err error
    
    if p.useManaged {
        // Managed Identity kullan
        cred, err = azidentity.NewDefaultAzureCredential(nil)
        if err != nil {
            return nil, fmt.Errorf("failed to create managed identity credential: %w", err)
        }
    } else {
        // Service Principal kullan
        options := &azidentity.ClientSecretCredentialOptions{}
        cred, err = azidentity.NewClientSecretCredential(p.tenantID, p.clientID, p.clientSecret, options)
        if err != nil {
            return nil, fmt.Errorf("failed to create service principal credential: %w", err)
        }
    }
    
    // Service Bus Queues client oluştur
    client, err := armservicebus.NewQueuesClient(p.subscriptionID, cred, nil)
    if err != nil {
        return nil, fmt.Errorf("failed to create service bus queues client: %w", err)
    }
    
    return client, nil
}

func (p *AzureAuthProvider) GetTopicsClient(ctx context.Context) (*armservicebus.TopicsClient, error) {
    var cred azcore.TokenCredential
    var err error
    
    if p.useManaged {
        // Managed Identity kullan
        cred, err = azidentity.NewDefaultAzureCredential(nil)
        if err != nil {
            return nil, fmt.Errorf("failed to create managed identity credential: %w", err)
        }
    } else {
        // Service Principal kullan
        options := &azidentity.ClientSecretCredentialOptions{}
        cred, err = azidentity.NewClientSecretCredential(p.tenantID, p.clientID, p.clientSecret, options)
        if err != nil {
            return nil, fmt.Errorf("failed to create service principal credential: %w", err)
        }
    }
    
    // Service Bus Topics client oluştur
    client, err := armservicebus.NewTopicsClient(p.subscriptionID, cred, nil)
    if err != nil {
        return nil, fmt.Errorf("failed to create service bus topics client: %w", err)
    }
    
    return client, nil
}

func (p *AzureAuthProvider) GetSubscriptionsClient(ctx context.Context) (*armservicebus.SubscriptionsClient, error) {
    var cred azcore.TokenCredential
    var err error
    
    if p.useManaged {
        // Managed Identity kullan
        cred, err = azidentity.NewDefaultAzureCredential(nil)
        if err != nil {
            return nil, fmt.Errorf("failed to create managed identity credential: %w", err)
        }
    } else {
        // Service Principal kullan
        options := &azidentity.ClientSecretCredentialOptions{}
        cred, err = azidentity.NewClientSecretCredential(p.tenantID, p.clientID, p.clientSecret, options)
        if err != nil {
            return nil, fmt.Errorf("failed to create service principal credential: %w", err)
        }
    }
    
    // Service Bus Subscriptions client oluştur
    client, err := armservicebus.NewSubscriptionsClient(p.subscriptionID, cred, nil)
    if err != nil {
        return nil, fmt.Errorf("failed to create service bus subscriptions client: %w", err)
    }
    
    return client, nil
}