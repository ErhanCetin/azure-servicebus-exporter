package servicebus

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/messaging/azservicebus"
	"github.com/Azure/azure-sdk-for-go/sdk/messaging/azservicebus/admin"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/monitor/armmonitor"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/servicebus/armservicebus"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/sirupsen/logrus"

	"azure-servicebus-exporter/pkg/auth"
	"azure-servicebus-exporter/pkg/config"
	"azure-servicebus-exporter/pkg/metrics"
)

// QueueMetric represents metrics for a queue
type QueueMetric struct {
	Namespace           string
	Name                string
	ActiveMessages      float64
	DeadLetterMessages  float64
	ScheduledMessages   float64
	TransferMessages    float64
	TransferDLQMessages float64
	SizeBytes           float64
	MaxSizeBytes        float64
	IncomingMessages    float64
	OutgoingMessages    float64
	MessageSize         float64
	OldestMessageAge    float64
}

// TopicMetric represents metrics for a topic
type TopicMetric struct {
	Namespace         string
	Name              string
	ActiveMessages    float64
	SizeBytes         float64
	MaxSizeBytes      float64
	IncomingMessages  float64
	OutgoingMessages  float64
	SubscriptionCount float64
}

// SubscriptionMetric represents metrics for a subscription
type SubscriptionMetric struct {
	Namespace           string
	TopicName           string
	Name                string
	ActiveMessages      float64
	DeadLetterMessages  float64
	ScheduledMessages   float64
	TransferMessages    float64
	TransferDLQMessages float64
	IncomingMessages    float64
	OutgoingMessages    float64
	OldestMessageAge    float64
}

// NamespaceMetric represents metrics for a namespace
type NamespaceMetric struct {
	Namespace         string
	ActiveConnections float64
	CPUUsage          float64
	MemoryUsage       float64
	QuotaUsage        map[string]float64
}

// Client represents a Service Bus client that can collect metrics
type Client struct {
	cfg         *config.Config
	auth        auth.AuthProvider
	sbClient    *azservicebus.Client
	adminClient *admin.Client
	log         *logrus.Logger
	namespace   string

	// Regex filters
	entityFilter *regexp.Regexp

	// Metrics
	metrics map[string]*prometheus.GaugeVec

	// Cached metrics
	queueMetrics        []QueueMetric
	topicMetrics        []TopicMetric
	subscriptionMetrics []SubscriptionMetric
	namespaceMetrics    []NamespaceMetric

	// Cache
	cache      map[string]metrics.MetricValue
	cacheMutex sync.RWMutex
	lastUpdate time.Time
}

// NewClient creates a new Service Bus client
func NewClient(cfg *config.Config, authProvider auth.AuthProvider, log *logrus.Logger) (*Client, error) {
	// Compile entity filter regex
	entityFilterRegex, err := regexp.Compile(cfg.ServiceBus.EntityFilter)
	if err != nil {
		return nil, fmt.Errorf("invalid entity filter regex: %w", err)
	}

	// Get namespace name
	var namespace string
	if len(cfg.ServiceBus.Namespaces) > 0 {
		namespace = cfg.ServiceBus.Namespaces[0]
	} else {
		return nil, fmt.Errorf("at least one namespace must be provided in configuration")
	}

	client := &Client{
		cfg:                 cfg,
		auth:                authProvider,
		log:                 log,
		entityFilter:        entityFilterRegex,
		namespace:           namespace,
		metrics:             make(map[string]*prometheus.GaugeVec),
		cache:               make(map[string]metrics.MetricValue),
		queueMetrics:        []QueueMetric{},
		topicMetrics:        []TopicMetric{},
		subscriptionMetrics: []SubscriptionMetric{},
		namespaceMetrics:    []NamespaceMetric{},
	}

	return client, nil
}

// CollectMetrics collects metrics from Service Bus
func (c *Client) CollectMetrics(ctx context.Context) error {
	// Check cache
	c.cacheMutex.RLock()
	if time.Since(c.lastUpdate) < c.cfg.Metrics.CacheDuration {
		c.cacheMutex.RUnlock()
		return nil
	}
	c.cacheMutex.RUnlock()

	// Lock cache for writing
	c.cacheMutex.Lock()
	defer c.cacheMutex.Unlock()

	// Initialize clients if needed
	if err := c.initializeClients(ctx); err != nil {
		return fmt.Errorf("failed to initialize clients: %w", err)
	}

	// Clear metric lists
	c.queueMetrics = []QueueMetric{}
	c.topicMetrics = []TopicMetric{}
	c.subscriptionMetrics = []SubscriptionMetric{}
	c.namespaceMetrics = []NamespaceMetric{}

	// Collect metrics
	if err := c.collectQueues(ctx); err != nil {
		c.log.WithError(err).Error("Failed to collect queue metrics")
	}

	if err := c.collectTopics(ctx); err != nil {
		c.log.WithError(err).Error("Failed to collect topic metrics")
	}

	if c.cfg.ServiceBus.IncludeNamespace {
		if err := c.collectNamespaceMetrics(ctx); err != nil {
			c.log.WithError(err).Error("Failed to collect namespace metrics")
		}
	}

	c.lastUpdate = time.Now()
	return nil
}

// Initialize the clients
func (c *Client) initializeClients(ctx context.Context) error {
	// Initialize Service Bus client
	if c.sbClient == nil {
		client, err := c.auth.GetServiceBusClient(ctx)
		if err != nil {
			return fmt.Errorf("failed to create Service Bus client: %w", err)
		}
		c.sbClient = client
	}

	// Initialize admin client if needed and possible
	if c.adminClient == nil {
		client, err := c.auth.GetServiceBusAdminClient(ctx)
		if err != nil {
			c.log.WithError(err).Warn("Failed to create Service Bus admin client, some metrics may be unavailable")
			// Continue without admin client
		} else {
			c.adminClient = client
		}
	}

	return nil
}

