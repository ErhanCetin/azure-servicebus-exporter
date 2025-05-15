package collector

import (
	"context"
	"strings"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/sirupsen/logrus"

	"azure-servicebus-exporter/pkg/azuremonitor"
	"azure-servicebus-exporter/pkg/config"
	"azure-servicebus-exporter/pkg/servicebus"
)

// UsageMetric represents a quota usage metric
type UsageMetric struct {
	Namespace string
	QuotaName string
	Value     float64
}

// PerformanceMetric represents a performance metric
type PerformanceMetric struct {
	Namespace  string
	EntityName string
	EntityType string
	MetricName string
	Operation  string
	Value      float64
}

// ServiceBusCollector implements prometheus.Collector for Service Bus metrics
type ServiceBusCollector struct {
	cfg          *config.Config
	log          *logrus.Logger
	serviceBus   *servicebus.Client
	azureMonitor *azuremonitor.Client

	// Collection state
	lastCollect time.Time
	mutex       sync.Mutex

	// Metrics
	up                 *prometheus.Desc
	scrapeErrors       *prometheus.Desc
	scrapeDuration     *prometheus.Desc
	activeMessages     *prometheus.Desc
	deadLetterMessages *prometheus.Desc
	scheduledMessages  *prometheus.Desc
	transferMessages   *prometheus.Desc
	sizeBytes          *prometheus.Desc
	incomingMessages   *prometheus.Desc
	outgoingMessages   *prometheus.Desc
	activeConnections  *prometheus.Desc
	cpuUsage           *prometheus.Desc
	memoryUsage        *prometheus.Desc
	quotaUsage         *prometheus.Desc
	requestLatency     *prometheus.Desc
	requestsSucceeded  *prometheus.Desc
	requestsFailed     *prometheus.Desc
}

// NewServiceBusCollector creates a new collector
func NewServiceBusCollector(
	cfg *config.Config,
	log *logrus.Logger,
	sbClient *servicebus.Client,
	amClient *azuremonitor.Client,
) *ServiceBusCollector {
	collector := &ServiceBusCollector{
		cfg:          cfg,
		log:          log,
		serviceBus:   sbClient,
		azureMonitor: amClient,

		// Metrik tanımlamaları
		up: prometheus.NewDesc(
			"azure_servicebus_up",
			"Indicates if the Service Bus exporter is able to collect metrics",
			nil, nil,
		),
		scrapeErrors: prometheus.NewDesc(
			"azure_servicebus_scrape_errors_total",
			"Number of scrape errors",
			nil, nil,
		),
		scrapeDuration: prometheus.NewDesc(
			"azure_servicebus_scrape_duration_seconds",
			"Duration of metrics collection",
			nil, nil,
		),
		activeMessages: prometheus.NewDesc(
			"azure_servicebus_active_messages",
			"Number of active messages in the entity",
			[]string{"namespace", "entity_name", "entity_type"}, nil,
		),
		deadLetterMessages: prometheus.NewDesc(
			"azure_servicebus_dead_letter_messages",
			"Number of dead letter messages in the entity",
			[]string{"namespace", "entity_name", "entity_type"}, nil,
		),
		scheduledMessages: prometheus.NewDesc(
			"azure_servicebus_scheduled_messages",
			"Number of scheduled messages in the entity",
			[]string{"namespace", "entity_name", "entity_type"}, nil,
		),
		transferMessages: prometheus.NewDesc(
			"azure_servicebus_transfer_messages",
			"Number of transfer messages in the entity",
			[]string{"namespace", "entity_name", "entity_type"}, nil,
		),
		sizeBytes: prometheus.NewDesc(
			"azure_servicebus_size_bytes",
			"Size of the entity in bytes",
			[]string{"namespace", "entity_name", "entity_type"}, nil,
		),
		incomingMessages: prometheus.NewDesc(
			"azure_servicebus_incoming_messages",
			"Number of incoming messages",
			[]string{"namespace", "entity_name", "entity_type"}, nil,
		),
		outgoingMessages: prometheus.NewDesc(
			"azure_servicebus_outgoing_messages",
			"Number of outgoing messages",
			[]string{"namespace", "entity_name", "entity_type"}, nil,
		),
		activeConnections: prometheus.NewDesc(
			"azure_servicebus_active_connections",
			"Number of active connections",
			[]string{"namespace"}, nil,
		),
		cpuUsage: prometheus.NewDesc(
			"azure_servicebus_cpu_usage",
			"CPU usage percentage",
			[]string{"namespace"}, nil,
		),
		memoryUsage: prometheus.NewDesc(
			"azure_servicebus_memory_usage",
			"Memory usage percentage",
			[]string{"namespace"}, nil,
		),
		quotaUsage: prometheus.NewDesc(
			"azure_servicebus_quota_usage_percentage",
			"Percentage of quota used",
			[]string{"namespace", "quota_name"}, nil,
		),
		requestLatency: prometheus.NewDesc(
			"azure_servicebus_request_latency_ms",
			"Request latency in milliseconds",
			[]string{"namespace", "entity_name", "entity_type", "operation"}, nil,
		),
		requestsSucceeded: prometheus.NewDesc(
			"azure_servicebus_requests_succeeded",
			"Number of successful requests",
			[]string{"namespace", "entity_name", "entity_type", "operation"}, nil,
		),
		requestsFailed: prometheus.NewDesc(
			"azure_servicebus_requests_failed",
			"Number of failed requests",
			[]string{"namespace", "entity_name", "entity_type", "operation"}, nil,
		),
	}

	return collector
}

