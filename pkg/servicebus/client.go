package servicebus

import (
	"context"
	"fmt"
	"regexp"
	"sync"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/messaging/azservicebus"
	"github.com/Azure/azure-sdk-for-go/sdk/messaging/azservicebus/admin"
	"github.com/sirupsen/logrus"

	"azure-servicebus-exporter/pkg/auth"
	"azure-servicebus-exporter/pkg/config"
)

// QueueMetric represents metrics for a queue
type QueueMetric struct {
	Namespace           string
	Name                string
	CreatedAt           time.Time
	UpdatedAt           time.Time
	AccessedAt          time.Time
	TotalMessages       float64
	ActiveMessages      float64
	DeadLetterMessages  float64
	ScheduledMessages   float64
	TransferMessages    float64
	TransferDLQMessages float64
	SizeBytes           float64
	MaxSizeBytes        float64
}

// TopicMetric represents metrics for a topic
type TopicMetric struct {
	Namespace         string
	Name              string
	SizeBytes         float64
	MaxSizeBytes      float64
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
}

// NamespaceMetric represents metrics for a namespace
type NamespaceMetric struct {
	Namespace         string
	ActiveConnections float64
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

	// Cached metrics
	queueMetrics        []QueueMetric
	topicMetrics        []TopicMetric
	subscriptionMetrics []SubscriptionMetric
	namespaceMetrics    []NamespaceMetric

	// Cache
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
		// Try to extract namespace from connection string
		// This is a simple extraction and might need improvement
		connStr := cfg.Auth.ConnectionString
		if connStr != "" {
			re := regexp.MustCompile(`Endpoint=sb://([^.]+)\.servicebus\.windows\.net/`)
			matches := re.FindStringSubmatch(connStr)
			if len(matches) > 1 {
				namespace = matches[1]
			}
		}

		if namespace == "" {
			return nil, fmt.Errorf("could not determine namespace from connection string")
		}
	}

	client := &Client{
		cfg:                 cfg,
		auth:                authProvider,
		log:                 log,
		entityFilter:        entityFilterRegex,
		namespace:           namespace,
		queueMetrics:        []QueueMetric{},
		topicMetrics:        []TopicMetric{},
		subscriptionMetrics: []SubscriptionMetric{},
		namespaceMetrics:    []NamespaceMetric{},
	}

	return client, nil
}

// CollectMetrics collects metrics from Service Bus
func (c *Client) CollectMetrics(ctx context.Context) error {
	c.log.Info("Starting metric collection")
	// Check cache
	c.cacheMutex.RLock()
	if time.Since(c.lastUpdate) < c.cfg.Metrics.CacheDuration {
		c.log.Debug("Using cached metrics, cache duration not expired yet")
		c.cacheMutex.RUnlock()
		return nil
	}
	c.cacheMutex.RUnlock()

	// Lock cache for writing
	c.cacheMutex.Lock()
	defer c.cacheMutex.Unlock()

	// Initialize clients if needed
	if err := c.initializeClients(ctx); err != nil {
		c.log.WithError(err).Error("Failed to initialize clients")
		return fmt.Errorf("failed to initialize clients: %w", err)
	}
	c.log.Info("Successfully initialized clients")

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
		// Just create basic namespace metric as we can't get much from connection string
		namespaceMetric := NamespaceMetric{
			Namespace:         c.namespace,
			ActiveConnections: 0, // Can't get this via connection string
			QuotaUsage:        make(map[string]float64),
		}
		c.namespaceMetrics = append(c.namespaceMetrics, namespaceMetric)
	}

	c.lastUpdate = time.Now()
	return nil
}

// Initialize the clients
func (c *Client) initializeClients(ctx context.Context) error {
	// Initialize Service Bus client
	if c.sbClient == nil {
		c.log.Info("Creating Service Bus client")

		client, err := c.auth.GetServiceBusClient(ctx)
		if err != nil {
			c.log.WithError(err).Error("Failed to create Service Bus client")

			return fmt.Errorf("failed to create Service Bus client: %w", err)
		}
		c.sbClient = client
		c.log.Info("Service Bus client created successfully")
	}

	// Initialize admin client
	if c.adminClient == nil {
		client, err := c.auth.GetServiceBusAdminClient(ctx)
		c.log.Info("Creating Service Bus admin client")
		if err != nil {
			c.log.WithError(err).Warn("Failed to create Service Bus admin client, some metrics may be unavailable")
			// Continue without admin client
		} else {
			c.adminClient = client
			c.log.Info("Service Bus admin client created successfully")
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

	if c.adminClient != nil {
		queueNames, err = c.getQueuesFromAdminClient(ctx)
		if err != nil {
			c.log.WithError(err).Warn("Failed to get queue names using admin client, using test queues")
			queueNames = []string{"queue1", "queue2", "queue3"}
		} else {
			c.log.WithField("count", len(queueNames)).Info("Got queue names from admin client")
		}
	} else {
		c.log.Warn("Admin client not available, using test queues")
		queueNames = []string{"queue1", "queue2", "queue3"}
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
	}

	return nil
}

// getQueuesFromAdminClient gets queue names using the Admin Client
func (c *Client) getQueuesFromAdminClient(ctx context.Context) ([]string, error) {
	var queueNames []string

	// Use pager to list queues
	pager := c.adminClient.NewListQueuesPager(nil)
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			return nil, fmt.Errorf("failed to list queues: %w", err)
		}
		for _, queue := range page.Queues {
			if queue.QueueName != "" {
				queueNames = append(queueNames, queue.QueueName)
			}
		}
	}

	return queueNames, nil
}

