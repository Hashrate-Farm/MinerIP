package main

import (
	"context"
	"crypto/tls"
	"embed"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"runtime"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
)

//go:embed web/*
var webFS embed.FS

// ───── Miner profile definitions ─────

type MinerProfile struct {
	Brand     string   `json:"brand"`
	Ports     []int    `json:"ports"`
	Endpoints []string `json:"endpoints"`
	Icon      string   `json:"icon"`
}

var minerProfiles = map[string]MinerProfile{
	"antminer": {
		Brand:     "Antminer (Bitmain)",
		Ports:     []int{80, 443, 4028},
		Endpoints: []string{"/cgi-bin/get_system_info.cgi", "/cgi-bin/stats.cgi"},
		Icon:      "A",
	},
	"whatsminer": {
		Brand:     "Whatsminer (MicroBT)",
		Ports:     []int{80, 443, 4028},
		Endpoints: []string{"/"},
		Icon:      "W",
	},
	"iceriver": {
		Brand:     "IceRiver",
		Ports:     []int{80, 443},
		Endpoints: []string{"/user/login", "/"},
		Icon:      "I",
	},
	"bitaxe": {
		Brand:     "Bitaxe",
		Ports:     []int{80},
		Endpoints: []string{"/api/system/info"},
		Icon:      "B",
	},
	"goldshell": {
		Brand:     "Goldshell",
		Ports:     []int{80, 443},
		Endpoints: []string{"/user/login", "/mcb/pools"},
		Icon:      "G",
	},
	"vnish": {
		Brand:     "VNish Firmware",
		Ports:     []int{80, 443},
		Endpoints: []string{"/api/v1/info"},
		Icon:      "V",
	},
	"braiins": {
		Brand:     "Braiins OS+",
		Ports:     []int{80, 443, 4028},
		Endpoints: []string{"/cgi-bin/luci/"},
		Icon:      "Br",
	},
	"luxos": {
		Brand:     "LuxOS",
		Ports:     []int{80, 4028},
		Endpoints: []string{"/"},
		Icon:      "L",
	},
}

// ───── Data types ─────

type DiscoveredMiner struct {
	IP           string         `json:"ip"`
	Port         int            `json:"port"`
	OpenPorts    []int          `json:"openPorts"`
	Brand        string         `json:"brand"`
	Model        string         `json:"model,omitempty"`
	Status       string         `json:"status"`
	ResponseMs   int64          `json:"responseMs"`
	Details      map[string]any `json:"details,omitempty"`
	DashboardURL string         `json:"dashboardUrl"`
}

type ScanRequest struct {
	Subnet     string `json:"subnet"`
	RangeStart int    `json:"rangeStart"`
	RangeEnd   int    `json:"rangeEnd"`
}

type ScanStatus struct {
	Phase    string            `json:"phase"`
	Scanned  int               `json:"scanned"`
	Total    int               `json:"total"`
	Found    int               `json:"found"`
	Miners   []DiscoveredMiner `json:"miners"`
	Complete bool              `json:"complete"`
}

// ───── Global state ─────

var (
	scanMu     sync.Mutex
	scanStatus ScanStatus
	scanCancel context.CancelFunc

	lastActivity   time.Time
	lastActivityMu sync.Mutex
)

func touchActivity() {
	lastActivityMu.Lock()
	lastActivity = time.Now()
	lastActivityMu.Unlock()
}