// GetQueueMetrics returns the collected queue metrics
func (c *Client) GetQueueMetrics() []QueueMetric {
	c.cacheMutex.RLock()
	defer c.cacheMutex.RUnlock()
	return c.queueMetrics
}

// GetTopicMetrics returns the collected topic metrics
func (c *Client) GetTopicMetrics() []TopicMetric {
	c.cacheMutex.RLock()
	defer c.cacheMutex.RUnlock()
	return c.topicMetrics
}

// GetSubscriptionMetrics returns the collected subscription metrics
func (c *Client) GetSubscriptionMetrics() []SubscriptionMetric {
	c.cacheMutex.RLock()
	defer c.cacheMutex.RUnlock()
	return c.subscriptionMetrics
}

// GetNamespaceMetrics returns the collected namespace metrics
func (c *Client) GetNamespaceMetrics() []NamespaceMetric {
	c.cacheMutex.RLock()
	defer c.cacheMutex.RUnlock()
	return c.namespaceMetrics
}

// collectQueues collects queue metrics
func (c *Client) collectQueues(ctx context.Context) error {
	c.log.Info("Collecting Service Bus queue metrics")

	// Get queue names
	var queueNames []string
	var err error

	if c.cfg.ServiceBus.UseRealEntities {
		// Get real queue names
		queueNames, err = c.getQueuesFromManagementAPI(ctx)
		if err != nil {
			c.log.WithError(err).Warn("Failed to get queue names using Management API, using test queues")
			queueNames = []string{"queue1", "queue2", "queue3"}
		} else {
			c.log.WithField("count", len(queueNames)).Info("Got queue names from Management API")
		}
	} else {
		// Use test data
		queueNames = []string{"queue1", "queue2", "queue3"}
		c.log.WithField("count", len(queueNames)).Info("Using test queue names")
	}

	// Process each queue
	for _, queueName := range queueNames {
		// Apply entity filter
		if c.entityFilter != nil && !c.entityFilter.MatchString(queueName) {
			continue
		}

		// Get queue metrics
		queueMetric, err := c.collectQueueMetrics(ctx, queueName)
		if err != nil {
			c.log.WithError(err).WithField("queue", queueName).Error("Failed to collect queue metrics")
			continue
		}

		// Add to metrics list
		c.queueMetrics = append(c.queueMetrics, *queueMetric)

		// Set Prometheus metrics
		labels := prometheus.Labels{
			"namespace":   c.namespace,
			"entity_name": queueName,
			"entity_type": "queue",
		}

		// Update metrics
		c.updatePrometheusMetrics(labels, map[string]float64{
			"active_messages":            queueMetric.ActiveMessages,
			"dead_letter_messages":       queueMetric.DeadLetterMessages,
			"scheduled_messages":         queueMetric.ScheduledMessages,
			"transfer_messages":          queueMetric.TransferMessages,
			"transfer_dlq_messages":      queueMetric.TransferDLQMessages,
			"size_bytes":                 queueMetric.SizeBytes,
			"max_size_bytes":             queueMetric.MaxSizeBytes,
			"incoming_messages":          queueMetric.IncomingMessages,
			"outgoing_messages":          queueMetric.OutgoingMessages,
			"message_size_bytes":         queueMetric.MessageSize,
			"oldest_message_age_seconds": queueMetric.OldestMessageAge,
		})
	}

	return nil
}

// Update multiple Prometheus metrics at once
func (c *Client) updatePrometheusMetrics(labels prometheus.Labels, values map[string]float64) {
	for name, value := range values {
		if gauge, ok := c.metrics[name]; ok {
			gauge.With(labels).Set(value)
		}
	}
}

