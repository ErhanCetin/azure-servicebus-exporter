package server

import (
	"context"
	"fmt"
	"net/http"
	"runtime"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/sirupsen/logrus"

	"azure-servicebus-exporter/pkg/collector"
	"azure-servicebus-exporter/pkg/config"
)

// Server represents the HTTP server
type Server struct {
	cfg        *config.Config
	log        *logrus.Logger
	router     *gin.Engine
	httpServer *http.Server
	collector  *collector.ServiceBusCollector
	startTime  time.Time
}

// NewServer creates a new HTTP server
func NewServer(cfg *config.Config, log *logrus.Logger, sbCollector *collector.ServiceBusCollector) *Server {
	server := &Server{
		cfg:       cfg,
		log:       log,
		collector: sbCollector,
		startTime: time.Now(),
	}

	return server
}

// Setup configures the HTTP server
func (s *Server) Setup() {
	// Gin router oluştur
	r := gin.New()
	r.Use(gin.Recovery())

	// Logger middleware ekle
	r.Use(func(c *gin.Context) {
		start := time.Now()
		path := c.Request.URL.Path

		c.Next()

		end := time.Now()
		latency := end.Sub(start)

		s.log.WithFields(logrus.Fields{
			"status":     c.Writer.Status(),
			"method":     c.Request.Method,
			"path":       path,
			"ip":         c.ClientIP(),
			"duration":   latency,
			"user-agent": c.Request.UserAgent(),
		}).Info()
	})

	// Prometheus handler'ı kaydet
	registry := prometheus.NewRegistry()
	registry.MustRegister(s.collector)

	h := promhttp.HandlerFor(registry, promhttp.HandlerOpts{
		Registry: registry,
	})

	// Default metrics
	r.GET("/metrics", gin.WrapH(h))

	// Probe metrics - liste sorguları
	r.GET("/probe/metrics", s.handleProbeMetrics)
	r.GET("/probe/metrics/list", s.handleProbeMetricsList)
	r.GET("/probe/metrics/resource", s.handleProbeMetricsResource)

	// Status ve health endpoint'leri
	r.GET("/status", s.handleStatus)
	r.GET("/health", s.handleHealth)

	// Query UI (geliştirme için)
	r.GET("/query", s.handleQueryUI)

	s.router = r
	s.httpServer = &http.Server{
		Addr:         s.cfg.Server.Listen,
		Handler:      s.router,
		ReadTimeout:  s.cfg.Server.Timeout,
		WriteTimeout: s.cfg.Server.Timeout,
	}
}

// Start starts the HTTP server
func (s *Server) Start() error {
	return s.httpServer.ListenAndServe()
}

// Stop stops the HTTP server
func (s *Server) Stop(ctx context.Context) error {
	return s.httpServer.Shutdown(ctx)
}

// handleProbeMetrics handles requests to /probe/metrics
func (s *Server) handleProbeMetrics(c *gin.Context) {
	ctx, cancel := context.WithTimeout(c.Request.Context(), time.Minute)
	defer cancel()

	// Collect fresh metrics from Service Bus
	if err := s.collector.GetServiceBus().CollectMetrics(ctx); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"status":  "error",
			"error":   err.Error(),
			"message": "Failed to collect Service Bus metrics",
		})
		return
	}

	// Get the metrics
	queueMetrics := s.collector.GetServiceBus().GetQueueMetrics()
	topicMetrics := s.collector.GetServiceBus().GetTopicMetrics()
	subscriptionMetrics := s.collector.GetServiceBus().GetSubscriptionMetrics()
	namespaceMetrics := s.collector.GetServiceBus().GetNamespaceMetrics()

	// Format response
	response := gin.H{
		"status":    "success",
		"timestamp": time.Now().Format(time.RFC3339),
		"metrics": gin.H{
			"queues":        queueMetrics,
			"topics":        topicMetrics,
			"subscriptions": subscriptionMetrics,
			"namespaces":    namespaceMetrics,
		},
	}

	c.JSON(http.StatusOK, response)
}

