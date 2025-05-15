# Azure Service Bus Prometheus Exporter

Prometheus exporter for Azure Service Bus metrics. Supports both direct Service Bus connection (via connection string) and Azure Monitor API (via service principal or managed identity).

## Features

- Collect Service Bus metrics via direct connection with connection string
- Collect Service Bus metrics via Azure Monitor API
- Support for multiple authentication methods (connection string, service principal, managed identity)
- Support for Kubernetes secrets
- Ready-to-use Kubernetes deployment and Helm chart
- Support for both Azure Kubernetes Service (AKS) and Google Kubernetes Engine (GKE)
- Entity filtering with regex
- Metric caching to reduce API calls
- Custom metric naming with templates
- Prometheus-compatible HTTP endpoints
- Development and testing web UI

## Metrics

The exporter collects the following Service Bus metrics:

### Queue and Topic Metrics
- Active Messages
- Dead-lettered Messages
- Scheduled Messages
- Message Size
- Incoming Messages
- Outgoing Messages

### Namespace Metrics
- Successful Requests
- Server Errors
- User Errors
- Throttled Requests
- Active Connections
- CPU Usage (Premium tier)
- Memory Usage (Premium tier)

## Installation

### Using Docker

```bash
docker run -p 8080:8080 \
  -e AUTH_MODE=connection_string \
  -e CONNECTION_STRING="Endpoint=sb://namespace.servicebus.windows.net/;SharedAccessKeyName=RootManageSharedAccessKey;SharedAccessKey=yourkeyhere" \
  yourregistry/azure-servicebus-exporter




kubectl apply -f deploy/kubernetes/
helm repo add your-repo https://your-helm-repo
helm install azure-servicebus-exporter your-repo/azure-servicebus-exporter


config.yaml

#For direct connection to Service Bus, provide a connection string with Manage permissions:

auth:
  mode: "connection_string"
  connectionString: "Endpoint=sb://namespace.servicebus.windows.net/;SharedAccessKeyName=RootManageSharedAccessKey;SharedAccessKey=yourkeyhere"

#For Azure Monitor API access, provide Azure service principal credentials:
auth:
  mode: "azure_auth"
  tenantId: "your-tenant-id"
  clientId: "your-client-id"
  clientSecret: "your-client-secret"
  useManagedIdentity: false

#For AKS environments, you can use managed identity:

auth:
  mode: "azure_auth"
  useManagedIdentity: true  


# Prometheus Configuration
 scrape_configs:
  - job_name: 'azure-servicebus'
    scrape_interval: 1m
    static_configs:
      - targets: ['azure-servicebus-exporter:8080'] 

Development
go build -o azure-servicebus-exporter ./cmd/azure-servicebus-exporter    
  
Testing
go test -v ./...

Building Docker Image
docker build -t azure-servicebus-exporter -f deploy/docker/Dockerfile .


Localde calistirma:
Proje ana dizininde config.yaml dosyası oluşturun:
  olusturuldu
Uygulamayı konfigürasyon dosyasıyla çalıştırın:
   ./azure-servicebus-exporter --config config.yaml
   # Uygulamayı çalıştırın
  ./azure-servicebus-exporter

-----
3. VSCode Launch Configuration ile Çalıştırma
VSCode'un hata ayıklama özelliklerini kullanarak uygulamayı çalıştırmak ve hata ayıklamak istiyorsanız, bir launch configuration oluşturabilirsiniz:

VSCode'da .vscode klasörü oluşturun (eğer yoksa)
Bu klasörde launch.json dosyası oluşturun:

json{
    "version": "0.2.0",
    "configurations": [
        {
            "name": "Launch Exporter",
            "type": "go",
            "request": "launch",
            "mode": "auto",
            "program": "${workspaceFolder}/cmd/azure-servicebus-exporter",
            "args": ["--config", "${workspaceFolder}/config.yaml"],
            "env": {
                "CONNECTION_STRING": "Endpoint=sb://your-namespace.servicebus.windows.net/;SharedAccessKeyName=RootManageSharedAccessKey;SharedAccessKey=yourkey"
            }
        }
    ]
}

VSCode'un "Run and Debug" sekmesini açın ve "Launch Exporter" konfigürasyonunu seçip çalıştırın.

---
- Uygulama çalıştıktan sonra, tarayıcınızdan veya curl ile şu adreslere erişerek test edebilirsiniz:
# Temel sağlık kontrolü
curl http://localhost:8080/health

# Prometheus metriklerini görüntüleme
curl http://localhost:8080/metrics

# Service Bus metriklerini sorgulama
curl http://localhost:8080/probe/metrics