// collectQueueMetrics collects metrics for a specific queue
func (c *Client) collectQueueMetrics(ctx context.Context, queueName string) (*QueueMetric, error) {
	c.log.WithField("queue", queueName).Debug("Collecting queue metrics")

	// Default values
	var activeMessages float64
	var dlqMessages float64
	var scheduledMessages float64
	var transferMessages float64
	var transferDLQMessages float64
	var sizeBytes float64
	var maxSizeBytes float64 = 1024 * 1024 * 1024 // 1GB default
	var createdAt time.Time
	var updatedAt time.Time
	var accessedAt time.Time
	var totalMessages float64 = 0

	// Get real metrics if admin client available
	if c.adminClient != nil {
		c.log.WithField("queue", queueName).Info("Admin client available, getting runtime properties")
		// Get runtime properties using admin client
		runtimeProps, err := c.adminClient.GetQueueRuntimeProperties(ctx, queueName, nil)
		if err != nil {
			c.log.WithError(err).WithField("queue", queueName).Warn("Failed to get queue runtime properties")
			c.log.WithField("queue", queueName).Info("Using fallback test data due to error")
			activeMessages = 99
			dlqMessages = 99
		} else if runtimeProps != nil {
			//c.log.WithField("queue", queueName).WithField("runtimeProps", fmt.Sprintf("%+v", runtimeProps)).Debug("Full runtime properties")
			fmt.Printf("RUNTIME PROPS for %s: %+v\n", queueName, runtimeProps)

			// Set metrics from runtime properties
			activeMessages = float64(runtimeProps.ActiveMessageCount)
			dlqMessages = float64(runtimeProps.DeadLetterMessageCount)
			scheduledMessages = float64(runtimeProps.ScheduledMessageCount)
			transferMessages = float64(runtimeProps.TransferMessageCount)
			transferDLQMessages = float64(runtimeProps.TransferDeadLetterMessageCount)
			sizeBytes = float64(runtimeProps.SizeInBytes)
			totalMessages = float64(runtimeProps.TotalMessageCount)
			// Timestamp values
			createdAt = runtimeProps.CreatedAt
			updatedAt = runtimeProps.UpdatedAt
			accessedAt = runtimeProps.AccessedAt

		} else {
			c.log.WithField("queue", queueName).Warn("runtimeProps is nil, skipping metric collection for this queue")
		}

		// Get queue properties
		queueProps, err := c.adminClient.GetQueue(ctx, queueName, nil)
		if err != nil {
			c.log.WithField("queue", queueName).Warn("Failed to get queue properties")
		} else if queueProps != nil && queueProps.MaxSizeInMegabytes != nil {
			maxSizeBytes = float64(*queueProps.MaxSizeInMegabytes) * 1024 * 1024
		} else {
			c.log.WithField("queue", queueName).Warn("queueProps or MaxSizeInMegabytes is nil")
		}
	} else {
		// Use test data if admin client not available
		c.log.Error("Admin client is nil, can't get queue metrics")
		// Use test data as fallback
		c.log.Info("Using test data since admin client is unavailable")
		activeMessages = 10
		dlqMessages = 1
		scheduledMessages = 2
		transferMessages = 0
		transferDLQMessages = 0
		sizeBytes = 5000
		maxSizeBytes = 1024 * 1024 * 1024 // 1GB
	}

	// Create queue metric
	queueMetric := &QueueMetric{
		Namespace:           c.namespace,
		Name:                queueName,
		CreatedAt:           createdAt,
		UpdatedAt:           updatedAt,
		AccessedAt:          accessedAt,
		TotalMessages:       totalMessages,
		ActiveMessages:      activeMessages,
		DeadLetterMessages:  dlqMessages,
		ScheduledMessages:   scheduledMessages,
		TransferMessages:    transferMessages,
		TransferDLQMessages: transferDLQMessages,
		SizeBytes:           sizeBytes,
		MaxSizeBytes:        maxSizeBytes,
	}

	c.log.WithFields(logrus.Fields{
		"queue":   queueName,
		"metrics": queueMetric,
	}).Info("Returning queue metrics")

	return queueMetric, nil
}

// collectTopics collects topic metrics
func (c *Client) collectTopics(ctx context.Context) error {
	c.log.Info("Collecting Service Bus topic metrics")

	// Get topic names
	var topicNames []string
	var err error

	if c.adminClient != nil {
		topicNames, err = c.getTopicsFromAdminClient(ctx)
		if err != nil {
			c.log.WithError(err).Warn("Failed to get topic names using admin client, using test topics")
			topicNames = []string{"topic1", "topic2"}
		} else {
			c.log.WithField("count", len(topicNames)).Info("Got topic names from admin client")
		}
	} else {
		c.log.Warn("Admin client not available, using test topics")
		topicNames = []string{"topic1", "topic2"}
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
	}

	return nil
}

