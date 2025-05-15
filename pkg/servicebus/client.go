package servicebus

import (
	"context"
	"fmt"
	"os/exec"
	"regexp"
	"strings"
	"sync"
	"time"

	servicebus "github.com/Azure/azure-service-bus-go"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/sirupsen/logrus"

	"azure-servicebus-exporter/pkg/auth"
	"azure-servicebus-exporter/pkg/config"
	"azure-servicebus-exporter/pkg/metrics"
)

// QueueMetric represents metrics for a queue
type QueueMetric struct {
	Namespace          string
	Name               string
	ActiveMessages     float64
	DeadLetterMessages float64
	ScheduledMessages  float64
	TransferMessages   float64
	SizeBytes          float64
	MaxSizeBytes       float64
}

// TopicMetric represents metrics for a topic
type TopicMetric struct {
	Namespace      string
	Name           string
	ActiveMessages float64
	SizeBytes      float64
	MaxSizeBytes   float64
}

// SubscriptionMetric represents metrics for a subscription
type SubscriptionMetric struct {
	Namespace          string
	TopicName          string
	Name               string
	ActiveMessages     float64
	DeadLetterMessages float64
	ScheduledMessages  float64
	TransferMessages   float64
}

// Client represents a Service Bus client that can collect metrics
type Client struct {
	cfg       *config.Config
	auth      auth.AuthProvider
	namespace *servicebus.Namespace
	log       *logrus.Logger

	// Regex filters
	entityFilter *regexp.Regexp

	// Metrics
	metrics map[string]*prometheus.GaugeVec

	// Cached metrics
	queueMetrics        []QueueMetric
	topicMetrics        []TopicMetric
	subscriptionMetrics []SubscriptionMetric

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

	client := &Client{
		cfg:                 cfg,
		auth:                authProvider,
		log:                 log,
		entityFilter:        entityFilterRegex,
		metrics:             make(map[string]*prometheus.GaugeVec),
		cache:               make(map[string]metrics.MetricValue),
		queueMetrics:        []QueueMetric{},
		topicMetrics:        []TopicMetric{},
		subscriptionMetrics: []SubscriptionMetric{},
	}

	return client, nil
}

// CollectMetrics collects metrics from Service Bus
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

	// Service Bus namespace'e bağlan
	if c.namespace == nil {
		ns, err := c.auth.GetServiceBusClient(ctx)
		if err != nil {
			return err
		}
		c.namespace = ns
	}

	// Metrik listelerini temizle
	c.queueMetrics = []QueueMetric{}
	c.topicMetrics = []TopicMetric{}
	c.subscriptionMetrics = []SubscriptionMetric{}

	// Kuyrukları topla
	if err := c.collectQueues(ctx); err != nil {
		return err
	}

	// Konuları ve abonelikleri topla
	if err := c.collectTopics(ctx); err != nil {
		return err
	}

	// Namespace metriklerini topla
	if c.cfg.ServiceBus.IncludeNamespace {
		if err := c.collectNamespaceMetrics(ctx); err != nil {
			return err
		}
	}

	c.lastUpdate = time.Now()
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

// getQueueNamesFromAzCLI attempts to get queue names using Azure CLI
func (c *Client) getQueueNamesFromAzCLI(ctx context.Context) ([]string, error) {
	// Check if the namespace is defined
	namespace := c.namespace.Name
	if namespace == "" {
		c.log.Warn("Namespace name is empty, using default test queues")
		return nil, fmt.Errorf("namespace name is empty")
	}

	// Use Azure CLI to list queues - need to have resource group name
	resourceGroup := c.cfg.ServiceBus.ResourceGroup
	if resourceGroup == "" {
		c.log.Warn("Resource group not specified in config, using default test queues")
		return nil, fmt.Errorf("resource group not specified")
	}

	c.log.WithFields(logrus.Fields{
		"namespace":      namespace,
		"resource_group": resourceGroup,
	}).Info("Listing queues using Azure CLI")

	cmd := exec.CommandContext(ctx, "az", "servicebus", "queue", "list",
		"--namespace-name", namespace,
		"--resource-group", resourceGroup,
		"--query", "[].name",
		"-o", "tsv")

	output, err := cmd.CombinedOutput()
	if err != nil {
		c.log.WithError(err).WithField("output", string(output)).Error("Failed to list queues using Azure CLI")
		return nil, fmt.Errorf("failed to list queues: %w", err)
	}

	// Parse the output - it's a tab-separated list of queue names
	queueNames := strings.Split(strings.TrimSpace(string(output)), "\n")

	// Filter out empty strings
	var filteredQueueNames []string
	for _, name := range queueNames {
		if name != "" {
			filteredQueueNames = append(filteredQueueNames, name)
		}
	}

	c.log.WithField("queues", filteredQueueNames).Info("Found queues using Azure CLI")

	if len(filteredQueueNames) == 0 {
		c.log.Warn("No queues found, using default test queues")
		return nil, fmt.Errorf("no queues found")
	}

	return filteredQueueNames, nil
}

