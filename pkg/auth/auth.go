package auth

import (
    "context"
    "errors"
    "fmt"
    "os"
    
    "github.com/Azure/azure-sdk-for-go/sdk/azcore"
    "github.com/Azure/azure-sdk-for-go/sdk/azidentity"
    "github.com/Azure/azure-sdk-for-go/sdk/messaging/azservicebus"
    "github.com/Azure/azure-sdk-for-go/sdk/messaging/azservicebus/admin"
    "github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/monitor/armmonitor"
    "github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/servicebus/armservicebus"
    "azure-servicebus-exporter/pkg/config"
)

// AuthProvider provides authentication for various Azure services
type AuthProvider interface {
    // GetServiceBusClient returns a Service Bus client
    GetServiceBusClient(ctx context.Context) (*azservicebus.Client, error)
    
    // GetServiceBusAdminClient returns a Service Bus admin client
    GetServiceBusAdminClient(ctx context.Context) (*admin.Client, error)
    
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
    
    // Connection String auth
    if cfg.Auth.Mode == "connection_string" {
        if cfg.Auth.ConnectionString != "" {
            return &ConnectionStringAuthProvider{
                connectionString: cfg.Auth.ConnectionString,
                namespace:        namespace,
            }, nil
        }
        
        // Environment variable fallback
        envConnStr := os.Getenv("SB_CONNECTION_STRING")
        if envConnStr != "" {
            return &ConnectionStringAuthProvider{
                connectionString: envConnStr,
                namespace:        namespace,
            }, nil
        }
        
        return nil, errors.New("connection string not found in configuration or environment variables")
    }
    
    // Azure auth
    if cfg.Auth.Mode == "azure_auth" {
        // At least one subscription ID required
        if len(cfg.AzureMonitor.SubscriptionIDs) == 0 {
            return nil, errors.New("at least one subscription ID is required")
        }
        
        // Get credentials from environment variables if not in config
        tenantID := cfg.Auth.TenantID
        if tenantID == "" {
            tenantID = os.Getenv("AZURE_TENANT_ID")
        }
        
        clientID := cfg.Auth.ClientID
        if clientID == "" {
            clientID = os.Getenv("AZURE_CLIENT_ID")
        }
        
        clientSecret := cfg.Auth.ClientSecret
        if clientSecret == "" {
            clientSecret = os.Getenv("AZURE_CLIENT_SECRET")
        }
        
        useManagedIdentity := cfg.Auth.UseManagedIdentity
        
        // Validate credentials
        if !useManagedIdentity && (tenantID == "" || clientID == "" || clientSecret == "") {
            return nil, errors.New("tenant_id, client_id, and client_secret must be provided when using service principal")
        }
        
        return &AzureAuthProvider{
            tenantID:       tenantID,
            clientID:       clientID,
            clientSecret:   clientSecret,
            useManaged:     useManagedIdentity,
            subscriptionID: cfg.AzureMonitor.SubscriptionIDs[0],
            namespace:      namespace,
        }, nil
    }
    
    return nil, errors.New("invalid auth mode or missing credentials")
}

// Connection String Auth Provider implementations

func (p *ConnectionStringAuthProvider) GetServiceBusClient(ctx context.Context) (*azservicebus.Client, error) {
    client, err := azservicebus.NewClientFromConnectionString(p.connectionString, nil)
    if err != nil {
        return nil, fmt.Errorf("failed to create Service Bus client: %w", err)
    }
    return client, nil
}

func (p *ConnectionStringAuthProvider) GetServiceBusAdminClient(ctx context.Context) (*admin.Client, error) {
    client, err := admin.NewClientFromConnectionString(p.connectionString, nil)
    if err != nil {
        return nil, fmt.Errorf("failed to create Service Bus admin client: %w", err)
    }
    return client, nil
}

func (p *ConnectionStringAuthProvider) GetMonitorClient(ctx context.Context) (*armmonitor.MetricsClient, error) {
    return nil, errors.New("cannot use Azure Monitor API with connection string auth")
}

func (p *ConnectionStringAuthProvider) GetServiceBusManagementClient(ctx context.Context) (*armservicebus.NamespacesClient, error) {
    return nil, errors.New("cannot use Service Bus Management API with connection string auth")
}

func (p *ConnectionStringAuthProvider) GetQueuesClient(ctx context.Context) (*armservicebus.QueuesClient, error) {
    return nil, errors.New("cannot use Queues Management API with connection string auth")
}

