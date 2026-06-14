package main

import (
	"embed"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
)

//go:embed all:frontend/dist
var assets embed.FS

func main() {
	// Determine config path
	configPath := os.Getenv("CONFIG_PATH")
	if configPath == "" {
		configPath = DefaultConfigPath()
	}

	// Load configuration
	cfg, err := NewConfigStore(configPath)
	if err != nil {
		log.Fatalf("Failed to load config: %v", err)
	}

	// Initialize components
	stability := NewStabilityManager(cfg)
	proxy := NewProxy(cfg, stability)
	stats := NewStatsManager()
	pm := NewProviderManager(cfg)

	// Wire up dependencies
	proxy.stats = stats
	proxy.pm = pm

	// Start HTTP proxy for Claude Code in background
	go startProxyServer(cfg, proxy, stats, pm, stability)

	// Create Wails app
	app := NewApp(cfg, proxy, stats, pm, stability)

	// Sub-filesystem for assets
	assetsSub, err := fs.Sub(assets, "frontend/dist")
	if err != nil {
		log.Fatalf("Failed to get frontend/dist sub-filesystem: %v", err)
	}

	err = wails.Run(&options.App{
		Title:     "Claude Proxy Pro v1.0.0",
		Width:     1280,
		Height:    800,
		MinWidth:  900,
		MinHeight: 600,
		AssetServer: &assetserver.Options{
			Assets: assetsSub,
		},
		BackgroundColour: &options.RGBA{R: 15, G: 23, B: 42, A: 1},
		OnStartup:        app.startup,
		Bind: []interface{}{
			app,
		},
	})

	if err != nil {
		log.Fatal(err)
	}
}

func startProxyServer(cfg *ConfigStore, proxy *Proxy, stats *StatsManager, pm *ProviderManager, stability *StabilityManager) {
	appCfg := cfg.Get()
	port := appCfg.Port
	if p := os.Getenv("PORT"); p != "" {
		port = p
	}

	// Start periodic health checks
	checkInterval := time.Duration(appCfg.CheckInterval) * time.Second
	if checkInterval <= 0 {
		checkInterval = 60 * time.Second
	}
	pm.StartHealthChecker(checkInterval)

	// Start initial discovery
	go func() {
		time.Sleep(2 * time.Second)
		pm.DiscoverModels()
	}()

	// Print banner
	fmt.Println("┌──────────────────────────────────────────────┐")
	fmt.Println("│       Claude Proxy Pro v1.0.0  (Wails)      │")
	fmt.Println("├──────────────────────────────────────────────┤")
	fmt.Printf("│  Port        : %-30s│\n", ":"+port)
	fmt.Printf("│  Config      : %-30s│\n", cfg.path)
	prov, _ := cfg.GetActiveProvider()
	if prov.Name != "" {
		fmt.Printf("│  Active      : %-30s│\n", prov.Name+" ("+prov.Model+")")
	} else {
		fmt.Printf("│  Active      : %-30s│\n", "No provider configured")
	}
	fmt.Printf("│  Auto-Retry  : %-30s│\n", fmt.Sprintf("%t (max %d)", appCfg.AutoRetry, appCfg.RetryMax))
	fmt.Printf("│  Failover    : %-30s│\n", fmt.Sprintf("%t", appCfg.Failover))
	fmt.Println("├──────────────────────────────────────────────┤")
	fmt.Println("│  API         : http://localhost:"+port+"/v1/  │")
	fmt.Println("│  Claude Code : ANTHROPIC_BASE_URL=http://localhost:"+port+"│")
	fmt.Println("└──────────────────────────────────────────────┘")

	// Sync current model to Claude Code's settings.json immediately
	if prov.Model != "" {
		updateClaudeSettings(prov.Model)
	}

	// Register routes
	mux := http.NewServeMux()

	// Anthropic-compatible API
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/models" {
			handleModelsAnthropic(w, r, cfg)
			return
		}
		if r.URL.Path == "/v1/messages" {
			// Record stats wrapper
			start := time.Now()

			// Intercept the response writer to capture status + response body for token extraction
			rw := &statusWriter{ResponseWriter: w, status: 200}
			proxy.HandleMessages(rw, r)

			// After response is complete, parse the full body for token usage
			rw.extractTokens()

			// Log the request with actual latency and token counts
			provider, _ := cfg.GetActiveProvider()
			stats.RecordRequest(RequestLog{
				Timestamp: start,
				Provider:  provider.Name,
				Model:     provider.Model,
				Status:    rw.status,
				Latency:   int(time.Since(start).Milliseconds()),
				InputTok:  rw.InputTok,
				OutputTok: rw.OutputTok,
			})
			return
		}
		http.NotFound(w, r)
	})

	// API endpoints (CORS-wrapped, backward compatible)
	mux.HandleFunc("/api/providers", cors(proxy.HandleAPIProviders))
	mux.HandleFunc("/api/providers/", func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path
		if strings.HasSuffix(path, "/test") {
			proxy.HandleAPITest(w, r)
			return
		}
		proxy.HandleAPIProviders(w, r)
	})
	mux.HandleFunc("/api/switch", cors(proxy.HandleAPISwitch))
	mux.HandleFunc("/api/stats", cors(proxy.HandleAPIStats))
	mux.HandleFunc("/api/logs", cors(proxy.HandleAPILogs))
	mux.HandleFunc("/api/health", cors(proxy.HandleAPIHealth))
	mux.HandleFunc("/api/models", cors(proxy.HandleAPIModels))
	mux.HandleFunc("/api/discover", cors(proxy.HandleAPIDiscover))
	mux.HandleFunc("/api/config", cors(proxy.HandleAPIConfig))
	mux.HandleFunc("/api/debug-log", cors(handleDebugLog))

	log.Fatal(http.ListenAndServe(":"+port, mux))
}