// collectQueueMetrics collects metrics for a specific queue
func (c *Client) collectQueueMetrics(ctx context.Context, queueName string) (*QueueMetric, error) {
	c.log.WithField("queue", queueName).Debug("Collecting queue metrics")

	// Default/fallback values
	var activeMessages float64
	var dlqMessages float64
	var scheduledMessages float64
	var transferMessages float64
	var transferDLQMessages float64
	var sizeBytes float64
	var maxSizeBytes float64 = 1024 * 1024 * 1024 // 1GB default
	var incomingMessages float64
	var outgoingMessages float64
	var messageSize float64
	var oldestMessageAge float64

	if c.cfg.ServiceBus.UseRealEntities {
		// Try to get real metrics
		if c.adminClient != nil {
			// Get runtime properties using admin client
			runtimeProps, err := c.adminClient.GetQueueRuntimeProperties(ctx, queueName, nil)
			if err != nil {
				c.log.WithError(err).WithField("queue", queueName).Warn("Failed to get queue runtime properties")
			} else {

				if runtimeProps != nil {
					// Set metrics from runtime properties
					activeMessages = float64(runtimeProps.ActiveMessageCount)
					dlqMessages = float64(runtimeProps.DeadLetterMessageCount)
					// API'nin güncel versiyonunda bu alan mevcut değil, alternatif kullanabiliriz
					scheduledMessages = 0 // Eğer alan mevcut değilse 0 değeri atayabiliriz				transferMessages = float64(runtimeProps.TransferMessageCount)
					transferDLQMessages = float64(runtimeProps.TransferDeadLetterMessageCount)
					sizeBytes = float64(runtimeProps.SizeInBytes)

					// Calculate message age if possible
					oldestMessageAge = time.Since(runtimeProps.AccessedAt).Seconds()
				} else {
					c.log.Warn("runtimeProps is nil, skipping metric collection for this queue")
				}

			}

			// Get queue properties
			queueProps, err := c.adminClient.GetQueue(ctx, queueName, nil)
			if err != nil {
				//c.log.WithError(err).WithField("queue", queueName).Warn("Failed to get queue properties")
				c.log.WithField("queue", queueName).Warn("queueProps is nil, skipping max size calculation")

			} else {
				// Set max size
				if queueProps == nil {
					//c.log.WithError(err).WithField("queue", queueName).Warn("Failed to get queue properties")
					c.log.WithField("queue", queueName).Warn("queueProps is nil, skipping max size calculation")
				} else if queueProps.MaxSizeInMegabytes != nil {
					maxSizeBytes = float64(*queueProps.MaxSizeInMegabytes) * 1024 * 1024
				} else {
					c.log.WithField("queue", queueName).Warn("MaxSizeInMegabytes is nil")
				}
			}

		} else {
			// Fallback to Management API
			queueProps, err := c.getQueuePropertiesFromManagementAPI(ctx, queueName)
			if err != nil {
				c.log.WithError(err).WithField("queue", queueName).Warn("Failed to get queue properties from Management API")
			} else {
				//queueProps := queueResponse.Properties
				// Parse queue properties
				if queueProps.CountDetails != nil {
					if queueProps.CountDetails.ActiveMessageCount != nil {
						activeMessages = float64(*queueProps.CountDetails.ActiveMessageCount)
					}
					if queueProps.CountDetails.DeadLetterMessageCount != nil {
						dlqMessages = float64(*queueProps.CountDetails.DeadLetterMessageCount)
					}
					if queueProps.CountDetails.ScheduledMessageCount != nil {
						scheduledMessages = float64(*queueProps.CountDetails.ScheduledMessageCount)
					}
					if queueProps.CountDetails.TransferMessageCount != nil {
						transferMessages = float64(*queueProps.CountDetails.TransferMessageCount)
					}
					if queueProps.CountDetails.TransferDeadLetterMessageCount != nil {
						transferDLQMessages = float64(*queueProps.CountDetails.TransferDeadLetterMessageCount)
					}
				}

				if queueProps.SizeInBytes != nil {
					sizeBytes = float64(*queueProps.SizeInBytes)
				}

				if queueProps.MaxSizeInMegabytes != nil {
					maxSizeBytes = float64(*queueProps.MaxSizeInMegabytes) * 1024 * 1024
				}

				// Try to calculate message age
				if queueProps.CreatedAt != nil {
					oldestMessageAge = time.Since(*queueProps.CreatedAt).Seconds()
				}
			}
		}

		// Try to get performance metrics from Azure Monitor
		if c.cfg.Auth.Mode == "azure_auth" {
			perfMetrics, err := c.getQueuePerformanceMetrics(ctx, queueName)
			if err != nil {
				c.log.WithError(err).WithField("queue", queueName).Warn("Failed to get queue performance metrics")
			} else {
				incomingMessages = perfMetrics.IncomingMessages
				outgoingMessages = perfMetrics.OutgoingMessages
				messageSize = perfMetrics.MessageSize
			}
		}
	} else {
		// Use test data
		activeMessages = 10
		dlqMessages = 1
		scheduledMessages = 2
		transferMessages = 0
		transferDLQMessages = 0
		sizeBytes = 5000
		maxSizeBytes = 1024 * 1024 * 1024 // 1GB
		incomingMessages = 100
		outgoingMessages = 95
		messageSize = 1024
		oldestMessageAge = 300 // 5 minutes
	}

	// Create queue metric
	return &QueueMetric{
		Namespace:           c.namespace,
		Name:                queueName,
		ActiveMessages:      activeMessages,
		DeadLetterMessages:  dlqMessages,
		ScheduledMessages:   scheduledMessages,
		TransferMessages:    transferMessages,
		TransferDLQMessages: transferDLQMessages,
		SizeBytes:           sizeBytes,
		MaxSizeBytes:        maxSizeBytes,
		IncomingMessages:    incomingMessages,
		OutgoingMessages:    outgoingMessages,
		MessageSize:         messageSize,
		OldestMessageAge:    oldestMessageAge,
	}, nil
}

// getQueuePerformanceMetrics gets performance metrics from Azure Monitor
func (c *Client) getQueuePerformanceMetrics(ctx context.Context, queueName string) (*struct {
	IncomingMessages float64
	OutgoingMessages float64
	MessageSize      float64
}, error) {
	monitorClient, err := c.auth.GetMonitorClient(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to create monitor client: %w", err)
	}

	// Resource ID for the queue
	resourceID := fmt.Sprintf(
		"/subscriptions/%s/resourceGroups/%s/providers/Microsoft.ServiceBus/namespaces/%s/queues/%s",
		c.cfg.AzureMonitor.SubscriptionIDs[0],
		c.cfg.ServiceBus.ResourceGroup,
		c.namespace,
		queueName,
	)

	// Timespan (last 5 minutes)
	timespan := fmt.Sprintf("%s/%s",
		time.Now().Add(-5*time.Minute).Format(time.RFC3339),
		time.Now().Format(time.RFC3339),
	)

	// Metrics to collect
	metricNames := "IncomingMessages,OutgoingMessages"

	// Get metrics
	response, err := monitorClient.List(
		ctx,
		resourceID,
		&armmonitor.MetricsClientListOptions{
			Timespan:    &timespan,
			Interval:    to.Ptr("PT1M"), // 1-minute intervals
			Metricnames: &metricNames,
			Aggregation: to.Ptr("Total"),
		},
	)

	if err != nil {
		return nil, fmt.Errorf("failed to get metrics from Azure Monitor: %w", err)
	}

	// Results
	result := &struct {
		IncomingMessages float64
		OutgoingMessages float64
		MessageSize      float64
	}{
		IncomingMessages: 0,
		OutgoingMessages: 0,
		MessageSize:      0,
	}

	// Process response
	if response.Value != nil {
		for _, metric := range response.Value {
			if metric.Name == nil || metric.Name.Value == nil {
				continue
			}

			metricName := *metric.Name.Value

			if len(metric.Timeseries) == 0 || len(metric.Timeseries[0].Data) == 0 {
				continue
			}

			// Get latest data point
			dataPoint := metric.Timeseries[0].Data[len(metric.Timeseries[0].Data)-1]

			// Set value based on metric name
			switch metricName {
			case "IncomingMessages":
				if dataPoint.Total != nil {
					result.IncomingMessages = *dataPoint.Total
				}
			case "OutgoingMessages":
				if dataPoint.Total != nil {
					result.OutgoingMessages = *dataPoint.Total
				}
			}
		}
	}

	// Try to get message size (separate call)
	metricNames = "MessageSize"

	response, err = monitorClient.List(
		ctx,
		resourceID,
		&armmonitor.MetricsClientListOptions{
			Timespan:    &timespan,
			Interval:    to.Ptr("PT1M"),
			Metricnames: &metricNames,
			Aggregation: to.Ptr("Average"),
		},
	)

	if err == nil && response.Value != nil {
		for _, metric := range response.Value {
			if metric.Name == nil || metric.Name.Value == nil {
				continue
			}

			if *metric.Name.Value == "MessageSize" && len(metric.Timeseries) > 0 && len(metric.Timeseries[0].Data) > 0 {
				dataPoint := metric.Timeseries[0].Data[len(metric.Timeseries[0].Data)-1]
				if dataPoint.Average != nil {
					result.MessageSize = *dataPoint.Average
				}
			}
		}
	}

	return result, nil
}