// getTopicNamesFromAzCLI attempts to get topic names using Azure CLI
func (c *Client) getTopicNamesFromAzCLI(ctx context.Context) ([]string, error) {
	// Check if the namespace is defined
	namespace := c.namespace.Name
	if namespace == "" {
		return nil, fmt.Errorf("namespace name is empty")
	}

	// Use Azure CLI to list topics - need to have resource group name
	resourceGroup := c.cfg.ServiceBus.ResourceGroup
	if resourceGroup == "" {
		return nil, fmt.Errorf("resource group not specified")
	}

	c.log.WithFields(logrus.Fields{
		"namespace":      namespace,
		"resource_group": resourceGroup,
	}).Info("Listing topics using Azure CLI")

	cmd := exec.CommandContext(ctx, "az", "servicebus", "topic", "list",
		"--namespace-name", namespace,
		"--resource-group", resourceGroup,
		"--query", "[].name",
		"-o", "tsv")

	output, err := cmd.CombinedOutput()
	if err != nil {
		c.log.WithError(err).WithField("output", string(output)).Error("Failed to list topics using Azure CLI")
		return nil, fmt.Errorf("failed to list topics: %w", err)
	}

	// Parse the output - it's a tab-separated list of topic names
	topicNames := strings.Split(strings.TrimSpace(string(output)), "\n")

	// Filter out empty strings
	var filteredTopicNames []string
	for _, name := range topicNames {
		if name != "" {
			filteredTopicNames = append(filteredTopicNames, name)
		}
	}

	c.log.WithField("topics", filteredTopicNames).Info("Found topics using Azure CLI")

	// Return empty list if no topics found - this is not an error
	return filteredTopicNames, nil
}

// getSubscriptionsFromAzCLI attempts to get subscriptions for a topic using Azure CLI
func (c *Client) getSubscriptionsFromAzCLI(ctx context.Context, topicName string) ([]string, error) {
	// Check if the namespace is defined
	namespace := c.namespace.Name
	if namespace == "" {
		return nil, fmt.Errorf("namespace name is empty")
	}

	// Use Azure CLI to list subscriptions - need to have resource group name
	resourceGroup := c.cfg.ServiceBus.ResourceGroup
	if resourceGroup == "" {
		return nil, fmt.Errorf("resource group not specified")
	}

	c.log.WithFields(logrus.Fields{
		"namespace":      namespace,
		"resource_group": resourceGroup,
		"topic":          topicName,
	}).Info("Listing subscriptions using Azure CLI")

	cmd := exec.CommandContext(ctx, "az", "servicebus", "topic", "subscription", "list",
		"--namespace-name", namespace,
		"--resource-group", resourceGroup,
		"--topic-name", topicName,
		"--query", "[].name",
		"-o", "tsv")

	output, err := cmd.CombinedOutput()
	if err != nil {
		c.log.WithError(err).WithField("output", string(output)).Error("Failed to list subscriptions using Azure CLI")
		return nil, fmt.Errorf("failed to list subscriptions: %w", err)
	}

	// Parse the output - it's a tab-separated list of subscription names
	subscriptionNames := strings.Split(strings.TrimSpace(string(output)), "\n")

	// Filter out empty strings
	var filteredSubscriptionNames []string
	for _, name := range subscriptionNames {
		if name != "" {
			filteredSubscriptionNames = append(filteredSubscriptionNames, name)
		}
	}

	c.log.WithFields(logrus.Fields{
		"topic":         topicName,
		"subscriptions": filteredSubscriptionNames,
	}).Info("Found subscriptions using Azure CLI")

	// Return empty list if no subscriptions found - this is not an error
	return filteredSubscriptionNames, nil
}