func handleDebugLog(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodPost {
		body, _ := io.ReadAll(r.Body)
		fmt.Printf(" [WEBVIEW DEBUG] %s\n", string(body))
		w.WriteHeader(200)
		return
	}
	w.WriteHeader(405)
}

// statusWriter wraps http.ResponseWriter to capture status code and full response body.
// Token extraction happens after the response is complete (not during streaming).
type statusWriter struct {
	http.ResponseWriter
	status    int
	body      []byte
	InputTok  int
	OutputTok int
}

func (sw *statusWriter) WriteHeader(code int) {
	sw.status = code
	sw.ResponseWriter.WriteHeader(code)
}

func (sw *statusWriter) Write(b []byte) (int, error) {
	sw.body = append(sw.body, b...)
	return sw.ResponseWriter.Write(b)
}

// Flush proxies the Flush call so SSE streaming works correctly.
func (sw *statusWriter) Flush() {
	if f, ok := sw.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// extractTokens parses the accumulated response body after the handler returns.
// Handles two formats:
//  1. Blocking JSON response: {"usage":{"input_tokens":N,"output_tokens":M}}
//  2. SSE stream: scans for "data: {...usage...}" lines
func (sw *statusWriter) extractTokens() {
	body := string(sw.body)

	// Strategy 1: Try as a complete JSON body (blocking/tool responses)
	var resp map[string]interface{}
	if json.Unmarshal(sw.body, &resp) == nil {
		if usage, ok := resp["usage"].(map[string]interface{}); ok {
			if v, ok := usage["input_tokens"].(float64); ok && v > 0 {
				sw.InputTok = int(v)
			}
			if v, ok := usage["output_tokens"].(float64); ok && v > 0 {
				sw.OutputTok = int(v)
			}
		}
		if sw.InputTok > 0 || sw.OutputTok > 0 {
			return
		}
	}

	// Strategy 2: Scan SSE stream lines for usage data
	// Format: data: {"type":"message_delta","usage":{"input_tokens":N,"output_tokens":M}}
	for _, line := range strings.Split(body, "\n") {
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		data := strings.TrimPrefix(line, "data: ")
		var chunk map[string]interface{}
		if json.Unmarshal([]byte(data), &chunk) != nil {
			continue
		}
		if usage, ok := chunk["usage"].(map[string]interface{}); ok {
			if v, ok := usage["input_tokens"].(float64); ok && v > 0 {
				sw.InputTok = int(v)
			}
			if v, ok := usage["output_tokens"].(float64); ok && v > 0 {
				sw.OutputTok = int(v)
			}
		}
		// Also check message_start for input tokens
		if chunk["type"] == "message_start" {
			if msg, ok := chunk["message"].(map[string]interface{}); ok {
				if usage, ok := msg["usage"].(map[string]interface{}); ok {
					if v, ok := usage["input_tokens"].(float64); ok && v > 0 {
						sw.InputTok = int(v)
					}
				}
			}
		}
	}
}

// handleModelsAnthropic returns models in Anthropic format for /v1/models.
func handleModelsAnthropic(w http.ResponseWriter, r *http.Request, cfg *ConfigStore) {
	models := cfg.GetModels()

	// If no models discovered, return placeholder from active provider
	if len(models) == 0 {
		prov, _ := cfg.GetActiveProvider()
		if prov.Model != "" {
			models = []ModelInfo{{
				ID:          prov.Model,
				Provider:    prov.Name,
				DisplayName: prov.Model,
			}}
		}
	}

	data := make([]map[string]interface{}, len(models))
	for i, m := range models {
		name := m.DisplayName
		if name == "" {
			name = m.ID
		}
		data[i] = map[string]interface{}{
			"id":           m.ID,
			"object":       "model",
			"display_name": name,
			"created":      m.Created,
			"owned_by":     m.OwnedBy,
		}
	}

	// Also include Anthropic aliases so Claude Code sees familiar models
	prov, _ := cfg.GetActiveProvider()
	if prov.Model != "" {
		data = append(data,
			map[string]interface{}{"id": "claude-3-opus-20240229", "object": "model", "display_name": "Proxy → " + prov.Model + " (Opus)"},
			map[string]interface{}{"id": "claude-3-5-sonnet-20241022", "object": "model", "display_name": "Proxy → " + prov.Model + " (Sonnet)"},
			map[string]interface{}{"id": "claude-3-5-haiku-20241022", "object": "model", "display_name": "Proxy → " + prov.Model + " (Haiku)"},
		)
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"object": "list",
		"data":   data,
	})
}

// cors adds CORS headers to a handler.
func cors(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Headers", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
		if r.Method == http.MethodOptions {
			w.WriteHeader(204)
			return
		}
		next(w, r)
	}
}