// collectTopics collects topic metrics
func (c *Client) collectTopics(ctx context.Context) error {
	c.log.Info("Collecting Service Bus topic metrics")

	// Get topic names
	var topicNames []string
	var err error

	if c.cfg.ServiceBus.UseRealEntities {
		// Get real topic names
		topicNames, err = c.getTopicsFromManagementAPI(ctx)
		if err != nil {
			c.log.WithError(err).Warn("Failed to get topic names using Management API, using test topics")
			topicNames = []string{"topic1", "topic2"}
		} else {
			c.log.WithField("count", len(topicNames)).Info("Got topic names from Management API")
		}
	} else {
		// Use test data
		topicNames = []string{"topic1", "topic2"}
		c.log.WithField("count", len(topicNames)).Info("Using test topic names")
	}

	// Process each topic
	for _, topicName := range topicNames {
		// Apply entity filter
		if c.entityFilter != nil && !c.entityFilter.MatchString(topicName) {
			continue
		}

		// Get topic metrics
		topicMetric, err := c.collectTopicMetrics(ctx, topicName)
		if err != nil {
			c.log.WithError(err).WithField("topic", topicName).Error("Failed to collect topic metrics")
			continue
		}

		// Add to metrics list
		c.topicMetrics = append(c.topicMetrics, *topicMetric)

		// Collect subscriptions for this topic
		if err := c.collectSubscriptions(ctx, topicName); err != nil {
			c.log.WithError(err).WithField("topic", topicName).Error("Failed to collect topic subscriptions")
		}

		// Set Prometheus metrics
		labels := prometheus.Labels{
			"namespace":   c.namespace,
			"entity_name": topicName,
			"entity_type": "topic",
		}

		// Update metrics
		c.updatePrometheusMetrics(labels, map[string]float64{
			"active_messages":    topicMetric.ActiveMessages,
			"size_bytes":         topicMetric.SizeBytes,
			"max_size_bytes":     topicMetric.MaxSizeBytes,
			"incoming_messages":  topicMetric.IncomingMessages,
			"outgoing_messages":  topicMetric.OutgoingMessages,
			"subscription_count": topicMetric.SubscriptionCount,
		})
	}

	return nil
}

// collectTopicMetrics collects metrics for a specific topic
func (c *Client) collectTopicMetrics(ctx context.Context, topicName string) (*TopicMetric, error) {
	c.log.WithField("topic", topicName).Debug("Collecting topic metrics")

	// Default/fallback values
	var activeMessages float64
	var sizeBytes float64
	var maxSizeBytes float64 = 1024 * 1024 * 1024 // 1GB default
	var incomingMessages float64
	var outgoingMessages float64
	var subscriptionCount float64

	if c.cfg.ServiceBus.UseRealEntities {
		// Try to get real metrics
		if c.adminClient != nil {
			// Get runtime properties using admin client
			runtimeProps, err := c.adminClient.GetTopicRuntimeProperties(ctx, topicName, nil)
			if err != nil {
				c.log.WithError(err).WithField("topic", topicName).Warn("Failed to get topic runtime properties")
			} else if runtimeProps != nil {
				// Set metrics from runtime properties
				sizeBytes = float64(runtimeProps.SizeInBytes)
				subscriptionCount = float64(runtimeProps.SubscriptionCount)
			} else {
				c.log.WithField("topic", topicName).Warn("runtimeProps is nil, skipping metric collection for this topic")
			}
			// Get topic properties
			topicProps, err := c.adminClient.GetTopic(ctx, topicName, nil)
			if err != nil {
				c.log.WithError(err).WithField("topic", topicName).Warn("Failed to get topic properties")
			} else if topicProps != nil {
				// Set max size
				if topicProps.MaxSizeInMegabytes != nil {
					maxSizeBytes = float64(*topicProps.MaxSizeInMegabytes) * 1024 * 1024
				}
			} else {
				c.log.WithField("topic", topicName).Warn("topicProps is nil, skipping metric collection for this topic")
			}
		} else {
			// Fallback to Management API
			topicProps, err := c.getTopicPropertiesFromManagementAPI(ctx, topicName)
			if err != nil {
				c.log.WithError(err).WithField("topic", topicName).Warn("Failed to get topic properties from Management API")
			} else {
				// Parse topic properties
				if topicProps.SizeInBytes != nil {
					sizeBytes = float64(*topicProps.SizeInBytes)
				}

				if topicProps.MaxSizeInMegabytes != nil {
					maxSizeBytes = float64(*topicProps.MaxSizeInMegabytes) * 1024 * 1024
				}

				if topicProps.SubscriptionCount != nil {
					subscriptionCount = float64(*topicProps.SubscriptionCount)
				}
			}

			// If subscription count is still 0, try to count subscriptions directly
			if subscriptionCount == 0 {
				subscriptions, err := c.getSubscriptionsFromManagementAPI(ctx, topicName)
				if err == nil {
					subscriptionCount = float64(len(subscriptions))
				}
			}
		}

		// Try to get performance metrics from Azure Monitor
		if c.cfg.Auth.Mode == "azure_auth" {
			perfMetrics, err := c.getTopicPerformanceMetrics(ctx, topicName)
			if err != nil {
				c.log.WithError(err).WithField("topic", topicName).Warn("Failed to get topic performance metrics")
			} else {
				activeMessages = perfMetrics.ActiveMessages
				incomingMessages = perfMetrics.IncomingMessages
				outgoingMessages = perfMetrics.OutgoingMessages
			}
		}
	} else {
		// Use test data
		activeMessages = 5
		sizeBytes = 3000
		maxSizeBytes = 1024 * 1024 * 1024 // 1GB
		incomingMessages = 80
		outgoingMessages = 75
		subscriptionCount = 2
	}

	// Create topic metric
	return &TopicMetric{
		Namespace:         c.namespace,
		Name:              topicName,
		ActiveMessages:    activeMessages,
		SizeBytes:         sizeBytes,
		MaxSizeBytes:      maxSizeBytes,
		IncomingMessages:  incomingMessages,
		OutgoingMessages:  outgoingMessages,
		SubscriptionCount: subscriptionCount,
	}, nil
}