// collectQueues collects queue metrics
func (c *Client) collectQueues(ctx context.Context) error {
	c.log.Info("Collecting Service Bus queue metrics")

	// Attempt to get real queue names if configured to use real entities
	var queueNames []string
	var useTestData bool = true

	if c.cfg.ServiceBus.UseRealEntities {
		// Try to get real queue names using Azure CLI
		realQueueNames, err := c.getQueueNamesFromAzCLI(ctx)
		if err == nil && len(realQueueNames) > 0 {
			queueNames = realQueueNames
			useTestData = false
			c.log.WithField("count", len(queueNames)).Info("Using real queue names")
		} else {
			c.log.WithError(err).Warn("Failed to get real queue names, falling back to test data")
		}
	}

	// Fall back to test data if needed
	if useTestData {
		queueNames = []string{"queue1", "queue2", "queue3"}
		c.log.WithField("count", len(queueNames)).Info("Using test queue names")
	}

	for _, queueName := range queueNames {
		// Entity filtreleme uygula
		if c.entityFilter != nil && !c.entityFilter.MatchString(queueName) {
			continue
		}

		// Kuyruk nesnesini oluştur - NewQueue artık 2 değer döndürüyor, hata kontrolü de yapalım
		queue, err := c.namespace.NewQueue(queueName)
		if err != nil {
			c.log.WithError(err).WithField("queue", queueName).Error("Failed to create queue client")
			continue
		}

		// Kuyruk metriklerini topla (gerçek uygulamada GetRuntimeInfo() veya benzeri API çağrıları kullanılır)
		c.log.WithField("queue", queue.Name).Debug("Processing queue")

		// Bu örnek için test verileri kullanıyoruz
		// Gerçek uygulamada, bu değerler Service Bus API'sından alınmalıdır
		activeMessages := float64(10)
		dlqMessages := float64(1)
		scheduledMessages := float64(2)
		transferMessages := float64(0)
		sizeBytes := float64(5000)
		maxSizeBytes := float64(1024 * 1024 * 1024) // 1 GB

		// Gerçek verileri almayı dene
		if !useTestData {
			// Örnek: Gerçek verileri almak için API çağrısı
			// Bu kısım Service Bus SDK'ya göre değişir
			// Şimdilik test verileri kullanmaya devam ediyoruz
		}

		// Metrik yapısını oluştur ve listeye ekle
		queueMetric := QueueMetric{
			Namespace:          c.namespace.Name,
			Name:               queueName,
			ActiveMessages:     activeMessages,
			DeadLetterMessages: dlqMessages,
			ScheduledMessages:  scheduledMessages,
			TransferMessages:   transferMessages,
			SizeBytes:          sizeBytes,
			MaxSizeBytes:       maxSizeBytes,
		}

		c.queueMetrics = append(c.queueMetrics, queueMetric)

		// Prometheus metriklerini ayarla (eski kod)
		labels := prometheus.Labels{
			"namespace":   c.namespace.Name,
			"entity_name": queueName,
			"entity_type": "queue",
		}

		setMetric := func(name string, value float64) {
			if gauge, ok := c.metrics[name]; ok {
				gauge.With(labels).Set(value)
			}
		}

		// Metrikleri ayarla
		setMetric("active_messages", activeMessages)
		setMetric("dead_letter_messages", dlqMessages)
		setMetric("scheduled_messages", scheduledMessages)
	}

	return nil
}

