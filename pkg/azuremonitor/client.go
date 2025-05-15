package azuremonitor

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/monitor/armmonitor"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/sirupsen/logrus"

	"azure-servicebus-exporter/pkg/auth"
	"azure-servicebus-exporter/pkg/config"
	"azure-servicebus-exporter/pkg/metrics"
)

// NamespaceMetric represents metrics for a Service Bus namespace
type NamespaceMetric struct {
	Namespace  string
	MetricName string
	Value      float64
}

// EntityMetric represents metrics for a Service Bus entity (queue, topic, subscription)
type EntityMetric struct {
	Namespace  string
	EntityName string
	EntityType string
	MetricName string
	Value      float64
}

// Client represents an Azure Monitor client that can collect metrics
type Client struct {
	cfg  *config.Config
	auth auth.AuthProvider
	log  *logrus.Logger

	metricsClient *armmonitor.MetricsClient

	// Metrics
	metrics map[string]*prometheus.GaugeVec

	// Cached metrics
	namespaceMetrics []NamespaceMetric
	entityMetrics    []EntityMetric

	// Cache
	cache      map[string]metrics.MetricValue
	cacheMutex sync.RWMutex
	lastUpdate time.Time
}

// NewClient creates a new Azure Monitor client
func NewClient(cfg *config.Config, authProvider auth.AuthProvider, log *logrus.Logger) (*Client, error) {
	client := &Client{
		cfg:              cfg,
		auth:             authProvider,
		log:              log,
		metrics:          make(map[string]*prometheus.GaugeVec),
		cache:            make(map[string]metrics.MetricValue),
		namespaceMetrics: []NamespaceMetric{},
		entityMetrics:    []EntityMetric{},
	}

	return client, nil
}

// GetNamespaceMetrics returns the collected namespace metrics
func (c *Client) GetNamespaceMetrics() []NamespaceMetric {
	c.cacheMutex.RLock()
	defer c.cacheMutex.RUnlock()
	return c.namespaceMetrics
}

// GetEntityMetrics returns the collected entity metrics
func (c *Client) GetEntityMetrics() []EntityMetric {
	c.cacheMutex.RLock()
	defer c.cacheMutex.RUnlock()
	return c.entityMetrics
}

// CollectMetrics collects metrics from Azure Monitor
func (c *Client) CollectMetrics(ctx context.Context) error {
	// Önbellek kontrolü
	c.cacheMutex.RLock()
	if time.Since(c.lastUpdate) < c.cfg.Metrics.CacheDuration {
		c.cacheMutex.RUnlock()
		return nil
	}
	c.cacheMutex.RUnlock()

	// Önbelleği yazma için kilitle
	c.cacheMutex.Lock()
	defer c.cacheMutex.Unlock()

	// Metrik listelerini temizle
	c.namespaceMetrics = []NamespaceMetric{}
	c.entityMetrics = []EntityMetric{}

	// Azure Monitor client'ı oluştur
	if c.metricsClient == nil {
		client, err := c.auth.GetMonitorClient(ctx)
		if err != nil {
			return err
		}
		c.metricsClient = client
	}

	// Her subscription için metrikleri topla
	for _, subID := range c.cfg.AzureMonitor.SubscriptionIDs {
		if err := c.collectServiceBusMetrics(ctx, subID); err != nil {
			c.log.WithError(err).WithField("subscription", subID).Error("Failed to collect Service Bus metrics")
		}
	}

	c.lastUpdate = time.Now()
	return nil
}

// collectServiceBusMetrics collects Service Bus metrics from Azure Monitor
func (c *Client) collectServiceBusMetrics(ctx context.Context, subscriptionID string) error {
	// Service Bus namespace'leri bul
	c.log.WithField("subscription", subscriptionID).Info("Collecting Service Bus metrics")

	// NOT: Gerçek projede, burada Azure REST API kullanarak Service Bus namespace'lerini
	// bulmanız gerekecek. Basitleştirmek için, konfigürasyondan namespace listesini kullanıyoruz.
	for _, namespace := range c.cfg.ServiceBus.Namespaces {
		resourceID := fmt.Sprintf("/subscriptions/%s/resourceGroups/%s/providers/Microsoft.ServiceBus/namespaces/%s",
			subscriptionID, "YOUR-RESOURCE-GROUP", namespace)

		if err := c.collectNamespaceMetrics(ctx, resourceID, namespace); err != nil {
			c.log.WithError(err).WithField("namespace", namespace).Error("Failed to collect namespace metrics")
		}

		// Ayrıca, bu namespace içindeki entity'ler (kuyruklar, konular, abonelikler) için de metrik toplayın
		if err := c.collectEntityMetrics(ctx, resourceID, namespace); err != nil {
			c.log.WithError(err).WithField("namespace", namespace).Error("Failed to collect entity metrics")
		}
	}

	return nil
}