// getTopicPerformanceMetrics gets performance metrics from Azure Monitor
func (c *Client) getTopicPerformanceMetrics(ctx context.Context, topicName string) (*struct {
	ActiveMessages   float64
	IncomingMessages float64
	OutgoingMessages float64
}, error) {
	monitorClient, err := c.auth.GetMonitorClient(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to create monitor client: %w", err)
	}

	// Resource ID for the topic
	resourceID := fmt.Sprintf(
		"/subscriptions/%s/resourceGroups/%s/providers/Microsoft.ServiceBus/namespaces/%s/topics/%s",
		c.cfg.AzureMonitor.SubscriptionIDs[0],
		c.cfg.ServiceBus.ResourceGroup,
		c.namespace,
		topicName,
	)

	// Timespan (last 5 minutes)
	timespan := fmt.Sprintf("%s/%s",
		time.Now().Add(-5*time.Minute).Format(time.RFC3339),
		time.Now().Format(time.RFC3339),
	)

	// Metrics to collect
	metricNames := "IncomingMessages,OutgoingMessages,ActiveMessages"

	// Get metrics
	response, err := monitorClient.List(
		ctx,
		resourceID,
		&armmonitor.MetricsClientListOptions{
			Timespan:    &timespan,
			Interval:    to.Ptr("PT1M"), // 1-minute intervals
			Metricnames: &metricNames,
			Aggregation: to.Ptr("Total,Average"),
		},
	)

	if err != nil {
		return nil, fmt.Errorf("failed to get metrics from Azure Monitor: %w", err)
	}

	// Results
	result := &struct {
		ActiveMessages   float64
		IncomingMessages float64
		OutgoingMessages float64
	}{
		ActiveMessages:   0,
		IncomingMessages: 0,
		OutgoingMessages: 0,
	}

	// Process response
	if response.Value != nil {
		for _, metric := range response.Value {
			if metric.Name == nil || metric.Name.Value == nil {
				continue
			}

			metricName := *metric.Name.Value

			if len(metric.Timeseries) == 0 || len(metric.Timeseries[0].Data) == 0 {
				continue
			}

			// Get latest data point
			dataPoint := metric.Timeseries[0].Data[len(metric.Timeseries[0].Data)-1]

			// Set value based on metric name
			switch metricName {
			case "ActiveMessages":
				if dataPoint.Average != nil {
					result.ActiveMessages = *dataPoint.Average
				}
			case "IncomingMessages":
				if dataPoint.Total != nil {
					result.IncomingMessages = *dataPoint.Total
				}
			case "OutgoingMessages":
				if dataPoint.Total != nil {
					result.OutgoingMessages = *dataPoint.Total
				}
			}
		}
	}

	return result, nil
}

// collectSubscriptions collects metrics for all subscriptions of a topic
func (c *Client) collectSubscriptions(ctx context.Context, topicName string) error {
	c.log.WithField("topic", topicName).Debug("Collecting topic subscriptions")

	// Get subscription names
	var subscriptionNames []string
	var err error

	if c.cfg.ServiceBus.UseRealEntities {
		// Get real subscription names
		subscriptionNames, err = c.getSubscriptionsFromManagementAPI(ctx, topicName)
		if err != nil {
			c.log.WithError(err).WithField("topic", topicName).Warn("Failed to get subscription names, using test subscriptions")
			subscriptionNames = []string{"sub1", "sub2"}
		} else {
			c.log.WithFields(logrus.Fields{
				"topic": topicName,
				"count": len(subscriptionNames),
			}).Info("Got subscription names from Management API")
		}
	} else {
		// Use test data
		subscriptionNames = []string{"sub1", "sub2"}
		c.log.WithFields(logrus.Fields{
			"topic": topicName,
			"count": len(subscriptionNames),
		}).Info("Using test subscription names")
	}

	// Process each subscription
	for _, subName := range subscriptionNames {
		// Apply entity filter
		if c.entityFilter != nil && !c.entityFilter.MatchString(fmt.Sprintf("%s/%s", topicName, subName)) {
			continue
		}

		// Get subscription metrics
		subMetric, err := c.collectSubscriptionMetrics(ctx, topicName, subName)
		if err != nil {
			c.log.WithError(err).WithFields(logrus.Fields{
				"topic":        topicName,
				"subscription": subName,
			}).Error("Failed to collect subscription metrics")
			continue
		}

		// Add to metrics list
		c.subscriptionMetrics = append(c.subscriptionMetrics, *subMetric)

		// Set Prometheus metrics
		labels := prometheus.Labels{
			"namespace":   c.namespace,
			"entity_name": fmt.Sprintf("%s/%s", topicName, subName),
			"entity_type": "subscription",
		}

		// Update metrics
		c.updatePrometheusMetrics(labels, map[string]float64{
			"active_messages":            subMetric.ActiveMessages,
			"dead_letter_messages":       subMetric.DeadLetterMessages,
			"scheduled_messages":         subMetric.ScheduledMessages,
			"transfer_messages":          subMetric.TransferMessages,
			"transfer_dlq_messages":      subMetric.TransferDLQMessages,
			"incoming_messages":          subMetric.IncomingMessages,
			"outgoing_messages":          subMetric.OutgoingMessages,
			"oldest_message_age_seconds": subMetric.OldestMessageAge,
		})
	}

	return nil
}