// Describe implements prometheus.Collector
func (c *ServiceBusCollector) Describe(ch chan<- *prometheus.Desc) {
	ch <- c.up
	ch <- c.scrapeErrors
	ch <- c.scrapeDuration
	ch <- c.activeMessages
	ch <- c.deadLetterMessages
	ch <- c.scheduledMessages
	ch <- c.transferMessages
	ch <- c.sizeBytes
	ch <- c.incomingMessages
	ch <- c.outgoingMessages
	ch <- c.activeConnections
	ch <- c.cpuUsage
	ch <- c.memoryUsage
	ch <- c.quotaUsage
	ch <- c.requestLatency
	ch <- c.requestsSucceeded
	ch <- c.requestsFailed
}

// Collect implements prometheus.Collector
func (c *ServiceBusCollector) Collect(ch chan<- prometheus.Metric) {
	c.mutex.Lock()
	defer c.mutex.Unlock()

	startTime := time.Now()

	// Up metriği
	var up float64 = 1

	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()

	// Metrik toplama stratejisini belirle
	var collectErr error

	// Önce connection string ile direkt toplamayı dene
	if c.serviceBus != nil {
		if err := c.serviceBus.CollectMetrics(ctx); err != nil {
			c.log.WithError(err).Warn("Failed to collect metrics via Service Bus API")
			collectErr = err
		} else {
			collectErr = nil
		}
	}

	// Eğer direkt toplama başarısız olursa veya yapılandırılmamışsa, Azure Monitor'ü dene
	if (collectErr != nil || c.serviceBus == nil) && c.azureMonitor != nil {
		if err := c.azureMonitor.CollectMetrics(ctx); err != nil {
			c.log.WithError(err).Warn("Failed to collect metrics via Azure Monitor API")
			collectErr = err
			up = 0
		} else {
			collectErr = nil
		}
	}

	// Up metriğini gönder
	ch <- prometheus.MustNewConstMetric(c.up, prometheus.GaugeValue, up)

	// Toplama süresi metriğini gönder
	duration := time.Since(startTime).Seconds()
	ch <- prometheus.MustNewConstMetric(c.scrapeDuration, prometheus.GaugeValue, duration)

	// Hata yoksa metrikleri Prometheus'a gönder
	if collectErr == nil {
		// Service Bus metriklerini topla ve gönder
		if c.serviceBus != nil {
			// Queue metriklerini gönder
			for _, queue := range c.serviceBus.GetQueueMetrics() {
				// Active messages
				ch <- prometheus.MustNewConstMetric(
					c.activeMessages,
					prometheus.GaugeValue,
					queue.ActiveMessages,
					queue.Namespace, queue.Name, "queue",
				)

				// Dead letter messages
				ch <- prometheus.MustNewConstMetric(
					c.deadLetterMessages,
					prometheus.GaugeValue,
					queue.DeadLetterMessages,
					queue.Namespace, queue.Name, "queue",
				)

				// Scheduled messages
				ch <- prometheus.MustNewConstMetric(
					c.scheduledMessages,
					prometheus.GaugeValue,
					queue.ScheduledMessages,
					queue.Namespace, queue.Name, "queue",
				)

				// Transfer messages
				ch <- prometheus.MustNewConstMetric(
					c.transferMessages,
					prometheus.GaugeValue,
					queue.TransferMessages,
					queue.Namespace, queue.Name, "queue",
				)

				// Size bytes
				ch <- prometheus.MustNewConstMetric(
					c.sizeBytes,
					prometheus.GaugeValue,
					queue.SizeBytes,
					queue.Namespace, queue.Name, "queue",
				)

				// Incoming messages
				ch <- prometheus.MustNewConstMetric(
					c.incomingMessages,
					prometheus.GaugeValue,
					queue.IncomingMessages,
					queue.Namespace, queue.Name, "queue",
				)

				// Outgoing messages
				ch <- prometheus.MustNewConstMetric(
					c.outgoingMessages,
					prometheus.GaugeValue,
					queue.OutgoingMessages,
					queue.Namespace, queue.Name, "queue",
				)
			}

			// Topic metriklerini gönder
			for _, topic := range c.serviceBus.GetTopicMetrics() {
				// Topic başına metrikler
				ch <- prometheus.MustNewConstMetric(
					c.activeMessages,
					prometheus.GaugeValue,
					topic.ActiveMessages,
					topic.Namespace, topic.Name, "topic",
				)

				// Size bytes
				ch <- prometheus.MustNewConstMetric(
					c.sizeBytes,
					prometheus.GaugeValue,
					topic.SizeBytes,
					topic.Namespace, topic.Name, "topic",
				)

				// Incoming messages
				ch <- prometheus.MustNewConstMetric(
					c.incomingMessages,
					prometheus.GaugeValue,
					topic.IncomingMessages,
					topic.Namespace, topic.Name, "topic",
				)

				// Outgoing messages
				ch <- prometheus.MustNewConstMetric(
					c.outgoingMessages,
					prometheus.GaugeValue,
					topic.OutgoingMessages,
					topic.Namespace, topic.Name, "topic",
				)
			}

			// Subscription metriklerini gönder
			for _, sub := range c.serviceBus.GetSubscriptionMetrics() {
				// Subscription başına metrikler
				ch <- prometheus.MustNewConstMetric(
					c.activeMessages,
					prometheus.GaugeValue,
					sub.ActiveMessages,
					sub.Namespace, sub.TopicName+"/"+sub.Name, "subscription",
				)

				ch <- prometheus.MustNewConstMetric(
					c.deadLetterMessages,
					prometheus.GaugeValue,
					sub.DeadLetterMessages,
					sub.Namespace, sub.TopicName+"/"+sub.Name, "subscription",
				)

				ch <- prometheus.MustNewConstMetric(
					c.scheduledMessages,
					prometheus.GaugeValue,
					sub.ScheduledMessages,
					sub.Namespace, sub.TopicName+"/"+sub.Name, "subscription",
				)

				ch <- prometheus.MustNewConstMetric(
					c.transferMessages,
					prometheus.GaugeValue,
					sub.TransferMessages,
					sub.Namespace, sub.TopicName+"/"+sub.Name, "subscription",
				)

				ch <- prometheus.MustNewConstMetric(
					c.incomingMessages,
					prometheus.GaugeValue,
					sub.IncomingMessages,
					sub.Namespace, sub.TopicName+"/"+sub.Name, "subscription",
				)

				ch <- prometheus.MustNewConstMetric(
					c.outgoingMessages,
					prometheus.GaugeValue,
					sub.OutgoingMessages,
					sub.Namespace, sub.TopicName+"/"+sub.Name, "subscription",
				)
			}

			// Namespace metriklerini gönder
			for _, ns := range c.serviceBus.GetNamespaceMetrics() {
				ch <- prometheus.MustNewConstMetric(
					c.activeConnections,
					prometheus.GaugeValue,
					ns.ActiveConnections,
					ns.Namespace,
				)

				ch <- prometheus.MustNewConstMetric(
					c.cpuUsage,
					prometheus.GaugeValue,
					ns.CPUUsage,
					ns.Namespace,
				)

				ch <- prometheus.MustNewConstMetric(
					c.memoryUsage,
					prometheus.GaugeValue,
					ns.MemoryUsage,
					ns.Namespace,
				)

				// Quota usage metrics
				for quotaName, value := range ns.QuotaUsage {
					ch <- prometheus.MustNewConstMetric(
						c.quotaUsage,
						prometheus.GaugeValue,
						value,
						ns.Namespace, quotaName,
					)
				}
			}
		}

		// Azure Monitor metriklerini topla ve gönder
		if c.azureMonitor != nil {
			// Namespace metriklerini gönder
			for _, nsMetric := range c.azureMonitor.GetNamespaceMetrics() {
				// Her metrik için etiketler oluştur
				labels := []string{nsMetric.Namespace}

				// Namespace başına metrikler
				// Her metrik tipini ayrı bir prometheus metriği olarak gönder
				switch nsMetric.MetricName {
				case "SuccessfulRequests":
					ch <- prometheus.MustNewConstMetric(
						prometheus.NewDesc(
							"azure_servicebus_successful_requests",
							"Number of successful requests",
							[]string{"namespace"}, nil,
						),
						prometheus.GaugeValue,
						nsMetric.Value,
						labels...,
					)
				case "ServerErrors":
					ch <- prometheus.MustNewConstMetric(
						prometheus.NewDesc(
							"azure_servicebus_server_errors",
							"Number of server errors",
							[]string{"namespace"}, nil,
						),
						prometheus.GaugeValue,
						nsMetric.Value,
						labels...,
					)
				case "UserErrors":
					ch <- prometheus.MustNewConstMetric(
						prometheus.NewDesc(
							"azure_servicebus_user_errors",
							"Number of user errors",
							[]string{"namespace"}, nil,
						),
						prometheus.GaugeValue,
						nsMetric.Value,
						labels...,
					)
				case "ThrottledRequests":
					ch <- prometheus.MustNewConstMetric(
						prometheus.NewDesc(
							"azure_servicebus_throttled_requests",
							"Number of throttled requests",
							[]string{"namespace"}, nil,
						),
						prometheus.GaugeValue,
						nsMetric.Value,
						labels...,
					)
				case "ActiveConnections":
					ch <- prometheus.MustNewConstMetric(
						c.activeConnections,
						prometheus.GaugeValue,
						nsMetric.Value,
						labels...,
					)
				case "CPU":
					ch <- prometheus.MustNewConstMetric(
						c.cpuUsage,
						prometheus.GaugeValue,
						nsMetric.Value,
						labels...,
					)
				case "Memory":
					ch <- prometheus.MustNewConstMetric(
						c.memoryUsage,
						prometheus.GaugeValue,
						nsMetric.Value,
						labels...,
					)
				}
			}

			// Entity metriklerini gönder (kuyruk, konu ve abonelikler için)
			for _, entityMetric := range c.azureMonitor.GetEntityMetrics() {
				// Her metrik için etiketler oluştur
				labels := []string{entityMetric.Namespace, entityMetric.EntityName, entityMetric.EntityType}

				// Entity başına metrikler
				switch entityMetric.MetricName {
				case "IncomingMessages":
					ch <- prometheus.MustNewConstMetric(
						c.incomingMessages,
						prometheus.GaugeValue,
						entityMetric.Value,
						labels...,
					)
				case "OutgoingMessages":
					ch <- prometheus.MustNewConstMetric(
						c.outgoingMessages,
						prometheus.GaugeValue,
						entityMetric.Value,
						labels...,
					)
				case "ActiveMessages":
					ch <- prometheus.MustNewConstMetric(
						c.activeMessages,
						prometheus.GaugeValue,
						entityMetric.Value,
						labels...,
					)
				case "DeadletteredMessages":
					ch <- prometheus.MustNewConstMetric(
						c.deadLetterMessages,
						prometheus.GaugeValue,
						entityMetric.Value,
						labels...,
					)
				case "ScheduledMessages":
					ch <- prometheus.MustNewConstMetric(
						c.scheduledMessages,
						prometheus.GaugeValue,
						entityMetric.Value,
						labels...,
					)
				case "Size":
					ch <- prometheus.MustNewConstMetric(
						c.sizeBytes,
						prometheus.GaugeValue,
						entityMetric.Value,
						labels...,
					)
				}

				// Performance metrics with operation
				if entityMetric.Operation != "" {
					operationLabels := []string{
						entityMetric.Namespace,
						entityMetric.EntityName,
						entityMetric.EntityType,
						entityMetric.Operation,
					}

					switch entityMetric.MetricName {
					case "RequestLatency":
						ch <- prometheus.MustNewConstMetric(
							c.requestLatency,
							prometheus.GaugeValue,
							entityMetric.Value,
							operationLabels...,
						)
					case "RequestsSucceeded":
						ch <- prometheus.MustNewConstMetric(
							c.requestsSucceeded,
							prometheus.GaugeValue,
							entityMetric.Value,
							operationLabels...,
						)
					case "RequestsFailed":
						ch <- prometheus.MustNewConstMetric(
							c.requestsFailed,
							prometheus.GaugeValue,
							entityMetric.Value,
							operationLabels...,
						)
					}
				}
			}

			// Entity bazında kullanım istatistikleri
			for _, usage := range c.getUsageMetrics() {
				ch <- prometheus.MustNewConstMetric(
					c.quotaUsage,
					prometheus.GaugeValue,
					usage.Value,
					usage.Namespace, usage.QuotaName,
				)
			}
		}
	} else {
		// Hata metriğini gönder
		ch <- prometheus.MustNewConstMetric(c.scrapeErrors, prometheus.CounterValue, 1)
	}

	c.lastCollect = time.Now()
}

