// pkg/metrics/metrics.go
package metrics

import (
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

// MetricValue represents a metric value with its labels
type MetricValue struct {
	Labels prometheus.Labels
	Value  float64
	Time   time.Time
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
	m.registerMetric("connections_opened", "Number of connections opened",
		[]string{"namespace"})
	m.registerMetric("connections_closed", "Number of connections closed",
		[]string{"namespace"})

	// Premium tier metrics
	m.registerMetric("cpu_usage", "CPU usage percentage for the namespace",
		[]string{"namespace"})
	m.registerMetric("memory_usage", "Memory usage for the namespace",
		[]string{"namespace"})

	// Quota metrics
	m.registerMetric("quota_usage_percentage", "Percentage of quota used",
		[]string{"namespace", "quota_name"})

	// Performance metrics by operation
	m.registerMetric("incoming_requests", "Number of incoming requests",
		[]string{"namespace", "entity_name", "entity_type", "operation"})
	m.registerMetric("outgoing_requests", "Number of outgoing requests",
		[]string{"namespace", "entity_name", "entity_type", "operation"})
	m.registerMetric("request_latency_ms", "Request latency in milliseconds",
		[]string{"namespace", "entity_name", "entity_type", "operation"})
	m.registerMetric("requests_succeeded", "Number of successful requests",
		[]string{"namespace", "entity_name", "entity_type", "operation"})
	m.registerMetric("requests_failed", "Number of failed requests",
		[]string{"namespace", "entity_name", "entity_type", "operation"})

	// Message size metrics
	m.registerMetric("message_size_bytes", "Size of messages in bytes",
		[]string{"namespace", "entity_name", "entity_type", "direction"})
	// İşlem sırasındaki mesaj sayıları
	m.registerMetric("processing_messages", "Number of messages being processed",
		[]string{"namespace", "entity_name", "entity_type"})

	// Mesaj yaşı (en eski mesajın yaşı)
	m.registerMetric("oldest_message_age_seconds", "Age of the oldest message in the entity in seconds",
		[]string{"namespace", "entity_name", "entity_type"})

	// Transit mesajlar
	m.registerMetric("transfer_dlq_messages", "Number of messages in the transfer dead-letter queue",
		[]string{"namespace", "entity_name", "entity_type"})

	// Mesaj hızı metrikleri
	m.registerMetric("message_throughput_per_second", "Messages processed per second",
		[]string{"namespace", "entity_name", "entity_type", "direction"})

	// Mesaj işleme gecikmesi
	m.registerMetric("message_processing_latency_ms", "Message processing latency in milliseconds",
		[]string{"namespace", "entity_name", "entity_type", "percentile"})

	// Bağlantı durumu metrikleri
	m.registerMetric("connection_failures", "Number of connection failures",
		[]string{"namespace", "entity_name", "entity_type", "reason"})

	// Premium katman özellikleri
	m.registerMetric("cpu_usage_percent", "CPU usage percentage in Premium tier",
		[]string{"namespace", "entity_name", "entity_type"})

	m.registerMetric("memory_usage_percent", "Memory usage percentage in Premium tier",
		[]string{"namespace", "entity_name", "entity_type"})
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

// FormatOperationLabels creates labels for an operation
func FormatOperationLabels(namespace, entityName, entityType, operation string) prometheus.Labels {
	return prometheus.Labels{
		"namespace":   namespace,
		"entity_name": entityName,
		"entity_type": entityType,
		"operation":   operation,
	}
}

// AzureMonitorMetricToPrometheusMetric converts Azure Monitor metric name to Prometheus metric name
func AzureMonitorMetricToPrometheusMetric(azureMetricName string) string {
	// Mapping from Azure metric names to our internal metric names
	metricMap := map[string]string{
		"SuccessfulRequests":   "successful_requests",
		"ServerErrors":         "server_errors",
		"UserErrors":           "user_errors",
		"ThrottledRequests":    "throttled_requests",
		"IncomingMessages":     "incoming_messages",
		"OutgoingMessages":     "outgoing_messages",
		"ActiveMessages":       "active_messages",
		"DeadletteredMessages": "dead_letter_messages",
		"ScheduledMessages":    "scheduled_messages",
		"Size":                 "size_bytes",
		"ActiveConnections":    "active_connections",
		"ConnectionsOpened":    "connections_opened",
		"ConnectionsClosed":    "connections_closed",
		"CPU":                  "cpu_usage",
		"Memory":               "memory_usage",
		"IncomingRequests":     "incoming_requests",
		"OutgoingRequests":     "outgoing_requests",
		"RequestLatency":       "request_latency_ms",
		"RequestsSucceeded":    "requests_succeeded",
		"RequestsFailed":       "requests_failed",
	}

	if prometheusMetric, ok := metricMap[azureMetricName]; ok {
		return prometheusMetric
	}

	// Default: convert camel case to snake case (simplified)
	var result string
	for i, c := range azureMetricName {
		if i > 0 && c >= 'A' && c <= 'Z' {
			result += "_" + string(c-'A'+'a')
		} else if c >= 'A' && c <= 'Z' {
			result += string(c - 'A' + 'a')
		} else {
			result += string(c)
		}
	}

	return result
}
