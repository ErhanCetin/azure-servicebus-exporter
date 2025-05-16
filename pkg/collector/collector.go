package collector

import (
	"context"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/sirupsen/logrus"

	"azure-servicebus-exporter/pkg/config"
	"azure-servicebus-exporter/pkg/servicebus"
)

// UsageMetric represents a quota usage metric
type UsageMetric struct {
	Namespace string
	QuotaName string
	Value     float64
}

// ServiceBusCollector implements prometheus.Collector for Service Bus metrics
type ServiceBusCollector struct {
	cfg        *config.Config
	log        *logrus.Logger
	serviceBus *servicebus.Client

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
	quotaUsage         *prometheus.Desc

	totalMessages     *prometheus.Desc
	queueCreatedTime  *prometheus.Desc
	queueUpdatedTime  *prometheus.Desc
	queueAccessedTime *prometheus.Desc
}

// NewServiceBusCollector creates a new collector
func NewServiceBusCollector(
	cfg *config.Config,
	log *logrus.Logger,
	sbClient *servicebus.Client,
) *ServiceBusCollector {
	collector := &ServiceBusCollector{
		cfg:        cfg,
		log:        log,
		serviceBus: sbClient,

		// Metric definitions
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
		quotaUsage: prometheus.NewDesc(
			"azure_servicebus_quota_usage_percentage",
			"Percentage of quota used",
			[]string{"namespace", "quota_name"}, nil,
		),
		totalMessages: prometheus.NewDesc(
			"azure_servicebus_total_messages",
			"Total number of messages in the entity",
			[]string{"namespace", "entity_name", "entity_type"}, nil,
		),
		queueCreatedTime: prometheus.NewDesc(
			"azure_servicebus_queue_created_time",
			"Time when the queue was created, in Unix timestamp format",
			[]string{"namespace", "entity_name"}, nil,
		),
		queueUpdatedTime: prometheus.NewDesc(
			"azure_servicebus_queue_updated_time",
			"Time when the queue was last updated, in Unix timestamp format",
			[]string{"namespace", "entity_name"}, nil,
		),
		queueAccessedTime: prometheus.NewDesc(
			"azure_servicebus_queue_accessed_time",
			"Time when the queue was last accessed, in Unix timestamp format",
			[]string{"namespace", "entity_name"}, nil,
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
	ch <- c.quotaUsage
	ch <- c.totalMessages
	ch <- c.queueCreatedTime
	ch <- c.queueUpdatedTime
	ch <- c.queueAccessedTime
}

// Collect implements prometheus.Collector
func (c *ServiceBusCollector) Collect(ch chan<- prometheus.Metric) {
	c.mutex.Lock()
	defer c.mutex.Unlock()

	startTime := time.Now()

	// Check if enough time has passed since the last collect based on scrape_interval
	if time.Since(c.lastCollect) < c.cfg.Metrics.ScrapeInterval && !c.lastCollect.IsZero() {
		c.log.Debug("Skipping collection, scrape interval not expired yet")
		// Still send up metric
		ch <- prometheus.MustNewConstMetric(c.up, prometheus.GaugeValue, 1)
		return
	}

	// Up metric
	var up float64 = 1

	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()

	// Collect metrics
	if err := c.serviceBus.CollectMetrics(ctx); err != nil {
		c.log.WithError(err).Error("Failed to collect metrics via Service Bus API")
		up = 0
	}

	// Send up metric
	ch <- prometheus.MustNewConstMetric(c.up, prometheus.GaugeValue, up)

	// Send duration metric
	duration := time.Since(startTime).Seconds()
	ch <- prometheus.MustNewConstMetric(c.scrapeDuration, prometheus.GaugeValue, duration)

	// If up, send collected metrics
	if up == 1 {
		// Queue metrics
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
			// Total messages
			ch <- prometheus.MustNewConstMetric(
				c.totalMessages,
				prometheus.GaugeValue,
				queue.TotalMessages,
				queue.Namespace, queue.Name, "queue",
			)

			// Queue created time
			if !queue.CreatedAt.IsZero() {
				ch <- prometheus.MustNewConstMetric(
					c.queueCreatedTime,
					prometheus.GaugeValue,
					float64(queue.CreatedAt.Unix()),
					queue.Namespace, queue.Name,
				)
			}

			// Queue updated time
			if !queue.UpdatedAt.IsZero() {
				ch <- prometheus.MustNewConstMetric(
					c.queueUpdatedTime,
					prometheus.GaugeValue,
					float64(queue.UpdatedAt.Unix()),
					queue.Namespace, queue.Name,
				)
			}

			// Queue accessed time
			if !queue.AccessedAt.IsZero() {
				ch <- prometheus.MustNewConstMetric(
					c.queueAccessedTime,
					prometheus.GaugeValue,
					float64(queue.AccessedAt.Unix()),
					queue.Namespace, queue.Name,
				)
			}
		}

		// Topic metrics
		for _, topic := range c.serviceBus.GetTopicMetrics() {
			// Size bytes
			ch <- prometheus.MustNewConstMetric(
				c.sizeBytes,
				prometheus.GaugeValue,
				topic.SizeBytes,
				topic.Namespace, topic.Name, "topic",
			)
		}

		// Subscription metrics
		for _, sub := range c.serviceBus.GetSubscriptionMetrics() {
			// Active messages
			ch <- prometheus.MustNewConstMetric(
				c.activeMessages,
				prometheus.GaugeValue,
				sub.ActiveMessages,
				sub.Namespace, sub.TopicName+"/"+sub.Name, "subscription",
			)

			// Dead letter messages
			ch <- prometheus.MustNewConstMetric(
				c.deadLetterMessages,
				prometheus.GaugeValue,
				sub.DeadLetterMessages,
				sub.Namespace, sub.TopicName+"/"+sub.Name, "subscription",
			)

			// Scheduled messages
			ch <- prometheus.MustNewConstMetric(
				c.scheduledMessages,
				prometheus.GaugeValue,
				sub.ScheduledMessages,
				sub.Namespace, sub.TopicName+"/"+sub.Name, "subscription",
			)

			// Transfer messages
			ch <- prometheus.MustNewConstMetric(
				c.transferMessages,
				prometheus.GaugeValue,
				sub.TransferMessages,
				sub.Namespace, sub.TopicName+"/"+sub.Name, "subscription",
			)
		}

		// Namespace metrics
		for _, ns := range c.serviceBus.GetNamespaceMetrics() {
			// Active connections
			ch <- prometheus.MustNewConstMetric(
				c.activeConnections,
				prometheus.GaugeValue,
				ns.ActiveConnections,
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
	} else {
		// Send error metric
		ch <- prometheus.MustNewConstMetric(c.scrapeErrors, prometheus.CounterValue, 1)
	}

	c.lastCollect = time.Now()
}

// GetUsageMetrics returns namespace quota usage metrics
func (c *ServiceBusCollector) getUsageMetrics() []UsageMetric {
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

func (c *ServiceBusCollector) GetServiceBus() *servicebus.Client {
    return c.serviceBus
}