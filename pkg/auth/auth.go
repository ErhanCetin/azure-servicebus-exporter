package auth

import (
    "context"
    "errors"
    "fmt"
    
    "github.com/Azure/azure-service-bus-go"
    "github.com/Azure/azure-sdk-for-go/sdk/azidentity"
    "github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/monitor/armmonitor"
    "k8s.io/client-go/kubernetes"
    metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
    "azure-servicebus-exporter/pkg/config"
)

// AuthProvider provides authentication for various Azure services
type AuthProvider interface {
    // GetServiceBusClient returns a Service Bus client
    GetServiceBusClient(ctx context.Context) (*servicebus.Namespace, error)
    
    // GetMonitorClient returns an Azure Monitor client
    GetMonitorClient(ctx context.Context) (*armmonitor.MetricsClient, error)
}

// ConnectionStringAuthProvider provides auth via connection string
type ConnectionStringAuthProvider struct {
    connectionString string
}

// AzureAuthProvider provides auth via Azure credentials
type AzureAuthProvider struct {
    tenantID       string
    clientID       string
    clientSecret   string
    useManaged     bool
    subscriptionID string
}

// K8sSecretAuthProvider provides auth via Kubernetes secret
type K8sSecretAuthProvider struct {
    client          *kubernetes.Clientset
    secretName      string
    secretNamespace string
    secretKey       string
}

// NewAuthProvider creates an AuthProvider based on config
func NewAuthProvider(cfg *config.Config) (AuthProvider, error) {
    // Connection String kimlik doğrulama
    if cfg.Auth.Mode == "connection_string" {
        if cfg.Auth.ConnectionString != "" {
            return &ConnectionStringAuthProvider{
                connectionString: cfg.Auth.ConnectionString,
            }, nil
        }
        
        // Kubernetes Secret'tan connection string'i al
        if cfg.Kubernetes.SecretName != "" {
            // K8s client oluştur
            // Not: Bu kısım gerçek bir uygulama için geliştirilmelidir
            // Şimdilik basit bir örnek oluşturuyoruz
            return &K8sSecretAuthProvider{
                secretName:      cfg.Kubernetes.SecretName,
                secretNamespace: cfg.Kubernetes.SecretNamespace,
                secretKey:       cfg.Kubernetes.SecretKey,
            }, nil
        }
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
        }, nil
    }
    
    return nil, errors.New("invalid auth mode or missing credentials")
}

// Implement AuthProvider for ConnectionStringAuthProvider
func (p *ConnectionStringAuthProvider) GetServiceBusClient(ctx context.Context) (*servicebus.Namespace, error) {
    // Connection string ile Service Bus client'ı oluştur
    ns, err := servicebus.NewNamespace(servicebus.NamespaceWithConnectionString(p.connectionString))
    if err != nil {
        return nil, err
    }
    return ns, nil
}

func (p *ConnectionStringAuthProvider) GetMonitorClient(ctx context.Context) (*armmonitor.MetricsClient, error) {
    // Connection string ile Monitor API'ye erişilemez
    return nil, errors.New("cannot use Azure Monitor API with connection string auth")
}

// Implement AuthProvider for AzureAuthProvider
func (p *AzureAuthProvider) GetServiceBusClient(ctx context.Context) (*servicebus.Namespace, error) {
    // Azure kimlik bilgileri ile doğrudan Service Bus'a bağlanmak için
    if p.useManaged {
        // Managed Identity kullan
        // Not: Azure Service Bus Go kütüphanesinin güncel sürümünde farklı bir yöntem kullanılıyor olabilir
        return nil, errors.New("managed identity auth for Service Bus not implemented")
    } else {
        // Connection string olmadan Service Bus bağlantısı için
        // Yeni sürümlerde farklı bir yaklaşım gerekebilir
        return nil, errors.New("service principal auth for Service Bus not implemented without connection string")
    }
}

func (p *AzureAuthProvider) GetMonitorClient(ctx context.Context) (*armmonitor.MetricsClient, error) {
    //var err error
    
    if p.useManaged {
        // Managed Identity kullan
        cred, err := azidentity.NewDefaultAzureCredential(nil)
        if err != nil {
            return nil, fmt.Errorf("failed to create managed identity credential: %w", err)
        }
        
        // ARM Monitor client oluştur
        client, err := armmonitor.NewMetricsClient(p.subscriptionID, cred, nil)
        if err != nil {
            return nil, fmt.Errorf("failed to create metrics client: %w", err)
        }
        
        return client, nil
    } else {
        // Service Principal kullan
        options := &azidentity.ClientSecretCredentialOptions{}
        cred, err := azidentity.NewClientSecretCredential(p.tenantID, p.clientID, p.clientSecret, options)
        if err != nil {
            return nil, fmt.Errorf("failed to create service principal credential: %w", err)
        }
        
        // ARM Monitor client oluştur
        client, err := armmonitor.NewMetricsClient(p.subscriptionID, cred, nil)
        if err != nil {
            return nil, fmt.Errorf("failed to create metrics client: %w", err)
        }
        
        return client, nil
    }
}

// Implement AuthProvider for K8sSecretAuthProvider
func (p *K8sSecretAuthProvider) GetServiceBusClient(ctx context.Context) (*servicebus.Namespace, error) {
    // Kubernetes Secret'tan connection string'i al
    connectionString, err := p.getConnectionStringFromSecret(ctx)
    if err != nil {
        return nil, err
    }
    
    // Connection string ile Service Bus client'ı oluştur
    ns, err := servicebus.NewNamespace(servicebus.NamespaceWithConnectionString(connectionString))
    if err != nil {
        return nil, err
    }
    
    return ns, nil
}

func (p *K8sSecretAuthProvider) GetMonitorClient(ctx context.Context) (*armmonitor.MetricsClient, error) {
    // Connection string ile Monitor API'ye erişilemez
    return nil, errors.New("cannot use Azure Monitor API with connection string auth")
}

// getConnectionStringFromSecret retrieves connection string from Kubernetes secret
func (p *K8sSecretAuthProvider) getConnectionStringFromSecret(ctx context.Context) (string, error) {
    if p.client == nil {
        return "", errors.New("kubernetes client is not initialized")
    }
    
    secretNamespace := p.secretNamespace
    if secretNamespace == "" {
        secretNamespace = "default"
    }
    
    secret, err := p.client.CoreV1().Secrets(secretNamespace).Get(ctx, p.secretName, metav1.GetOptions{})
    if err != nil {
        return "", fmt.Errorf("failed to get secret %s/%s: %w", secretNamespace, p.secretName, err)
    }
    
    connectionString, ok := secret.Data[p.secretKey]
    if !ok {
        return "", fmt.Errorf("key %s not found in secret %s/%s", p.secretKey, secretNamespace, p.secretName)
    }
    
    return string(connectionString), nil
}