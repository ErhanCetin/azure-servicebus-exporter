package azuremonitor

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
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
	Timestamp  time.Time
}

// EntityMetric represents metrics for a Service Bus entity (queue, topic, subscription)
type EntityMetric struct {
	Namespace  string
	EntityName string
	EntityType string
	MetricName string
	Operation  string
	Labels     map[string]string
	Value      float64
	Timestamp  time.Time
}

// Client represents an Azure Monitor client that can collect metrics
type Client struct {
	cfg       *config.Config
	auth      auth.AuthProvider
	log       *logrus.Logger
	namespace string

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
	// Get namespace from config
	var namespace string
	if len(cfg.ServiceBus.Namespaces) > 0 {
		namespace = cfg.ServiceBus.Namespaces[0]
	} else {
		return nil, fmt.Errorf("at least one namespace must be provided in configuration")
	}

	client := &Client{
		cfg:              cfg,
		auth:             authProvider,
		log:              log,
		namespace:        namespace, // Bu satır eklenmeli
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

	// Clear metric lists
	c.namespaceMetrics = []NamespaceMetric{}
	c.entityMetrics = []EntityMetric{}

	// Create Azure Monitor client if not already created
	if c.metricsClient == nil {
		client, err := c.auth.GetMonitorClient(ctx)
		if err != nil {
			return fmt.Errorf("failed to create metrics client: %w", err)
		}
		c.metricsClient = client
	}

	// Collect metrics for each subscription
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
	c.log.WithField("subscription", subscriptionID).Info("Collecting Service Bus metrics")

	// Collect metrics for all configured namespaces
	for _, namespace := range c.cfg.ServiceBus.Namespaces {
		resourceID := fmt.Sprintf("/subscriptions/%s/resourceGroups/%s/providers/Microsoft.ServiceBus/namespaces/%s",
			subscriptionID, c.cfg.ServiceBus.ResourceGroup, namespace)

		// Collect namespace-level metrics
		if err := c.collectNamespaceMetrics(ctx, resourceID, namespace); err != nil {
			c.log.WithError(err).WithField("namespace", namespace).Error("Failed to collect namespace metrics")
		}

		// Collect entity-level metrics (queues, topics, subscriptions)
		for _, entityType := range c.cfg.ServiceBus.EntityTypes {
			if err := c.collectEntityMetrics(ctx, resourceID, namespace, entityType); err != nil {
				c.log.WithError(err).WithFields(logrus.Fields{
					"namespace":   namespace,
					"entity_type": entityType,
				}).Error("Failed to collect entity metrics")
			}
		}

		// Collect detailed performance metrics
		if err := c.collectDetailedMetrics(ctx, resourceID, namespace); err != nil {
			c.log.WithError(err).WithField("namespace", namespace).Error("Failed to collect detailed metrics")
		}
	}

	return nil
}

// collectNamespaceMetrics collects metrics for a specific namespace
func (c *Client) collectNamespaceMetrics(ctx context.Context, resourceID, namespace string) error {
	// Define standard namespace metrics
	metricNames := []string{
		"SuccessfulRequests",
		"ServerErrors",
		"UserErrors",
		"ThrottledRequests",
		"ActiveConnections",
	}

	// Define premium tier metrics
	premiumMetrics := []string{
		"CPU",
		"Memory",
		"ConnectionsClosed",
		"ConnectionsOpened",
	}

	// Include premium metrics if relevant
	allMetrics := append(metricNames, premiumMetrics...)

	// Set the timespan for the query (last scrape interval)
	timespan := fmt.Sprintf("%s/%s",
		time.Now().Add(-c.cfg.Metrics.ScrapeInterval).Format(time.RFC3339),
		time.Now().Format(time.RFC3339),
	)

	// Get metrics from Azure Monitor API
	c.log.WithFields(logrus.Fields{
		"resourceID": resourceID,
		"timespan":   timespan,
		"metrics":    allMetrics,
	}).Info("Fetching namespace metrics from Azure Monitor API")

	// Build metricnames string with commas
	metricNamesStr := ""
	for i, name := range allMetrics {
		if i > 0 {
			metricNamesStr += ","
		}
		metricNamesStr += name
	}

	// Call Azure Monitor API
	response, err := c.metricsClient.List(
		ctx,
		resourceID,
		&armmonitor.MetricsClientListOptions{
			Timespan:    &timespan,
			Interval:    to.Ptr("PT5M"), // 5-minute intervals
			Metricnames: &metricNamesStr,
			Aggregation: to.Ptr("Total,Average,Maximum"),
		},
	)

	if err != nil {
		return fmt.Errorf("failed to get metrics: %w", err)
	}

	// Process metrics from response
	if response.Value != nil {
		for _, metric := range response.Value {
			if metric.Name == nil || metric.Name.Value == nil {
				continue
			}

			metricName := *metric.Name.Value

			if metric.Timeseries == nil || len(metric.Timeseries) == 0 {
				continue
			}

			// Get the latest data point for each metric
			timeSeries := metric.Timeseries[0]
			if timeSeries.Data == nil || len(timeSeries.Data) == 0 {
				continue
			}

			// Use the latest data point
			dataPoint := timeSeries.Data[len(timeSeries.Data)-1]

			// Choose the appropriate value based on the metric type
			var value float64
			var timestamp time.Time

			if dataPoint.TimeStamp != nil {
				timestamp = *dataPoint.TimeStamp
			} else {
				timestamp = time.Now()
			}

			if dataPoint.Total != nil {
				value = *dataPoint.Total
			} else if dataPoint.Average != nil {
				value = *dataPoint.Average
			} else if dataPoint.Maximum != nil {
				value = *dataPoint.Maximum
			} else {
				// Skip if no value available
				continue
			}

			// Create namespace metric
			namespaceMetric := NamespaceMetric{
				Namespace:  namespace,
				MetricName: metricName,
				Value:      value,
				Timestamp:  timestamp,
			}

			// Add to the list
			c.namespaceMetrics = append(c.namespaceMetrics, namespaceMetric)

			// Update Prometheus metrics
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
	}

	return nil
}

// collectEntityMetrics collects metrics for entities within a namespace
func (c *Client) collectEntityMetrics(ctx context.Context, resourceID, namespace, entityType string) error {
	// Define standard entity metrics
	metricNames := []string{
		"IncomingMessages",
		"OutgoingMessages",
		"ActiveMessages",
		"DeadletteredMessages",
		"ScheduledMessages",
		"Size",
	}

	// Get entity list based on entity type
	// For Azure Monitor, we need to know the entities beforehand
	// We could get these from the servicebus client or from configuration

	var entityNames []string

	// TODO: Get entity names from Service Bus Management API or cached results
	// For demonstration, use test data
	switch entityType {
	case "queue":
		entityNames = []string{"queue1", "queue2", "queue3"}
	case "topic":
		entityNames = []string{"topic1", "topic2"}
	case "subscription":
		// For subscriptions, we need to specify both topic and subscription
		entityNames = []string{"topic1/subscription1", "topic1/subscription2", "topic2/subscription1"}
	default:
		return fmt.Errorf("unsupported entity type: %s", entityType)
	}

	c.log.WithFields(logrus.Fields{
		"resourceID":  resourceID,
		"namespace":   namespace,
		"entityType":  entityType,
		"entityCount": len(entityNames),
		"metrics":     metricNames,
	}).Info("Fetching entity metrics from Azure Monitor API")

	// Process each entity
	for _, entityName := range entityNames {
		// Build the entity resource ID
		var entityResourceID string

		switch entityType {
		case "queue":
			entityResourceID = fmt.Sprintf("%s/queues/%s", resourceID, entityName)
		case "topic":
			entityResourceID = fmt.Sprintf("%s/topics/%s", resourceID, entityName)
		case "subscription":
			// For subscriptions, split topic/subscription
			parts := strings.SplitN(entityName, "/", 2)
			if len(parts) != 2 {
				c.log.WithField("entity", entityName).Warn("Invalid subscription name format, expected topic/subscription")
				continue
			}
			topicName, subName := parts[0], parts[1]
			entityResourceID = fmt.Sprintf("%s/topics/%s/subscriptions/%s", resourceID, topicName, subName)
		}

		// Set the timespan for the query (last scrape interval)
		timespan := fmt.Sprintf("%s/%s",
			time.Now().Add(-c.cfg.Metrics.ScrapeInterval).Format(time.RFC3339),
			time.Now().Format(time.RFC3339),
		)

		// Build metricnames string with commas
		metricNamesStr := ""
		for i, name := range metricNames {
			if i > 0 {
				metricNamesStr += ","
			}
			metricNamesStr += name
		}

		// Call Azure Monitor API for each entity
		response, err := c.metricsClient.List(
			ctx,
			entityResourceID,
			&armmonitor.MetricsClientListOptions{
				Timespan:    &timespan,
				Interval:    to.Ptr("PT5M"), // 5-minute intervals
				Metricnames: &metricNamesStr,
				Aggregation: to.Ptr("Total,Average,Maximum"),
			},
		)

		if err != nil {
			c.log.WithError(err).WithFields(logrus.Fields{
				"entityType": entityType,
				"entity":     entityName,
			}).Error("Failed to get entity metrics")
			continue
		}

		// Process metrics from response
		if response.Value != nil {
			for _, metric := range response.Value {
				if metric.Name == nil || metric.Name.Value == nil {
					continue
				}

				metricName := *metric.Name.Value

				if metric.Timeseries == nil || len(metric.Timeseries) == 0 {
					continue
				}

				// Get the latest data point for each metric
				timeSeries := metric.Timeseries[0]
				if timeSeries.Data == nil || len(timeSeries.Data) == 0 {
					continue
				}

				// Use the latest data point
				dataPoint := timeSeries.Data[len(timeSeries.Data)-1]

				// Choose the appropriate value based on the metric type
				var value float64
				var timestamp time.Time

				if dataPoint.TimeStamp != nil {
					timestamp = *dataPoint.TimeStamp
				} else {
					timestamp = time.Now()
				}

				if dataPoint.Total != nil {
					value = *dataPoint.Total
				} else if dataPoint.Average != nil {
					value = *dataPoint.Average
				} else if dataPoint.Maximum != nil {
					value = *dataPoint.Maximum
				} else {
					// Skip if no value available
					continue
				}

				// Create entity metric
				entityMetric := EntityMetric{
					Namespace:  namespace,
					EntityName: entityName,
					EntityType: entityType,
					MetricName: metricName,
					Value:      value,
					Timestamp:  timestamp,
				}

				// Add to the list
				c.entityMetrics = append(c.entityMetrics, entityMetric)

				// Update Prometheus metrics
				promMetricName := metrics.AzureMonitorMetricToPrometheusMetric(metricName)
				labels := prometheus.Labels{
					"namespace":   namespace,
					"entity_name": entityName,
					"entity_type": entityType,
				}

				if gauge, ok := c.metrics[promMetricName]; ok {
					gauge.With(labels).Set(value)
				}
			}
		}
	}

	return nil
}

// collectDetailedMetrics collects detailed performance metrics
func (c *Client) collectDetailedMetrics(ctx context.Context, resourceID, namespace string) error {
	// Define operations to monitor
	operations := []string{"Send", "Receive", "Complete", "Abandon", "Defer", "DeadLetter"}

	// Define metrics to collect
	metricNames := []string{
		"IncomingRequests",
		"OutgoingRequests",
		"RequestLatency",
		"ThrottledRequests",
		"RequestsSucceeded",
		"RequestsFailed",
	}

	c.log.WithFields(logrus.Fields{
		"resourceID": resourceID,
		"namespace":  namespace,
		"operations": operations,
		"metrics":    metricNames,
	}).Info("Fetching detailed performance metrics")

	// Set the timespan for the query (last scrape interval)
	timespan := fmt.Sprintf("%s/%s",
		time.Now().Add(-c.cfg.Metrics.ScrapeInterval).Format(time.RFC3339),
		time.Now().Format(time.RFC3339),
	)

	// For each operation, collect metrics
	for _, operation := range operations {
		// Build filter for operation
		filter := fmt.Sprintf("OperationName eq '%s'", operation)

		// Build metricnames string with commas
		metricNamesStr := ""
		for i, name := range metricNames {
			if i > 0 {
				metricNamesStr += ","
			}
			metricNamesStr += name
		}

		// Call Azure Monitor API with filter
		response, err := c.metricsClient.List(
			ctx,
			resourceID,
			&armmonitor.MetricsClientListOptions{
				Timespan:    &timespan,
				Interval:    to.Ptr("PT5M"), // 5-minute intervals
				Metricnames: &metricNamesStr,
				Filter:      &filter,
				Aggregation: to.Ptr("Total,Average,Maximum"),
			},
		)

		if err != nil {
			c.log.WithError(err).WithFields(logrus.Fields{
				"operation": operation,
			}).Error("Failed to get operation metrics")
			continue
		}

		// Process metrics from response
		if response.Value != nil {
			for _, metric := range response.Value {
				if metric.Name == nil || metric.Name.Value == nil {
					continue
				}

				metricName := *metric.Name.Value

				if metric.Timeseries == nil || len(metric.Timeseries) == 0 {
					continue
				}

				// Get the latest data point for each metric
				timeSeries := metric.Timeseries[0]
				if timeSeries.Data == nil || len(timeSeries.Data) == 0 {
					continue
				}

				// Use the latest data point
				dataPoint := timeSeries.Data[len(timeSeries.Data)-1]

				// Choose the appropriate value based on the metric type
				var value float64
				var timestamp time.Time

				if dataPoint.TimeStamp != nil {
					timestamp = *dataPoint.TimeStamp
				} else {
					timestamp = time.Now()
				}

				if dataPoint.Total != nil {
					value = *dataPoint.Total
				} else if dataPoint.Average != nil {
					value = *dataPoint.Average
				} else if dataPoint.Maximum != nil {
					value = *dataPoint.Maximum
				} else {
					// Skip if no value available
					continue
				}

				// Create entity metric with operation label
				entityMetric := EntityMetric{
					Namespace:  namespace,
					EntityName: "namespace", // Apply to namespace level
					EntityType: "namespace",
					MetricName: metricName,
					Operation:  operation,
					Labels: map[string]string{
						"operation": operation,
					},
					Value:     value,
					Timestamp: timestamp,
				}

				// Add to the list
				c.entityMetrics = append(c.entityMetrics, entityMetric)

				// Update Prometheus metrics
				promMetricName := fmt.Sprintf("%s_%s",
					metrics.AzureMonitorMetricToPrometheusMetric(metricName),
					strings.ToLower(operation),
				)

				labels := prometheus.Labels{
					"namespace": namespace,
					"operation": operation,
				}

				if gauge, ok := c.metrics[promMetricName]; ok {
					gauge.With(labels).Set(value)
				}
			}
		}
	}

	return nil
}

// collectSubscriptionsFromManagementAPI gets subscription names for a topic using the Management API
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