// handleProbeMetricsList handles requests to /probe/metrics/list
func (s *Server) handleProbeMetricsList(c *gin.Context) {
	ctx, cancel := context.WithTimeout(c.Request.Context(), time.Minute)
	defer cancel()

	// Collect fresh metrics from Service Bus
	if err := s.collector.GetServiceBus().CollectMetrics(ctx); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"status":  "error",
			"error":   err.Error(),
			"message": "Failed to collect Service Bus metrics",
		})
		return
	}

	// Get queue and topic names
	queueMetrics := s.collector.GetServiceBus().GetQueueMetrics()
	topicMetrics := s.collector.GetServiceBus().GetTopicMetrics()
	subscriptionMetrics := s.collector.GetServiceBus().GetSubscriptionMetrics()

	queues := make([]string, 0, len(queueMetrics))
	for _, q := range queueMetrics {
		queues = append(queues, q.Name)
	}

	topics := make([]string, 0, len(topicMetrics))
	for _, t := range topicMetrics {
		topics = append(topics, t.Name)
	}

	subscriptions := make([]map[string]string, 0, len(subscriptionMetrics))
	for _, s := range subscriptionMetrics {
		subscriptions = append(subscriptions, map[string]string{
			"topic":        s.TopicName,
			"subscription": s.Name,
		})
	}

	// Format response
	response := gin.H{
		"status":    "success",
		"timestamp": time.Now().Format(time.RFC3339),
		"entities": gin.H{
			"queues":        queues,
			"topics":        topics,
			"subscriptions": subscriptions,
		},
	}

	c.JSON(http.StatusOK, response)
}

// handleProbeMetricsResource handles requests to /probe/metrics/resource
func (s *Server) handleProbeMetricsResource(c *gin.Context) {
	// Get entity type and name from query parameters
	entityType := c.Query("type")
	entityName := c.Query("name")

	if entityType == "" || entityName == "" {
		c.JSON(http.StatusBadRequest, gin.H{
			"status":  "error",
			"message": "Both 'type' and 'name' query parameters are required",
		})
		return
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), time.Minute)
	defer cancel()

	// Collect fresh metrics from Service Bus
	if err := s.collector.GetServiceBus().CollectMetrics(ctx); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"status":  "error",
			"error":   err.Error(),
			"message": "Failed to collect Service Bus metrics",
		})
		return
	}

	// Filter metrics based on entity type and name
	var result interface{}
	var found bool

	switch entityType {
	case "queue":
		for _, q := range s.collector.GetServiceBus().GetQueueMetrics() {
			if q.Name == entityName {
				result = q
				found = true
				break
			}
		}
	case "topic":
		for _, t := range s.collector.GetServiceBus().GetTopicMetrics() {
			if t.Name == entityName {
				result = t
				found = true
				break
			}
		}
	case "subscription":
		// Format: "topicName/subscriptionName"
		parts := strings.SplitN(entityName, "/", 2)
		if len(parts) != 2 {
			c.JSON(http.StatusBadRequest, gin.H{
				"status":  "error",
				"message": "For subscription type, name should be in format 'topicName/subscriptionName'",
			})
			return
		}

		topicName, subName := parts[0], parts[1]
		for _, s := range s.collector.GetServiceBus().GetSubscriptionMetrics() {
			if s.TopicName == topicName && s.Name == subName {
				result = s
				found = true
				break
			}
		}
	default:
		c.JSON(http.StatusBadRequest, gin.H{
			"status":  "error",
			"message": "Entity type must be one of: queue, topic, subscription",
		})
		return
	}

	if !found {
		c.JSON(http.StatusNotFound, gin.H{
			"status":  "error",
			"message": fmt.Sprintf("%s with name '%s' not found", entityType, entityName),
		})
		return
	}

	// Format response
	response := gin.H{
		"status":      "success",
		"timestamp":   time.Now().Format(time.RFC3339),
		"entity_type": entityType,
		"entity_name": entityName,
		"metrics":     result,
	}

	c.JSON(http.StatusOK, response)
}