// getTopicsFromAdminClient gets topic names using the Admin Client
func (c *Client) getTopicsFromAdminClient(ctx context.Context) ([]string, error) {
	var topicNames []string

	// Use pager to list topics
	pager := c.adminClient.NewListTopicsPager(nil)
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			return nil, fmt.Errorf("failed to list topics: %w", err)
		}

		for _, topic := range page.Topics {
			if topic.TopicName != "" {
				topicNames = append(topicNames, topic.TopicName)
			}
		}
	}

	return topicNames, nil
}

// collectTopicMetrics collects metrics for a specific topic
func (c *Client) collectTopicMetrics(ctx context.Context, topicName string) (*TopicMetric, error) {
	c.log.WithField("topic", topicName).Debug("Collecting topic metrics")

	// Default values
	var sizeBytes float64
	var maxSizeBytes float64 = 1024 * 1024 * 1024 // 1GB default
	var subscriptionCount float64

	// Get real metrics if admin client available
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
			c.log.WithField("topic", topicName).Warn("runtimeProps for Topic is nil")
		}

		// Get topic properties
		topicProps, err := c.adminClient.GetTopic(ctx, topicName, nil)
		if err != nil {
			c.log.WithError(err).WithField("topic", topicName).Warn("Failed to get topic properties")
		} else if topicProps != nil && topicProps.MaxSizeInMegabytes != nil {
			maxSizeBytes = float64(*topicProps.MaxSizeInMegabytes) * 1024 * 1024
		} else {
			c.log.WithField("topic", topicName).Warn("topicProps or MaxSizeInMegabytes is nil")
		}
	} else {
		// Use test data if admin client not available
		sizeBytes = 3000
		maxSizeBytes = 1024 * 1024 * 1024 // 1GB
		subscriptionCount = 2
	}

	// Create topic metric
	return &TopicMetric{
		Namespace:         c.namespace,
		Name:              topicName,
		SizeBytes:         sizeBytes,
		MaxSizeBytes:      maxSizeBytes,
		SubscriptionCount: subscriptionCount,
	}, nil
}

// collectSubscriptions collects metrics for all subscriptions of a topic
func (c *Client) collectSubscriptions(ctx context.Context, topicName string) error {
	c.log.WithField("topic", topicName).Debug("Collecting topic subscriptions")

	// Get subscription names
	var subscriptionNames []string
	var err error

	if c.adminClient != nil {
		subscriptionNames, err = c.getSubscriptionsFromAdminClient(ctx, topicName)
		if err != nil {
			c.log.WithError(err).WithField("topic", topicName).Warn("Failed to get subscription names, using test subscriptions")
			subscriptionNames = []string{"sub1", "sub2"}
		} else {
			c.log.WithFields(logrus.Fields{
				"topic": topicName,
				"count": len(subscriptionNames),
			}).Info("Got subscription names from admin client")
		}
	} else {
		c.log.Warn("Admin client not available, using test subscriptions")
		subscriptionNames = []string{"sub1", "sub2"}
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
	}

	return nil
}

// getSubscriptionsFromAdminClient gets subscription names using the Admin Client
func (c *Client) getSubscriptionsFromAdminClient(ctx context.Context, topicName string) ([]string, error) {
	var subscriptionNames []string

	// Use pager to list subscriptions
	pager := c.adminClient.NewListSubscriptionsPager(topicName, nil)
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			return nil, fmt.Errorf("failed to list subscriptions for topic %s: %w", topicName, err)
		}

		for _, subscription := range page.Subscriptions {
			if subscription.SubscriptionName != "" {
				subscriptionNames = append(subscriptionNames, subscription.SubscriptionName)
			}
		}
	}

	return subscriptionNames, nil
}

// collectSubscriptionMetrics collects metrics for a specific subscription
func (c *Client) collectSubscriptionMetrics(ctx context.Context, topicName, subName string) (*SubscriptionMetric, error) {
	c.log.WithFields(logrus.Fields{
		"topic":        topicName,
		"subscription": subName,
	}).Debug("Collecting subscription metrics")

	// Default values
	var activeMessages float64
	var dlqMessages float64
	var scheduledMessages float64
	var transferMessages float64
	var transferDLQMessages float64

	// Get real metrics if admin client available
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
			scheduledMessages = 0 // Not available in current API version
			transferMessages = float64(runtimeProps.TransferMessageCount)
			transferDLQMessages = float64(runtimeProps.TransferDeadLetterMessageCount)
		} else {
			c.log.WithField("topic", topicName).WithField("subscription", subName).Warn("runtimeProps is nil")
		}
	} else {
		// Use test data if admin client not available
		activeMessages = 3
		dlqMessages = 0
		scheduledMessages = 1
		transferMessages = 0
		transferDLQMessages = 0
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
	}, nil
}