func main() {
	// Kill any previous instance stuck on the same port
	killExistingInstance()

	mux := http.NewServeMux()

	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		touchActivity()
		if r.URL.Path != "/" {
			data, err := webFS.ReadFile("web" + r.URL.Path)
			if err != nil {
				http.NotFound(w, r)
				return
			}
			ct := "application/octet-stream"
			switch {
			case strings.HasSuffix(r.URL.Path, ".css"):
				ct = "text/css"
			case strings.HasSuffix(r.URL.Path, ".js"):
				ct = "application/javascript"
			case strings.HasSuffix(r.URL.Path, ".png"):
				ct = "image/png"
			case strings.HasSuffix(r.URL.Path, ".ico"):
				ct = "image/x-icon"
			case strings.HasSuffix(r.URL.Path, ".svg"):
				ct = "image/svg+xml"
			}
			w.Header().Set("Content-Type", ct)
			w.Write(data)
			return
		}
		data, _ := webFS.ReadFile("web/index.html")
		w.Header().Set("Content-Type", "text/html")
		w.Write(data)
	})

	mux.HandleFunc("/api/scan", handleScan)
	mux.HandleFunc("/api/status", handleStatus)
	mux.HandleFunc("/api/stop", handleStop)
	mux.HandleFunc("/api/detect-ip", handleDetectIP)
	mux.HandleFunc("/api/shutdown", handleShutdown)
	mux.HandleFunc("/api/ping", handlePing)

	port := 5080
	addr := fmt.Sprintf("127.0.0.1:%d", port)
	url := fmt.Sprintf("http://%s", addr)

	listener, err := net.Listen("tcp", addr)
	if err != nil {
		fmt.Printf("[MinerIP] Port %d is busy — another instance may be running.\n", port)
		fmt.Println("[MinerIP] Opening dashboard in existing instance...")
		openBrowser(url)
		os.Exit(0)
	}

	touchActivity()

	fmt.Println()
	fmt.Println("  ╔═══════════════════════════════════════════════╗")
	fmt.Println("  ║       MinerIP — ASIC Miner Network Scanner   ║")
	fmt.Println("  ║       by Hashrate Farm                       ║")
	fmt.Println("  ╠═══════════════════════════════════════════════╣")
	fmt.Printf("  ║  Dashboard: %-34s ║\n", url)
	fmt.Println("  ║  Press Ctrl+C to stop                        ║")
	fmt.Println("  ╚═══════════════════════════════════════════════╝")
	fmt.Println()

	server := &http.Server{Handler: mux}

	// Graceful shutdown on Ctrl+C or SIGTERM
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)

	// Auto-shutdown after 5 minutes of no activity (no browser connected)
	go func() {
		for {
			time.Sleep(30 * time.Second)
			lastActivityMu.Lock()
			idle := time.Since(lastActivity)
			lastActivityMu.Unlock()
			if idle > 5*time.Minute {
				fmt.Println("\n[MinerIP] No activity for 5 minutes. Shutting down...")
				server.Shutdown(context.Background())
				return
			}
		}
	}()

	go func() {
		<-sigChan
		fmt.Println("\n[MinerIP] Shutting down...")
		server.Shutdown(context.Background())
	}()

	go openBrowser(url)

	if err := server.Serve(listener); err != http.ErrServerClosed {
		fmt.Println("[MinerIP] Server error:", err)
	}
	fmt.Println("[MinerIP] Stopped.")
}

func killExistingInstance() {
	conn, err := net.DialTimeout("tcp", "127.0.0.1:5080", 500*time.Millisecond)
	if err != nil {
		return
	}
	conn.Close()
	// Port is in use — try to ask it to shut down via API
	client := &http.Client{Timeout: 2 * time.Second}
	client.Post("http://127.0.0.1:5080/api/shutdown", "application/json", nil)
	time.Sleep(1 * time.Second)
}

func openBrowser(url string) {
	time.Sleep(500 * time.Millisecond)
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	case "darwin":
		cmd = exec.Command("open", url)
	default:
		cmd = exec.Command("xdg-open", url)
	}
	cmd.Start()
}

// ───── API Handlers ─────

func handleDetectIP(w http.ResponseWriter, r *http.Request) {
	touchActivity()
	w.Header().Set("Content-Type", "application/json")
	ip := getLocalIP()
	json.NewEncoder(w).Encode(map[string]string{"ip": ip})
}

func handlePing(w http.ResponseWriter, r *http.Request) {
	touchActivity()
	w.Header().Set("Content-Type", "application/json")
	w.Write([]byte(`{"status":"ok"}`))
}

func handleShutdown(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Write([]byte(`{"status":"shutting_down"}`))
	go func() {
		time.Sleep(200 * time.Millisecond)
		os.Exit(0)
	}()
}

func handleScan(w http.ResponseWriter, r *http.Request) {
	touchActivity()
	if r.Method != http.MethodPost {
		http.Error(w, "POST required", http.StatusMethodNotAllowed)
		return
	}

	var req ScanRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request", http.StatusBadRequest)
		return
	}

	if req.RangeStart < 1 {
		req.RangeStart = 1
	}
	if req.RangeEnd > 254 {
		req.RangeEnd = 254
	}
	if req.RangeEnd < req.RangeStart {
		req.RangeEnd = req.RangeStart
	}

	scanMu.Lock()
	if scanCancel != nil {
		scanCancel()
	}
	scanStatus = ScanStatus{Phase: "Starting scan...", Total: req.RangeEnd - req.RangeStart + 1}
	scanMu.Unlock()

	ctx, cancel := context.WithCancel(context.Background())
	scanMu.Lock()
	scanCancel = cancel
	scanMu.Unlock()

	go runScan(ctx, req)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "started"})
}

