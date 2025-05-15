package server

import (
    "context"
    "net/http"
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
}

// NewServer creates a new HTTP server
func NewServer(cfg *config.Config, log *logrus.Logger, collector *collector.ServiceBusCollector) *Server {
    server := &Server{
        cfg:       cfg,
        log:       log,
        collector: collector,
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

// Handler implementations
func (s *Server) handleProbeMetrics(c *gin.Context) {
    // Örnek yanıt
    c.JSON(http.StatusOK, gin.H{
        "status": "ok",
        "message": "This endpoint will provide Service Bus metrics",
    })
}

func (s *Server) handleProbeMetricsList(c *gin.Context) {
    // Örnek yanıt
    c.JSON(http.StatusOK, gin.H{
        "status": "ok",
        "message": "This endpoint will provide Service Bus metrics list",
    })
}

func (s *Server) handleProbeMetricsResource(c *gin.Context) {
    // Örnek yanıt
    c.JSON(http.StatusOK, gin.H{
        "status": "ok",
        "message": "This endpoint will provide Service Bus resource metrics",
    })
}

func (s *Server) handleStatus(c *gin.Context) {
    c.JSON(http.StatusOK, gin.H{
        "status":  "ok",
        "version": "1.0.0", // Versiyon bilgisi ekle
    })
}

func (s *Server) handleHealth(c *gin.Context) {
    c.JSON(http.StatusOK, gin.H{"status": "healthy"})
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