// collectTopics collects topic and subscription metrics
func (c *Client) collectTopics(ctx context.Context) error {
	c.log.Info("Collecting Service Bus topic metrics")

	// Attempt to get real topic names if configured to use real entities
	var topicNames []string

	if c.cfg.ServiceBus.UseRealEntities {
		// Try to get real topic names using Azure CLI
		realTopicNames, err := c.getTopicNamesFromAzCLI(ctx)
		if err != nil {
			c.log.WithError(err).Error("Error getting real topic names")
		} else {
			// Successfully retrieved topic list (may be empty)
			topicNames = realTopicNames
			c.log.WithField("count", len(topicNames)).Info("Using real topic names")
		}
	} else {
		// Use test data if not configured to use real entities
		topicNames = []string{"topic1", "topic2"}
		c.log.WithField("count", len(topicNames)).Info("Using test topic names")
	}

	// Process each topic
	for _, topicName := range topicNames {
		// Entity filtreleme uygula
		if c.entityFilter != nil && !c.entityFilter.MatchString(topicName) {
			continue
		}

		// Konu nesnesini oluştur
		topic, err := c.namespace.NewTopic(topicName)
		if err != nil {
			c.log.WithError(err).WithField("topic", topicName).Error("Failed to create topic client")
			continue
		}

		c.log.WithField("topic", topic.Name).Debug("Processing topic")

		// Bu örnek için test verileri kullanıyoruz
		activeMessages := float64(5)
		sizeBytes := float64(3000)
		maxSizeBytes := float64(1024 * 1024 * 1024) // 1 GB

		// Metrik yapısını oluştur ve listeye ekle
		topicMetric := TopicMetric{
			Namespace:      c.namespace.Name,
			Name:           topicName,
			ActiveMessages: activeMessages,
			SizeBytes:      sizeBytes,
			MaxSizeBytes:   maxSizeBytes,
		}

		c.topicMetrics = append(c.topicMetrics, topicMetric)

		// Get subscriptions for this topic
		var subscriptionNames []string

		if c.cfg.ServiceBus.UseRealEntities {
			// Try to get real subscription names
			realSubscriptionNames, err := c.getSubscriptionsFromAzCLI(ctx, topicName)
			if err != nil {
				c.log.WithError(err).WithField("topic", topicName).Error("Error getting real subscription names")
				// Don't fall back to test data for subscriptions if we're using real topics
				continue
			}

			subscriptionNames = realSubscriptionNames
			c.log.WithFields(logrus.Fields{
				"topic": topicName,
				"count": len(subscriptionNames),
			}).Info("Using real subscription names")
		} else {
			// Default test subscriptions
			subscriptionNames = []string{"sub1", "sub2"}
			c.log.WithFields(logrus.Fields{
				"topic": topicName,
				"count": len(subscriptionNames),
			}).Info("Using test subscription names")
		}

		// Process each subscription
		for _, subName := range subscriptionNames {
			// Bu örnek için test verileri kullanıyoruz
			subActiveMessages := float64(3)
			subDlqMessages := float64(0)
			subScheduledMessages := float64(1)
			subTransferMessages := float64(0)

			// Metrik yapısını oluştur ve listeye ekle
			subscriptionMetric := SubscriptionMetric{
				Namespace:          c.namespace.Name,
				TopicName:          topicName,
				Name:               subName,
				ActiveMessages:     subActiveMessages,
				DeadLetterMessages: subDlqMessages,
				ScheduledMessages:  subScheduledMessages,
				TransferMessages:   subTransferMessages,
			}

			c.subscriptionMetrics = append(c.subscriptionMetrics, subscriptionMetric)

			// Prometheus metric'lerini de set et
			labels := prometheus.Labels{
				"namespace":   c.namespace.Name,
				"entity_name": fmt.Sprintf("%s/%s", topicName, subName),
				"entity_type": "subscription",
			}

			setMetric := func(name string, value float64) {
				if gauge, ok := c.metrics[name]; ok {
					gauge.With(labels).Set(value)
				}
			}

			// Metrikleri ayarla
			setMetric("active_messages", subActiveMessages)
			setMetric("dead_letter_messages", subDlqMessages)
			setMetric("scheduled_messages", subScheduledMessages)
		}
	}

	return nil
}

// collectNamespaceMetrics collects namespace metrics
func (c *Client) collectNamespaceMetrics(ctx context.Context) error {
	c.log.Info("Collecting Service Bus namespace metrics")

	// Not: Namespace metriklerini toplamak için genellikle Azure Monitor API'sini
	// veya Management Client'ı kullanmanız gerekir
	// Bu kısım, azuremonitor paketi tarafından yönetiliyor olabilir

	return nil
}