func handleStatus(w http.ResponseWriter, r *http.Request) {
	touchActivity()
	scanMu.Lock()
	data, _ := json.Marshal(scanStatus)
	scanMu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	w.Write(data)
}

func handleStop(w http.ResponseWriter, r *http.Request) {
	touchActivity()
	scanMu.Lock()
	if scanCancel != nil {
		scanCancel()
		scanCancel = nil
	}
	scanStatus.Phase = "Scan stopped by user."
	scanStatus.Complete = true
	scanMu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "stopped"})
}

// ───── Scanner engine ─────

func runScan(ctx context.Context, req ScanRequest) {
	base := strings.Join(strings.Split(req.Subnet, ".")[:3], ".")
	allPorts := uniquePorts()
	total := req.RangeEnd - req.RangeStart + 1

	var scanned int64
	var minersMu sync.Mutex
	var miners []DiscoveredMiner

	addMiner := func(m DiscoveredMiner) {
		minersMu.Lock()
		miners = append(miners, m)
		sorted := append([]DiscoveredMiner{}, miners...)
		sort.Slice(sorted, func(i, j int) bool { return sorted[i].IP < sorted[j].IP })
		minersMu.Unlock()

		scanMu.Lock()
		scanStatus.Miners = sorted
		scanStatus.Found = len(sorted)
		scanMu.Unlock()
	}

	updateProgress := func(ip string) {
		cur := atomic.AddInt64(&scanned, 1)
		scanMu.Lock()
		minersMu.Lock()
		found := len(miners)
		minersMu.Unlock()
		scanStatus.Phase = fmt.Sprintf("Scanning %s... (%d/%d)", ip, cur, total)
		scanStatus.Scanned = int(cur)
		scanStatus.Total = total
		scanStatus.Found = found
		scanMu.Unlock()
	}

	semaphore := make(chan struct{}, 50)
	var wg sync.WaitGroup

	for i := req.RangeStart; i <= req.RangeEnd; i++ {
		select {
		case <-ctx.Done():
			goto done
		default:
		}

		ip := fmt.Sprintf("%s.%d", base, i)
		wg.Add(1)
		semaphore <- struct{}{}

		go func(ip string) {
			defer wg.Done()
			defer func() { <-semaphore }()

			var openPorts []int
			var fastest int64 = 999999

			for _, port := range allPorts {
				select {
				case <-ctx.Done():
					return
				default:
				}

				start := time.Now()
				conn, err := net.DialTimeout("tcp", fmt.Sprintf("%s:%d", ip, port), 1500*time.Millisecond)
				elapsed := time.Since(start).Milliseconds()
				if err == nil {
					conn.Close()
					openPorts = append(openPorts, port)
					if elapsed < fastest {
						fastest = elapsed
					}
				}
			}

			updateProgress(ip)

			if len(openPorts) > 0 {
				miner := identifyMiner(ctx, ip, openPorts, fastest)
				addMiner(miner)
			}
		}(ip)
	}

	wg.Wait()

done:
	minersMu.Lock()
	final := append([]DiscoveredMiner{}, miners...)
	sort.Slice(final, func(i, j int) bool { return final[i].IP < final[j].IP })
	minersMu.Unlock()

	scanMu.Lock()
	scanStatus.Miners = final
	scanStatus.Found = len(final)
	scanStatus.Scanned = int(atomic.LoadInt64(&scanned))
	scanStatus.Phase = fmt.Sprintf("Scan complete. Found %d device(s).", len(final))
	scanStatus.Complete = true
	scanMu.Unlock()
}