// collectNamespaceMetrics collects metrics for a specific namespace
func (c *Client) collectNamespaceMetrics(ctx context.Context, resourceID, namespace string) error {
	// Namespace metriklerini tanımla
	metricNames := []string{
		"SuccessfulRequests",
		"ServerErrors",
		"UserErrors",
		"ThrottledRequests",
		"ActiveConnections",
	}

	// Premium katman için ek metrikler
	premiumMetrics := []string{
		"CPU",
		"Memory",
	}

	// Tüm metrikleri birleştir
	allMetrics := append(metricNames, premiumMetrics...)

	// Azure Monitor API'den metrikleri al
	timespan := fmt.Sprintf("%s/%s", time.Now().Add(-c.cfg.Metrics.ScrapeInterval).Format(time.RFC3339), time.Now().Format(time.RFC3339))

	// Bu kısım gerçek API çağrısıyla değiştirilmelidir - burada örnek olarak test verileri kullanıyoruz
	c.log.WithFields(logrus.Fields{
		"resourceID": resourceID,
		"timespan":   timespan,
		"metrics":    allMetrics,
	}).Info("Fetching namespace metrics from Azure Monitor API")

	// Test verileri oluştur
	for _, metricName := range allMetrics {
		var value float64

		// Örnek değerler (gerçek uygulamada API'dan alınır)
		switch metricName {
		case "SuccessfulRequests":
			value = 950
		case "ServerErrors":
			value = 5
		case "UserErrors":
			value = 20
		case "ThrottledRequests":
			value = 0
		case "ActiveConnections":
			value = 10
		case "CPU":
			value = 15 // %15 CPU kullanımı
		case "Memory":
			value = 30 // %30 Bellek kullanımı
		default:
			value = 0
		}

		// Metrik yapısı oluştur
		namespaceMetric := NamespaceMetric{
			Namespace:  namespace,
			MetricName: metricName,
			Value:      value,
		}

		// Metriği listeye ekle
		c.namespaceMetrics = append(c.namespaceMetrics, namespaceMetric)

		// Prometheus metriğini de güncelle (eski kod)
		promMetricName := metrics.AzureMonitorMetricToPrometheusMetric(metricName)

		labels := prometheus.Labels{
			"resource_id": resourceID,
			"namespace":   namespace,
			"metric":      metricName,
		}

		if gauge, ok := c.metrics[promMetricName]; ok {
			gauge.With(labels).Set(value)
		}
	}

	return nil
}

// collectEntityMetrics collects metrics for entities within a namespace
func (c *Client) collectEntityMetrics(ctx context.Context, resourceID, namespace string) error {
	// Entity metriklerini tanımla
	metricNames := []string{
		"IncomingMessages",
		"OutgoingMessages",
		"ActiveMessages",
		"DeadletteredMessages",
		"ScheduledMessages",
	}

	// Bu kısımda, bir namespace içindeki tüm kuyruklar, konular ve abonelikler için
	// Azure Monitor API'sini kullanarak metrik toplamamız gerekir

	// Gerçek uygulamada, ResourceGraph veya Management API kullanarak
	// entities listesi alınmalıdır

	// Basitleştirmek için, sabit entity listesi kullanıyoruz
	// Gerçek uygulamada, bu bilgiler dinamik olarak alınmalıdır
	entities := []struct {
		Name string
		Type string
	}{
		{"queue1", "queue"},
		{"queue2", "queue"},
		{"topic1", "topic"},
		{"topic1/subscription1", "subscription"},
		{"topic1/subscription2", "subscription"},
	}

	c.log.WithFields(logrus.Fields{
		"resourceID": resourceID,
		"namespace":  namespace,
		"metrics":    metricNames,
	}).Info("Fetching entity metrics from Azure Monitor API")

	// Entities için metrikleri topla
	for _, entity := range entities {
		for _, metricName := range metricNames {
			var value float64

			// Basitleştirmek için sabit test değerleri kullanıyoruz
			// Gerçek uygulamada, bu değerler Azure Monitor API'sından alınır
			switch metricName {
			case "IncomingMessages":
				value = 100
			case "OutgoingMessages":
				value = 95
			case "ActiveMessages":
				if entity.Type == "queue" {
					value = 10
				} else if entity.Type == "topic" {
					value = 5
				} else { // subscription
					value = 3
				}
			case "DeadletteredMessages":
				if entity.Type == "queue" {
					value = 1
				} else if entity.Type == "topic" {
					value = 0
				} else { // subscription
					value = 0
				}
			case "ScheduledMessages":
				if entity.Type == "queue" {
					value = 2
				} else if entity.Type == "topic" {
					value = 1
				} else { // subscription
					value = 0
				}
			default:
				value = 0
			}

			// Metrik yapısı oluştur
			entityMetric := EntityMetric{
				Namespace:  namespace,
				EntityName: entity.Name,
				EntityType: entity.Type,
				MetricName: metricName,
				Value:      value,
			}

			// Metriği listeye ekle
			c.entityMetrics = append(c.entityMetrics, entityMetric)
		}
	}

	return nil
}