// collectSubscriptionMetrics collects metrics for a specific subscription
func (c *Client) collectSubscriptionMetrics(ctx context.Context, topicName, subName string) (*SubscriptionMetric, error) {
	c.log.WithFields(logrus.Fields{
		"topic":        topicName,
		"subscription": subName,
	}).Debug("Collecting subscription metrics")

	// Default/fallback values
	var activeMessages float64
	var dlqMessages float64
	var scheduledMessages float64
	var transferMessages float64
	var transferDLQMessages float64
	var incomingMessages float64
	var outgoingMessages float64
	var oldestMessageAge float64

	if c.cfg.ServiceBus.UseRealEntities {
		// Try to get real metrics
		if c.adminClient != nil {
			// Get runtime properties using admin client
			runtimeProps, err := c.adminClient.GetSubscriptionRuntimeProperties(ctx, topicName, subName, nil)
			if err != nil {
				c.log.WithError(err).WithFields(logrus.Fields{
					"topic":        topicName,
					"subscription": subName,
				}).Warn("Failed to get subscription runtime properties")
			} else if runtimeProps != nil {
				// Set metrics from runtime properties
				activeMessages = float64(runtimeProps.ActiveMessageCount)
				dlqMessages = float64(runtimeProps.DeadLetterMessageCount)
				scheduledMessages = 0 // API'nin güncel versiyonunda bu alan mevcut değil, alternatif kullanabiliriz
				transferMessages = float64(runtimeProps.TransferMessageCount)
				transferDLQMessages = float64(runtimeProps.TransferDeadLetterMessageCount)

				// Calculate message age if possible
				oldestMessageAge = time.Since(runtimeProps.AccessedAt).Seconds()
			} else {
				c.log.WithField("topic", topicName).WithField("subscription", subName).Warn("runtimeProps is nil, skipping metric collection for this subscription")
			}
		} else {
			// Fallback to Management API
			subProps, err := c.getSubscriptionPropertiesFromManagementAPI(ctx, topicName, subName)
			if err != nil {
				c.log.WithError(err).WithFields(logrus.Fields{
					"topic":        topicName,
					"subscription": subName,
				}).Warn("Failed to get subscription properties from Management API")
			} else {
				// Parse subscription properties
				if subProps.CountDetails != nil {
					if subProps.CountDetails.ActiveMessageCount != nil {
						activeMessages = float64(*subProps.CountDetails.ActiveMessageCount)
					}
					if subProps.CountDetails.DeadLetterMessageCount != nil {
						dlqMessages = float64(*subProps.CountDetails.DeadLetterMessageCount)
					}
					if subProps.CountDetails.ScheduledMessageCount != nil {
						scheduledMessages = float64(*subProps.CountDetails.ScheduledMessageCount)
					}
					if subProps.CountDetails.TransferMessageCount != nil {
						transferMessages = float64(*subProps.CountDetails.TransferMessageCount)
					}
					if subProps.CountDetails.TransferDeadLetterMessageCount != nil {
						transferDLQMessages = float64(*subProps.CountDetails.TransferDeadLetterMessageCount)
					}
				}

				// Try to calculate message age
				if subProps.CreatedAt != nil {
					oldestMessageAge = time.Since(*subProps.CreatedAt).Seconds()
				}
			}
		}

		// Try to get performance metrics from Azure Monitor
		if c.cfg.Auth.Mode == "azure_auth" {
			perfMetrics, err := c.getSubscriptionPerformanceMetrics(ctx, topicName, subName)
			if err != nil {
				c.log.WithError(err).WithFields(logrus.Fields{
					"topic":        topicName,
					"subscription": subName,
				}).Warn("Failed to get subscription performance metrics")
			} else {
				incomingMessages = perfMetrics.IncomingMessages
				outgoingMessages = perfMetrics.OutgoingMessages
			}
		}
	} else {
		// Use test data
		activeMessages = 3
		dlqMessages = 0
		scheduledMessages = 1
		transferMessages = 0
		transferDLQMessages = 0
		incomingMessages = 50
		outgoingMessages = 47
		oldestMessageAge = 120 // 2 minutes
	}

	// Create subscription metric
	return &SubscriptionMetric{
		Namespace:           c.namespace,
		TopicName:           topicName,
		Name:                subName,
		ActiveMessages:      activeMessages,
		DeadLetterMessages:  dlqMessages,
		ScheduledMessages:   scheduledMessages,
		TransferMessages:    transferMessages,
		TransferDLQMessages: transferDLQMessages,
		IncomingMessages:    incomingMessages,
		OutgoingMessages:    outgoingMessages,
		OldestMessageAge:    oldestMessageAge,
	}, nil
}