func identifyMiner(ctx context.Context, ip string, openPorts []int, responseMs int64) DiscoveredMiner {
	miner := DiscoveredMiner{
		IP:         ip,
		Port:       openPorts[0],
		OpenPorts:  openPorts,
		Status:     "unknown",
		ResponseMs: responseMs,
		Details:    make(map[string]any),
	}

	if contains(openPorts, 80) {
		miner.DashboardURL = fmt.Sprintf("http://%s", ip)
		miner.Port = 80
	} else if contains(openPorts, 443) {
		miner.DashboardURL = fmt.Sprintf("https://%s", ip)
		miner.Port = 443
	} else if contains(openPorts, 8080) {
		miner.DashboardURL = fmt.Sprintf("http://%s:8080", ip)
		miner.Port = 8080
	} else {
		miner.DashboardURL = fmt.Sprintf("http://%s:%d", ip, openPorts[0])
	}

	// --- Bitaxe (clean JSON API) ---
	if contains(openPorts, 80) {
		if data, ok := httpGetJSON(ctx, fmt.Sprintf("http://%s/api/system/info", ip)); ok {
			miner.Brand = "Bitaxe"
			miner.Status = "identified"
			miner.Details = data
			if m, ok := data["ASICModel"].(string); ok {
				miner.Model = m
			} else if h, ok := data["hostname"].(string); ok {
				miner.Model = h
			}
			return miner
		}
	}

	// --- VNish Firmware ---
	if contains(openPorts, 80) || contains(openPorts, 443) {
		scheme := "http"
		if !contains(openPorts, 80) {
			scheme = "https"
		}
		if data, ok := httpGetJSON(ctx, fmt.Sprintf("%s://%s/api/v1/info", scheme, ip)); ok {
			miner.Brand = "VNish Firmware"
			miner.Status = "identified"
			if m, ok := data["model"].(string); ok {
				miner.Model = m
			}
			extractVNishStats(ctx, scheme, ip, miner.Details)
			for k, v := range data {
				if _, exists := miner.Details[k]; !exists {
					miner.Details[k] = v
				}
			}
			return miner
		}
	}

	// --- Antminer CGI endpoint ---
	if contains(openPorts, 80) {
		if body, ok := httpGetBody(ctx, fmt.Sprintf("http://%s/cgi-bin/get_system_info.cgi", ip)); ok {
			if strings.Contains(strings.ToLower(body), "antminer") || strings.Contains(strings.ToLower(body), "minertype") {
				miner.Brand = "Antminer (Bitmain)"
				miner.Status = "identified"
				var data map[string]any
				if json.Unmarshal([]byte(body), &data) == nil {
					miner.Details = data
					if m, ok := data["minertype"].(string); ok {
						miner.Model = m
					}
				}
				if contains(openPorts, 4028) {
					enrichFromCGMiner(ctx, ip, miner.Details)
				}
				return miner
			}
		}
	}

	// --- Web page brand detection ---
	if contains(openPorts, 80) {
		if body, ok := httpGetBody(ctx, fmt.Sprintf("http://%s/", ip)); ok {
			bodyLower := strings.ToLower(body)
			switch {
			case strings.Contains(bodyLower, "iceriver"):
				miner.Brand = "IceRiver"
				miner.Status = "identified"
			case strings.Contains(bodyLower, "goldshell"):
				miner.Brand = "Goldshell"
				miner.Status = "identified"
			case strings.Contains(bodyLower, "antminer") || strings.Contains(bodyLower, "bitmain"):
				miner.Brand = "Antminer (Bitmain)"
				miner.Status = "identified"
			case strings.Contains(bodyLower, "whatsminer") || strings.Contains(bodyLower, "microbt"):
				miner.Brand = "Whatsminer (MicroBT)"
				miner.Status = "identified"
			case strings.Contains(bodyLower, "braiins") || strings.Contains(bodyLower, "bos"):
				miner.Brand = "Braiins OS+"
				miner.Status = "identified"
			case strings.Contains(bodyLower, "luxos"):
				miner.Brand = "LuxOS"
				miner.Status = "identified"
			}
			if miner.Status == "identified" {
				if contains(openPorts, 4028) {
					enrichFromCGMiner(ctx, ip, miner.Details)
				}
				return miner
			}
		}
	}

	// --- CGMiner API on port 4028 (fallback) ---
	if contains(openPorts, 4028) {
		if resp := cgminerCommand(ctx, ip, `{"command":"version"}`); resp != "" {
			respLower := strings.ToLower(resp)
			miner.Status = "identified"
			switch {
			case strings.Contains(respLower, "antminer") || strings.Contains(respLower, "bmminer"):
				miner.Brand = "Antminer (Bitmain)"
			case strings.Contains(respLower, "cgminer"):
				miner.Brand = "Miner (CGMiner API)"
			default:
				miner.Brand = "Miner (API port 4028)"
			}
			enrichFromCGMiner(ctx, ip, miner.Details)
			return miner
		}
	}

	if contains(openPorts, 80) || contains(openPorts, 443) {
		miner.Status = "alive"
		miner.Brand = "Unknown Device - Maybe Not a Miner"
	}

	return miner
}