// handleStatus handles requests to /status
func (s *Server) handleStatus(c *gin.Context) {
	buildInfo := map[string]string{
		"version":    "1.0.0", // Sürüm bilgisini uygun şekilde değiştirin
		"go_version": runtime.Version(),
		"os":         runtime.GOOS,
		"arch":       runtime.GOARCH,
	}

	status := "ok"

	// Service Bus bağlantısını kontrol et
	ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
	defer cancel()

	if err := s.collector.GetServiceBus().CollectMetrics(ctx); err != nil {
		status = "degraded"
		buildInfo["service_bus_status"] = "connection error"
	} else {
		buildInfo["service_bus_status"] = "connected"
	}

	c.JSON(http.StatusOK, gin.H{
		"status":     status,
		"timestamp":  time.Now().Format(time.RFC3339),
		"uptime":     time.Since(s.startTime).String(),
		"build_info": buildInfo,
	})
}

// handleHealth handles requests to /health
func (s *Server) handleHealth(c *gin.Context) {
	// Bu endpoint basit bir sağlık kontrolü sağlar
	// Uygulama çalışıyorsa OK döner
	c.JSON(http.StatusOK, gin.H{
		"status":    "healthy",
		"timestamp": time.Now().Format(time.RFC3339),
	})
}

func (s *Server) handleQueryUI(c *gin.Context) {
	// Geliştirme için basit bir web UI
	html := `
	<!DOCTYPE html>
	<html>
	<head>
		<title>Azure Service Bus Exporter Query UI</title>
		<style>
			body {
				font-family: Arial, sans-serif;
				max-width: 800px;
				margin: 0 auto;
				padding: 20px;
			}
			h1 {
				color: #0078d4;
			}
			.form-group {
				margin-bottom: 15px;
			}
			label {
				display: block;
				margin-bottom: 5px;
			}
			input, select {
				width: 100%;
				padding: 8px;
				box-sizing: border-box;
			}
			button {
				background-color: #0078d4;
				color: white;
				padding: 10px 15px;
				border: none;
				cursor: pointer;
			}
			#result {
				margin-top: 20px;
				background-color: #f5f5f5;
				padding: 15px;
				border-radius: 5px;
				white-space: pre-wrap;
			}
		</style>
	</head>
	<body>
		<h1>Azure Service Bus Exporter Query UI</h1>
		
		<div class="form-group">
			<label for="endpoint">Endpoint:</label>
			<select id="endpoint">
				<option value="/metrics">Default metrics</option>
				<option value="/probe/metrics">Probe metrics</option>
				<option value="/probe/metrics/list">Metrics list</option>
				<option value="/probe/metrics/resource">Resource metrics</option>
			</select>
		</div>
		
		<div class="form-group">
			<label for="params">Query Parameters (optional):</label>
			<input type="text" id="params" placeholder="name=value&name2=value2">
		</div>
		
		<button onclick="fetchMetrics()">Fetch Metrics</button>
		
		<div id="result"></div>
		
		<script>
			function fetchMetrics() {
				const endpoint = document.getElementById('endpoint').value;
				const params = document.getElementById('params').value;
				const url = endpoint + (params ? '?' + params : '');
				
				document.getElementById('result').textContent = 'Loading...';
				
				fetch(url)
					.then(response => response.text())
					.then(data => {
						document.getElementById('result').textContent = data;
					})
					.catch(error => {
						document.getElementById('result').textContent = 'Error: ' + error;
					});
			}
		</script>
	</body>
	</html>
	`

	c.Header("Content-Type", "text/html")
	c.String(http.StatusOK, html)
}