// GetUsageMetrics returns namespace quota usage metrics
func (c *ServiceBusCollector) getUsageMetrics() []UsageMetric {
	// If Azure Monitor client available, use its data
	if c.azureMonitor != nil {
		// Extract quota usage metrics from Azure Monitor data
		var usageMetrics []UsageMetric

		for _, nsMetric := range c.azureMonitor.GetNamespaceMetrics() {
			// For namespace metrics that represent quota usage
			if strings.HasSuffix(nsMetric.MetricName, "Quota") ||
				strings.Contains(nsMetric.MetricName, "Usage") ||
				strings.Contains(nsMetric.MetricName, "Utilization") {

				// Format quota name from metric name
				quotaName := strings.ToLower(strings.TrimSuffix(nsMetric.MetricName, "Quota"))
				quotaName = strings.ToLower(strings.TrimSuffix(quotaName, "Usage"))
				quotaName = strings.ToLower(strings.TrimSuffix(quotaName, "Utilization"))

				usageMetric := UsageMetric{
					Namespace: nsMetric.Namespace,
					QuotaName: quotaName,
					Value:     nsMetric.Value,
				}

				usageMetrics = append(usageMetrics, usageMetric)
			}
		}

		// If we found usage metrics, return them
		if len(usageMetrics) > 0 {
			return usageMetrics
		}
	}

	// Otherwise, if Service Bus client available, use its data
	if c.serviceBus != nil {
		var usageMetrics []UsageMetric

		// Extract quota usage metrics from Service Bus namespace data
		for _, nsMetric := range c.serviceBus.GetNamespaceMetrics() {
			for quotaName, value := range nsMetric.QuotaUsage {
				usageMetric := UsageMetric{
					Namespace: nsMetric.Namespace,
					QuotaName: quotaName,
					Value:     value,
				}

				usageMetrics = append(usageMetrics, usageMetric)
			}
		}

		return usageMetrics
	}

	// If no metrics are available, return empty slice
	return []UsageMetric{}
}

// GetPerformanceMetrics returns performance metrics
func (c *ServiceBusCollector) getPerformanceMetrics() []PerformanceMetric {
	// If Azure Monitor client available, use its data
	if c.azureMonitor != nil {
		var perfMetrics []PerformanceMetric

		// Extract performance metrics from Azure Monitor data
		for _, entityMetric := range c.azureMonitor.GetEntityMetrics() {
			// Only include performance metrics with operation data
			if entityMetric.Operation != "" {
				perfMetric := PerformanceMetric{
					Namespace:  entityMetric.Namespace,
					EntityName: entityMetric.EntityName,
					EntityType: entityMetric.EntityType,
					MetricName: entityMetric.MetricName,
					Operation:  entityMetric.Operation,
					Value:      entityMetric.Value,
				}

				perfMetrics = append(perfMetrics, perfMetric)
			}
		}

		return perfMetrics
	}

	// If no metrics are available, return empty slice
	return []PerformanceMetric{}
}