// enrichFromCGMiner pulls hashrate and temperature from the CGMiner/BMMiner stats API on port 4028
func enrichFromCGMiner(ctx context.Context, ip string, details map[string]any) {
	if resp := cgminerCommand(ctx, ip, `{"command":"summary"}`); resp != "" {
		var result map[string]any
		if json.Unmarshal([]byte(resp), &result) == nil {
			if summaryArr, ok := result["SUMMARY"].([]any); ok && len(summaryArr) > 0 {
				if summary, ok := summaryArr[0].(map[string]any); ok {
					if hr, ok := toFloat(summary["GHS 5s"]); ok && hr > 0 {
						details["hashRate"] = fmt.Sprintf("%.2f GH/s", hr)
					} else if hr, ok := toFloat(summary["GHS av"]); ok && hr > 0 {
						details["hashRate"] = fmt.Sprintf("%.2f GH/s", hr)
					} else if hr, ok := toFloat(summary["MHS 5s"]); ok && hr > 0 {
						details["hashRate"] = fmt.Sprintf("%.2f MH/s", hr)
					} else if hr, ok := toFloat(summary["MHS av"]); ok && hr > 0 {
						details["hashRate"] = fmt.Sprintf("%.2f MH/s", hr)
					}
					if rej, ok := toFloat(summary["Hardware Errors"]); ok {
						details["hwErrors"] = fmt.Sprintf("%.0f", rej)
					}
				}
			}
		}
	}

	if resp := cgminerCommand(ctx, ip, `{"command":"stats"}`); resp != "" {
		var result map[string]any
		if json.Unmarshal([]byte(resp), &result) == nil {
			if statsArr, ok := result["STATS"].([]any); ok {
				for _, item := range statsArr {
					stat, ok := item.(map[string]any)
					if !ok {
						continue
					}
					// Temperature: look for temp, temp1, temp2_6, temp_chip, etc.
					for _, key := range []string{"temp", "temp1", "temp2_6", "temp_chip", "Temperature"} {
						if t, ok := toFloat(stat[key]); ok && t > 0 {
							details["temp"] = fmt.Sprintf("%.1f", t)
							break
						}
					}
					// Fan speed
					for _, key := range []string{"fan1", "fan_speed_in", "Fan Speed In"} {
						if f, ok := toFloat(stat[key]); ok && f > 0 {
							details["fan"] = fmt.Sprintf("%.0f RPM", f)
							break
						}
					}
					// Power (some firmwares report it)
					for _, key := range []string{"Power", "power", "total_power"} {
						if p, ok := toFloat(stat[key]); ok && p > 0 {
							details["power"] = fmt.Sprintf("%.0fW", p)
							break
						}
					}
					// Uptime
					if up, ok := toFloat(stat["Elapsed"]); ok && up > 0 {
						hours := up / 3600
						details["uptime"] = fmt.Sprintf("%.1fh", hours)
					}
				}
			}
		}
	}
}