// getSubscriptionPerformanceMetrics gets performance metrics from Azure Monitor
func (c *Client) getSubscriptionPerformanceMetrics(ctx context.Context, topicName, subName string) (*struct {
	IncomingMessages float64
	OutgoingMessages float64
}, error) {
	monitorClient, err := c.auth.GetMonitorClient(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to create monitor client: %w", err)
	}

	// Resource ID for the subscription
	resourceID := fmt.Sprintf(
		"/subscriptions/%s/resourceGroups/%s/providers/Microsoft.ServiceBus/namespaces/%s/topics/%s/subscriptions/%s",
		c.cfg.AzureMonitor.SubscriptionIDs[0],
		c.cfg.ServiceBus.ResourceGroup,
		c.namespace,
		topicName,
		subName,
	)

	// Timespan (last 5 minutes)
	timespan := fmt.Sprintf("%s/%s",
		time.Now().Add(-5*time.Minute).Format(time.RFC3339),
		time.Now().Format(time.RFC3339),
	)

	// Metrics to collect
	metricNames := "IncomingMessages,OutgoingMessages"

	// Get metrics
	response, err := monitorClient.List(
		ctx,
		resourceID,
		&armmonitor.MetricsClientListOptions{
			Timespan:    &timespan,
			Interval:    to.Ptr("PT1M"), // 1-minute intervals
			Metricnames: &metricNames,
			Aggregation: to.Ptr("Total"),
		},
	)

	if err != nil {
		return nil, fmt.Errorf("failed to get metrics from Azure Monitor: %w", err)
	}

	// Results
	result := &struct {
		IncomingMessages float64
		OutgoingMessages float64
	}{
		IncomingMessages: 0,
		OutgoingMessages: 0,
	}

	// Process response
	if response.Value != nil {
		for _, metric := range response.Value {
			if metric.Name == nil || metric.Name.Value == nil {
				continue
			}

			metricName := *metric.Name.Value

			if len(metric.Timeseries) == 0 || len(metric.Timeseries[0].Data) == 0 {
				continue
			}

			// Get latest data point
			dataPoint := metric.Timeseries[0].Data[len(metric.Timeseries[0].Data)-1]

			// Set value based on metric name
			switch metricName {
			case "IncomingMessages":
				if dataPoint.Total != nil {
					result.IncomingMessages = *dataPoint.Total
				}
			case "OutgoingMessages":
				if dataPoint.Total != nil {
					result.OutgoingMessages = *dataPoint.Total
				}
			}
		}
	}

	return result, nil
}

// collectNamespaceMetrics collects metrics for the namespace
func (c *Client) collectNamespaceMetrics(ctx context.Context) error {
	c.log.Info("Collecting Service Bus namespace metrics")

	var activeConnections float64
	var cpuUsage float64
	var memoryUsage float64
	var quotaUsage = make(map[string]float64)

	if c.cfg.ServiceBus.UseRealEntities && c.cfg.Auth.Mode == "azure_auth" {
		// Get namespace metrics from Azure Monitor
		monitorClient, err := c.auth.GetMonitorClient(ctx)
		if err != nil {
			c.log.WithError(err).Warn("Failed to create monitor client for namespace metrics")
		} else {
			// Resource ID for the namespace
			resourceID := fmt.Sprintf(
				"/subscriptions/%s/resourceGroups/%s/providers/Microsoft.ServiceBus/namespaces/%s",
				c.cfg.AzureMonitor.SubscriptionIDs[0],
				c.cfg.ServiceBus.ResourceGroup,
				c.namespace,
			)

			// Timespan (last 5 minutes)
			timespan := fmt.Sprintf("%s/%s",
				time.Now().Add(-5*time.Minute).Format(time.RFC3339),
				time.Now().Format(time.RFC3339),
			)

			// Basic namespace metrics
			metricNames := "ActiveConnections,CPU,Memory"

			response, err := monitorClient.List(
				ctx,
				resourceID,
				&armmonitor.MetricsClientListOptions{
					Timespan:    &timespan,
					Interval:    to.Ptr("PT1M"),
					Metricnames: &metricNames,
					Aggregation: to.Ptr("Average"),
				},
			)

			if err != nil {
				c.log.WithError(err).Warn("Failed to get namespace metrics from Azure Monitor")
			} else if response.Value != nil {
				// Process response
				for _, metric := range response.Value {
					if metric.Name == nil || metric.Name.Value == nil {
						continue
					}

					metricName := *metric.Name.Value

					if len(metric.Timeseries) == 0 || len(metric.Timeseries[0].Data) == 0 {
						continue
					}

					// Get latest data point
					dataPoint := metric.Timeseries[0].Data[len(metric.Timeseries[0].Data)-1]

					// Set value based on metric name
					if dataPoint.Average != nil {
						switch metricName {
						case "ActiveConnections":
							activeConnections = *dataPoint.Average
						case "CPU":
							cpuUsage = *dataPoint.Average
						case "Memory":
							memoryUsage = *dataPoint.Average
						}
					}
				}
			}

			// Quota metrics
			metricNames = "NamespaceQuotaUtilization,MessagesQuotaUtilization,TopicsQuotaUtilization,QueuesQuotaUtilization,SubscriptionsQuotaUtilization"

			response, err = monitorClient.List(
				ctx,
				resourceID,
				&armmonitor.MetricsClientListOptions{
					Timespan:    &timespan,
					Interval:    to.Ptr("PT1M"),
					Metricnames: &metricNames,
					Aggregation: to.Ptr("Average"),
				},
			)

			if err != nil {
				c.log.WithError(err).Warn("Failed to get quota metrics from Azure Monitor")
			} else if response.Value != nil {
				// Process quota metrics
				for _, metric := range response.Value {
					if metric.Name == nil || metric.Name.Value == nil {
						continue
					}

					metricName := *metric.Name.Value

					if len(metric.Timeseries) == 0 || len(metric.Timeseries[0].Data) == 0 {
						continue
					}

					// Get latest data point
					dataPoint := metric.Timeseries[0].Data[len(metric.Timeseries[0].Data)-1]

					// Set quota value
					if dataPoint.Average != nil {
						// Clean up metric name
						quotaName := strings.TrimSuffix(strings.ToLower(metricName), "quotautilization")
						quotaUsage[quotaName] = *dataPoint.Average
					}
				}
			}
		}
	} else {
		// Use test data
		activeConnections = 10
		cpuUsage = 15                    // 15% CPU usage
		memoryUsage = 30                 // 30% Memory usage
		quotaUsage["namespace"] = 20     // 20% Namespace quota used
		quotaUsage["messages"] = 40      // 40% Message quota used
		quotaUsage["topics"] = 30        // 30% Topics quota used
		quotaUsage["queues"] = 25        // 25% Queues quota used
		quotaUsage["subscriptions"] = 15 // 15% Subscriptions quota used
	}

	// Create namespace metric
	namespaceMetric := NamespaceMetric{
		Namespace:         c.namespace,
		ActiveConnections: activeConnections,
		CPUUsage:          cpuUsage,
		MemoryUsage:       memoryUsage,
		QuotaUsage:        quotaUsage,
	}

	// Add to metrics list
	c.namespaceMetrics = append(c.namespaceMetrics, namespaceMetric)

	// Set Prometheus metrics
	labels := prometheus.Labels{
		"namespace": c.namespace,
	}

	// Update basic metrics
	c.updatePrometheusMetrics(labels, map[string]float64{
		"active_connections": activeConnections,
		"cpu_usage":          cpuUsage,
		"memory_usage":       memoryUsage,
	})

	// Update quota metrics
	for quotaName, value := range quotaUsage {
		quotaLabels := prometheus.Labels{
			"namespace":  c.namespace,
			"quota_name": quotaName,
		}

		if gauge, ok := c.metrics["quota_usage_percentage"]; ok {
			gauge.With(quotaLabels).Set(value)
		}
	}

	return nil
}