func (p *ConnectionStringAuthProvider) GetTopicsClient(ctx context.Context) (*armservicebus.TopicsClient, error) {
    return nil, errors.New("cannot use Topics Management API with connection string auth")
}

func (p *ConnectionStringAuthProvider) GetSubscriptionsClient(ctx context.Context) (*armservicebus.SubscriptionsClient, error) {
    return nil, errors.New("cannot use Subscriptions Management API with connection string auth")
}

// Azure Auth Provider implementations

func (p *AzureAuthProvider) getCredential() (azcore.TokenCredential, error) {
    if p.useManaged {
        // Use managed identity
        return azidentity.NewDefaultAzureCredential(nil)
    } else {
        // Use service principal
        options := &azidentity.ClientSecretCredentialOptions{}
        return azidentity.NewClientSecretCredential(p.tenantID, p.clientID, p.clientSecret, options)
    }
}

func (p *AzureAuthProvider) GetServiceBusClient(ctx context.Context) (*azservicebus.Client, error) {
    cred, err := p.getCredential()
    if err != nil {
        return nil, fmt.Errorf("failed to create credential: %w", err)
    }
    
    fullyQualifiedNamespace := fmt.Sprintf("%s.servicebus.windows.net", p.namespace)
    client, err := azservicebus.NewClient(fullyQualifiedNamespace, cred, nil)
    if err != nil {
        return nil, fmt.Errorf("failed to create Service Bus client: %w", err)
    }
    
    return client, nil
}

func (p *AzureAuthProvider) GetServiceBusAdminClient(ctx context.Context) (*admin.Client, error) {
    cred, err := p.getCredential()
    if err != nil {
        return nil, fmt.Errorf("failed to create credential: %w", err)
    }
    
    fullyQualifiedNamespace := fmt.Sprintf("%s.servicebus.windows.net", p.namespace)
    client, err := admin.NewClient(fullyQualifiedNamespace, cred, nil)
    if err != nil {
        return nil, fmt.Errorf("failed to create Service Bus admin client: %w", err)
    }
    
    return client, nil
}

func (p *AzureAuthProvider) GetMonitorClient(ctx context.Context) (*armmonitor.MetricsClient, error) {
    cred, err := p.getCredential()
    if err != nil {
        return nil, fmt.Errorf("failed to create credential: %w", err)
    }
    
    client, err := armmonitor.NewMetricsClient(p.subscriptionID, cred, nil)
    if err != nil {
        return nil, fmt.Errorf("failed to create metrics client: %w", err)
    }
    
    return client, nil
}

func (p *AzureAuthProvider) GetServiceBusManagementClient(ctx context.Context) (*armservicebus.NamespacesClient, error) {
    cred, err := p.getCredential()
    if err != nil {
        return nil, fmt.Errorf("failed to create credential: %w", err)
    }
    
    client, err := armservicebus.NewNamespacesClient(p.subscriptionID, cred, nil)
    if err != nil {
        return nil, fmt.Errorf("failed to create Service Bus management client: %w", err)
    }
    
    return client, nil
}

func (p *AzureAuthProvider) GetQueuesClient(ctx context.Context) (*armservicebus.QueuesClient, error) {
    cred, err := p.getCredential()
    if err != nil {
        return nil, fmt.Errorf("failed to create credential: %w", err)
    }
    
    client, err := armservicebus.NewQueuesClient(p.subscriptionID, cred, nil)
    if err != nil {
        return nil, fmt.Errorf("failed to create queues client: %w", err)
    }
    
    return client, nil
}

func (p *AzureAuthProvider) GetTopicsClient(ctx context.Context) (*armservicebus.TopicsClient, error) {
    cred, err := p.getCredential()
    if err != nil {
        return nil, fmt.Errorf("failed to create credential: %w", err)
    }
    
    client, err := armservicebus.NewTopicsClient(p.subscriptionID, cred, nil)
    if err != nil {
        return nil, fmt.Errorf("failed to create topics client: %w", err)
    }
    
    return client, nil
}

func (p *AzureAuthProvider) GetSubscriptionsClient(ctx context.Context) (*armservicebus.SubscriptionsClient, error) {
    cred, err := p.getCredential()
    if err != nil {
        return nil, fmt.Errorf("failed to create credential: %w", err)
    }
    
    client, err := armservicebus.NewSubscriptionsClient(p.subscriptionID, cred, nil)
    if err != nil {
        return nil, fmt.Errorf("failed to create subscriptions client: %w", err)
    }
    
    return client, nil
}