// extractVNishStats pulls hashrate/temp from VNish's summary endpoint
func extractVNishStats(ctx context.Context, scheme, ip string, details map[string]any) {
	if data, ok := httpGetJSON(ctx, fmt.Sprintf("%s://%s/api/v1/summary", scheme, ip)); ok {
		if hr, ok := toFloat(data["hashrate"]); ok && hr > 0 {
			if hr > 1e12 {
				details["hashRate"] = fmt.Sprintf("%.2f TH/s", hr/1e12)
			} else if hr > 1e9 {
				details["hashRate"] = fmt.Sprintf("%.2f GH/s", hr/1e9)
			} else {
				details["hashRate"] = fmt.Sprintf("%.2f MH/s", hr/1e6)
			}
		} else if hrs, ok := data["hashrate"].(string); ok && hrs != "" {
			details["hashRate"] = hrs
		}
		if t, ok := toFloat(data["temperature"]); ok && t > 0 {
			details["temp"] = fmt.Sprintf("%.1f", t)
		}
		if p, ok := toFloat(data["power"]); ok && p > 0 {
			details["power"] = fmt.Sprintf("%.0fW", p)
		}
		if f, ok := toFloat(data["fan_speed"]); ok && f > 0 {
			details["fan"] = fmt.Sprintf("%.0f RPM", f)
		}
	}
	// Also try /api/v1/status for more VNish data
	if data, ok := httpGetJSON(ctx, fmt.Sprintf("%s://%s/api/v1/status", scheme, ip)); ok {
		if _, exists := details["hashRate"]; !exists {
			if hr, ok := toFloat(data["hashrate"]); ok && hr > 0 {
				if hr > 1e12 {
					details["hashRate"] = fmt.Sprintf("%.2f TH/s", hr/1e12)
				} else if hr > 1e9 {
					details["hashRate"] = fmt.Sprintf("%.2f GH/s", hr/1e9)
				} else {
					details["hashRate"] = fmt.Sprintf("%.2f MH/s", hr/1e6)
				}
			}
		}
		if _, exists := details["temp"]; !exists {
			if t, ok := toFloat(data["temperature"]); ok && t > 0 {
				details["temp"] = fmt.Sprintf("%.1f", t)
			}
		}
		// Chain/board temps
		if chains, ok := data["chains"].([]any); ok {
			for i, c := range chains {
				if chain, ok := c.(map[string]any); ok {
					if t, ok := toFloat(chain["temp_chip"]); ok && t > 0 {
						details[fmt.Sprintf("chain%d_temp", i)] = fmt.Sprintf("%.1f°C", t)
					}
				}
			}
		}
	}
}

func toFloat(v any) (float64, bool) {
	switch val := v.(type) {
	case float64:
		return val, true
	case float32:
		return float64(val), true
	case int:
		return float64(val), true
	case int64:
		return float64(val), true
	case json.Number:
		f, err := val.Float64()
		return f, err == nil
	}
	return 0, false
}

// ───── Helpers ─────

func getLocalIP() string {
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return ""
	}
	for _, addr := range addrs {
		if ipNet, ok := addr.(*net.IPNet); ok && !ipNet.IP.IsLoopback() && ipNet.IP.To4() != nil {
			ip := ipNet.IP.String()
			if strings.HasPrefix(ip, "192.168.") || strings.HasPrefix(ip, "10.") || strings.HasPrefix(ip, "172.") {
				return ip
			}
		}
	}
	return ""
}

func uniquePorts() []int {
	portSet := make(map[int]bool)
	for _, p := range minerProfiles {
		for _, port := range p.Ports {
			portSet[port] = true
		}
	}
	portSet[80] = true
	portSet[443] = true
	portSet[8080] = true
	portSet[4028] = true

	ports := make([]int, 0, len(portSet))
	for p := range portSet {
		ports = append(ports, p)
	}
	sort.Ints(ports)
	return ports
}

func contains(slice []int, val int) bool {
	for _, v := range slice {
		if v == val {
			return true
		}
	}
	return false
}

func httpGetJSON(ctx context.Context, url string) (map[string]any, bool) {
	client := &http.Client{
		Timeout: 3 * time.Second,
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
		},
	}
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, false
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, false
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
	if err != nil {
		return nil, false
	}
	var data map[string]any
	if json.Unmarshal(body, &data) != nil {
		return nil, false
	}
	return data, true
}

func httpGetBody(ctx context.Context, url string) (string, bool) {
	client := &http.Client{
		Timeout: 3 * time.Second,
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
		},
	}
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return "", false
	}
	resp, err := client.Do(req)
	if err != nil {
		return "", false
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 128*1024))
	if err != nil {
		return "", false
	}
	return string(body), true
}

func cgminerCommand(ctx context.Context, ip, cmd string) string {
	d := net.Dialer{Timeout: 2 * time.Second}
	conn, err := d.DialContext(ctx, "tcp", fmt.Sprintf("%s:4028", ip))
	if err != nil {
		return ""
	}
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(3 * time.Second))
	_, err = conn.Write([]byte(cmd))
	if err != nil {
		return ""
	}
	buf := make([]byte, 8192)
	n, err := conn.Read(buf)
	if err != nil && n == 0 {
		return ""
	}
	return strings.TrimRight(string(buf[:n]), "\x00")
}