// getQueuesFromManagementAPI gets queue names using the Management API
func (c *Client) getQueuesFromManagementAPI(ctx context.Context) ([]string, error) {
	queuesClient, err := c.auth.GetQueuesClient(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get queues client: %w", err)
	}

	resourceGroupName := c.cfg.ServiceBus.ResourceGroup
	if resourceGroupName == "" {
		return nil, fmt.Errorf("resource group name not specified in config")
	}

	var queueNames []string
	pager := queuesClient.NewListByNamespacePager(resourceGroupName, c.namespace, nil)
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			return nil, fmt.Errorf("failed to get queues page: %w", err)
		}

		for _, queue := range page.Value {
			if queue.Name != nil {
				queueNames = append(queueNames, *queue.Name)
			}
		}
	}

	return queueNames, nil
}

// getQueuePropertiesFromManagementAPI gets a specific queue using the Management API
func (c *Client) getQueuePropertiesFromManagementAPI(ctx context.Context, queueName string) (*armservicebus.SBQueueProperties, error) {
	queuesClient, err := c.auth.GetQueuesClient(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get queues client: %w", err)
	}

	resourceGroupName := c.cfg.ServiceBus.ResourceGroup
	if resourceGroupName == "" {
		return nil, fmt.Errorf("resource group name not specified in config")
	}

	response, err := queuesClient.Get(ctx, resourceGroupName, c.namespace, queueName, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to get queue: %w", err)
	}

	return response.Properties, nil
}

// getTopicsFromManagementAPI gets topic names using the Management API
func (c *Client) getTopicsFromManagementAPI(ctx context.Context) ([]string, error) {
	topicsClient, err := c.auth.GetTopicsClient(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get topics client: %w", err)
	}

	resourceGroupName := c.cfg.ServiceBus.ResourceGroup
	if resourceGroupName == "" {
		return nil, fmt.Errorf("resource group name not specified in config")
	}

	var topicNames []string
	pager := topicsClient.NewListByNamespacePager(resourceGroupName, c.namespace, nil)
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			return nil, fmt.Errorf("failed to get topics page: %w", err)
		}

		for _, topic := range page.Value {
			if topic.Name != nil {
				topicNames = append(topicNames, *topic.Name)
			}
		}
	}

	return topicNames, nil
}

// getTopicPropertiesFromManagementAPI gets a specific topic using the Management API
func (c *Client) getTopicPropertiesFromManagementAPI(ctx context.Context, topicName string) (*armservicebus.SBTopicProperties, error) {
	topicsClient, err := c.auth.GetTopicsClient(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get topics client: %w", err)
	}

	resourceGroupName := c.cfg.ServiceBus.ResourceGroup
	if resourceGroupName == "" {
		return nil, fmt.Errorf("resource group name not specified in config")
	}

	topic, err := topicsClient.Get(ctx, resourceGroupName, c.namespace, topicName, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to get topic: %w", err)
	}

	return topic.Properties, nil
}

// getSubscriptionsFromManagementAPI gets subscription names for a topic using the Management API
func (c *Client) getSubscriptionsFromManagementAPI(ctx context.Context, topicName string) ([]string, error) {
	subscriptionsClient, err := c.auth.GetSubscriptionsClient(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get subscriptions client: %w", err)
	}

	resourceGroupName := c.cfg.ServiceBus.ResourceGroup
	if resourceGroupName == "" {
		return nil, fmt.Errorf("resource group name not specified in config")
	}

	var subscriptionNames []string
	pager := subscriptionsClient.NewListByTopicPager(resourceGroupName, c.namespace, topicName, nil)
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			return nil, fmt.Errorf("failed to get subscriptions page: %w", err)
		}

		for _, subscription := range page.Value {
			if subscription.Name != nil {
				subscriptionNames = append(subscriptionNames, *subscription.Name)
			}
		}
	}

	return subscriptionNames, nil
}

// getSubscriptionPropertiesFromManagementAPI gets a specific subscription using the Management API
func (c *Client) getSubscriptionPropertiesFromManagementAPI(ctx context.Context, topicName, subName string) (*armservicebus.SBSubscriptionProperties, error) {
	subscriptionsClient, err := c.auth.GetSubscriptionsClient(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get subscriptions client: %w", err)
	}

	resourceGroupName := c.cfg.ServiceBus.ResourceGroup
	if resourceGroupName == "" {
		return nil, fmt.Errorf("resource group name not specified in config")
	}

	subscription, err := subscriptionsClient.Get(ctx, resourceGroupName, c.namespace, topicName, subName, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to get subscription: %w", err)
	}

	return subscription.Properties, nil
}
