// pkg/metrics/metrics.go
package metrics

import (
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

// MetricValue represents a metric value with its labels
type MetricValue struct {
	Labels  prometheus.Labels
	Value   float64
	Time    time.Time
}

// ServiceBusMetrics defines all the Azure Service Bus metrics that we track
type ServiceBusMetrics struct {
	registry          *prometheus.Registry
	namespace         string
	metricDefinitions map[string]*prometheus.GaugeVec
}

// NewServiceBusMetrics creates a new ServiceBusMetrics instance
func NewServiceBusMetrics(registry *prometheus.Registry, namespace string) *ServiceBusMetrics {
	m := &ServiceBusMetrics{
		registry:          registry,
		namespace:         namespace,
		metricDefinitions: make(map[string]*prometheus.GaugeVec),
	}
	
	m.initMetricDefinitions()
	return m
}

// initMetricDefinitions initializes all metric definitions
func (m *ServiceBusMetrics) initMetricDefinitions() {
	// Request metrics
	m.registerMetric("successful_requests", "Number of successful requests made to Service Bus", 
		[]string{"namespace", "entity_name", "entity_type"})
	m.registerMetric("server_errors", "Number of server errors in Service Bus", 
		[]string{"namespace", "entity_name", "entity_type"})
	m.registerMetric("user_errors", "Number of user errors in Service Bus", 
		[]string{"namespace", "entity_name", "entity_type"})
	m.registerMetric("throttled_requests", "Number of throttled requests in Service Bus", 
		[]string{"namespace", "entity_name", "entity_type"})

	// Message metrics
	m.registerMetric("incoming_messages", "Number of incoming messages", 
		[]string{"namespace", "entity_name", "entity_type"})
	m.registerMetric("outgoing_messages", "Number of outgoing messages", 
		[]string{"namespace", "entity_name", "entity_type"})
	m.registerMetric("active_messages", "Number of active messages", 
		[]string{"namespace", "entity_name", "entity_type"})
	m.registerMetric("dead_letter_messages", "Number of dead letter messages", 
		[]string{"namespace", "entity_name", "entity_type"})
	m.registerMetric("scheduled_messages", "Number of scheduled messages", 
		[]string{"namespace", "entity_name", "entity_type"})
	m.registerMetric("transfer_messages", "Number of transfer messages", 
		[]string{"namespace", "entity_name", "entity_type"})
	m.registerMetric("transfer_dead_letter_messages", "Number of transfer dead letter messages", 
		[]string{"namespace", "entity_name", "entity_type"})
	
	// Size metrics
	m.registerMetric("size_bytes", "Size of the entity in bytes", 
		[]string{"namespace", "entity_name", "entity_type"})
	m.registerMetric("max_size_bytes", "Maximum size of the entity in bytes", 
		[]string{"namespace", "entity_name", "entity_type"})
	
	// Connection metrics
	m.registerMetric("active_connections", "Number of active connections", 
		[]string{"namespace"})
	
	// Premium tier metrics
	m.registerMetric("cpu_usage", "CPU usage percentage for the namespace", 
		[]string{"namespace"})
	m.registerMetric("memory_usage", "Memory usage for the namespace", 
		[]string{"namespace"})
}

// registerMetric registers a new metric definition
func (m *ServiceBusMetrics) registerMetric(name, help string, labelNames []string) {
	metricName := prometheus.BuildFQName(m.namespace, "servicebus", name)
	gauge := prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: metricName,
		Help: help,
	}, labelNames)
	
	m.registry.MustRegister(gauge)
	m.metricDefinitions[name] = gauge
}

// SetMetricValue sets a metric value
func (m *ServiceBusMetrics) SetMetricValue(metricName string, labels prometheus.Labels, value float64) {
	if gauge, ok := m.metricDefinitions[metricName]; ok {
		gauge.With(labels).Set(value)
	}
}

// GetMetric returns a metric by name
func (m *ServiceBusMetrics) GetMetric(metricName string) *prometheus.GaugeVec {
	if gauge, ok := m.metricDefinitions[metricName]; ok {
		return gauge
	}
	return nil
}

// Reset resets all metrics
func (m *ServiceBusMetrics) Reset() {
	for _, gauge := range m.metricDefinitions {
		gauge.Reset()
	}
}

// FormatEntityLabels creates labels for an entity
func FormatEntityLabels(namespace, entityName, entityType string) prometheus.Labels {
	return prometheus.Labels{
		"namespace":   namespace,
		"entity_name": entityName,
		"entity_type": entityType,
	}
}

// FormatNamespaceLabels creates labels for a namespace
func FormatNamespaceLabels(namespace string) prometheus.Labels {
	return prometheus.Labels{
		"namespace": namespace,
	}
}

// AzureMonitorMetricToPrometheusMetric converts Azure Monitor metric name to Prometheus metric name
func AzureMonitorMetricToPrometheusMetric(azureMetricName string) string {
	// Mapping from Azure metric names to our internal metric names
	metricMap := map[string]string{
		"SuccessfulRequests":            "successful_requests",
		"ServerErrors":                  "server_errors",
		"UserErrors":                    "user_errors",
		"ThrottledRequests":             "throttled_requests",
		"IncomingMessages":              "incoming_messages",
		"OutgoingMessages":              "outgoing_messages",
		"ActiveMessages":                "active_messages",
		"DeadletteredMessages":          "dead_letter_messages",
		"ScheduledMessages":             "scheduled_messages",
		"Size":                          "size_bytes",
		"ActiveConnections":             "active_connections",
		"CPU":                           "cpu_usage",
		"Memory":                        "memory_usage",
	}
	
	if prometheusMetric, ok := metricMap[azureMetricName]; ok {
		return prometheusMetric
	}
	
	// Default: convert camel case to snake case
	return azureMetricName
}

// ServiceBusDimensionToLabel converts Azure Service Bus dimension name to label name
func ServiceBusDimensionToLabel(dimensionName string) string {
	// Mapping from Azure dimension names to our label names
	dimensionMap := map[string]string{
		"EntityName": "entity_name",
	}
	
	if label, ok := dimensionMap[dimensionName]; ok {
		return label
	}
	
	return dimensionName
}