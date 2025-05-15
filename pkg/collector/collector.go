package collector

import (
	"context"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/sirupsen/logrus"

	"azure-servicebus-exporter/pkg/azuremonitor"
	"azure-servicebus-exporter/pkg/config"
	"azure-servicebus-exporter/pkg/servicebus"
)

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
	// ... diğer metrikler
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
		// ... diğer metrik tanımlamaları
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
	// ... diğer metrik tanımlamalarını gönder
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

				// Diğer metrikleri de benzer şekilde gönder
				// Scheduled messages, transfer messages, size, vb.
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

				// Diğer topic metriklerini de gönder
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

				// Diğer subscription metriklerini de gönder
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
						prometheus.NewDesc(
							"azure_servicebus_active_connections",
							"Number of active connections",
							[]string{"namespace"}, nil,
						),
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
						prometheus.NewDesc(
							"azure_servicebus_incoming_messages",
							"Number of incoming messages",
							[]string{"namespace", "entity_name", "entity_type"}, nil,
						),
						prometheus.GaugeValue,
						entityMetric.Value,
						labels...,
					)
				case "OutgoingMessages":
					ch <- prometheus.MustNewConstMetric(
						prometheus.NewDesc(
							"azure_servicebus_outgoing_messages",
							"Number of outgoing messages",
							[]string{"namespace", "entity_name", "entity_type"}, nil,
						),
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
						prometheus.NewDesc(
							"azure_servicebus_scheduled_messages",
							"Number of scheduled messages",
							[]string{"namespace", "entity_name", "entity_type"}, nil,
						),
						prometheus.GaugeValue,
						entityMetric.Value,
						labels...,
					)
				}
			}
		}
	} else {
		// Hata metriğini gönder
		ch <- prometheus.MustNewConstMetric(c.scrapeErrors, prometheus.CounterValue, 1)
	}

	c.lastCollect = time.Now()
}
