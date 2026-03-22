package demo

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"math/rand"
	"net/http"
	"time"

	"vuek8/internal/cluster"
	"vuek8/internal/config"
	"vuek8/internal/kube"
	"vuek8/internal/update"
	"vuek8/internal/web"
)

func NewServer() *http.Server {
	mux := http.NewServeMux()

	clusters := buildClusters()
	namespaces := buildNamespaces()
	nodes := buildNodes()
	pods := buildPods()
	metrics := buildMetrics(pods)

	jsonHandler := func(v any) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(v)
		}
	}
	okHandler := func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"ok":true}`))
	}

	mux.HandleFunc("/api/clusters", jsonHandler(clusters))
	mux.HandleFunc("/api/namespaces", jsonHandler(namespaces))
	mux.HandleFunc("/api/nodes", jsonHandler(nodes))
	mux.HandleFunc("/api/pods", jsonHandler(pods))
	mux.HandleFunc("/api/metrics", jsonHandler(metrics))
	mux.HandleFunc("/api/progress", jsonHandler(kube.Progress{Ready: true}))
	mux.HandleFunc("/api/version", update.HandleVersion)

	mux.HandleFunc("/api/clusters/switch", okHandler)
	mux.HandleFunc("/api/clusters/rename", okHandler)
	mux.HandleFunc("/api/clusters/hide", okHandler)
	mux.HandleFunc("/api/settings", jsonHandler(config.Settings{}))
	mux.HandleFunc("/api/settings/update", okHandler)
	fakeLogText := buildFakeLogText()
	mux.HandleFunc("/api/logs", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		w.Write([]byte(fakeLogText))
	})
	mux.HandleFunc("/api/logs/stream", handleFakeLogs)
	mux.HandleFunc("/api/logs/download", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(204)
	})

	if web.DevMode {
		mux.Handle("/", http.FileServer(http.Dir("internal/web/static")))
	} else {
		staticFS, _ := fs.Sub(web.StaticFiles, "static")
		mux.Handle("/", http.FileServer(http.FS(staticFS)))
	}

	return &http.Server{Handler: mux}
}

// --- deterministic pseudo-random ---

type rng struct{ s uint64 }

func newRng(seed uint64) *rng { return &rng{s: seed} }

func (r *rng) next() uint64 {
	r.s = r.s*6364136223846793005 + 1442695040888963407
	return r.s >> 33
}

// intn returns a value in [0, n)
func (r *rng) intn(n int) int { return int(r.next() % uint64(n)) }

// between returns a value in [lo, hi] inclusive
func (r *rng) between(lo, hi int) int { return lo + r.intn(hi-lo+1) }

func (r *rng) hex(n int) string {
	const chars = "0123456789abcdef"
	b := make([]byte, n)
	for i := range b {
		b[i] = chars[r.intn(16)]
	}
	return string(b)
}

// --- clusters ---

func buildClusters() []cluster.ClusterInfo {
	return []cluster.ClusterInfo{
		{
			ID: "prod-eu", DisplayName: "arctis-prod-eu",
			ContextName: "arctis-prod-eu", Server: "https://k8s.eu.arctis.io:6443",
			FilePath: "/home/deploy/.kube/arctis-prod.yaml", Active: true, IsDefault: true,
		},
		{
			ID: "prod-us", DisplayName: "arctis-prod-us",
			ContextName: "arctis-prod-us", Server: "https://k8s.us.arctis.io:6443",
			FilePath: "/home/deploy/.kube/arctis-prod.yaml",
		},
		{
			ID: "staging", DisplayName: "arctis-staging",
			ContextName: "arctis-staging", Server: "https://k8s.staging.arctis.io:6443",
			FilePath: "/home/deploy/.kube/arctis-staging.yaml", IsDefault: true,
		},
	}
}

func buildNamespaces() []string {
	return []string{"default", "kube-system", "monitoring"}
}

// --- nodes ---

func buildNodes() []kube.NodeInfo {
	var nodes []kube.NodeInfo

	mkNode := func(name, ip, pool, cpu, mem string) kube.NodeInfo {
		return kube.NodeInfo{
			Name: name, IP: ip, Status: "Ready", Roles: "worker",
			KubeletVersion: "v1.31.2", OS: "linux", Arch: "amd64",
			CPUCapacity: cpu, MemoryCapacity: mem,
			Labels: map[string]string{
				"node-pool":                        pool,
				"topology.kubernetes.io/zone":       "eu-west-1a",
				"node.kubernetes.io/instance-type":  "bare-metal",
			},
		}
	}

	// Brokers: 4 nodes, 8 CPU · 62.8Gi
	for i := 1; i <= 4; i++ {
		nodes = append(nodes, mkNode(
			fmt.Sprintf("brokers-%d", i),
			fmt.Sprintf("10.0.1.%d", 10+i),
			"brokers", "8", "62.8Gi",
		))
	}

	// General: 13 nodes, 40 CPU · 188.8Gi
	for i := 1; i <= 13; i++ {
		nodes = append(nodes, mkNode(
			fmt.Sprintf("general-%d", i),
			fmt.Sprintf("10.0.2.%d", 10+i),
			"general", "40", "188.8Gi",
		))
	}

	// Workers: 15 nodes, 40 CPU · 188.8Gi
	for i := 1; i <= 15; i++ {
		nodes = append(nodes, mkNode(
			fmt.Sprintf("workers-%d", i),
			fmt.Sprintf("10.0.3.%d", 10+i),
			"workers", "40", "188.8Gi",
		))
	}

	return nodes
}

// --- pods ---

// Service workload templates.
type workload struct {
	name      string
	image     string
	tag       string
	kind      string // Deployment or StatefulSet
	cpuReq    int64
	cpuLim    int64
	memReqMi  int64
	memLimMi  int64
}

// Broker workloads (6 pods per broker node).
var brokerWorkloads = []workload{
	{"nats-streaming", "nats-streaming", "0.25.6", "StatefulSet", 500, 2000, 1024, 4096},
	{"kafka", "bitnami/kafka", "3.8.0", "StatefulSet", 1000, 3000, 2048, 8192},
	{"zookeeper", "bitnami/zookeeper", "3.9.2", "StatefulSet", 200, 500, 512, 1024},
	{"rabbitmq", "bitnami/rabbitmq", "3.13.7", "StatefulSet", 500, 1500, 1024, 4096},
	{"event-router", "arctis/event-router", "2.4.1", "Deployment", 200, 500, 256, 512},
	{"schema-registry", "confluentinc/schema-registry", "7.7.1", "Deployment", 300, 800, 512, 1024},
}

// General-pool service names (for generating varied workloads).
var generalServices = []workload{
	{"api-gateway", "arctis/api-gateway", "2.14.0", "Deployment", 200, 500, 256, 512},
	{"auth-service", "arctis/auth-service", "1.8.3", "Deployment", 150, 400, 192, 384},
	{"user-service", "arctis/user-service", "3.1.0", "Deployment", 150, 400, 192, 384},
	{"session-manager", "arctis/session-manager", "1.2.1", "Deployment", 100, 300, 128, 256},
	{"notification-worker", "arctis/notification-worker", "1.5.0", "Deployment", 200, 500, 256, 512},
	{"payment-processor", "arctis/payment-processor", "3.0.4", "Deployment", 200, 500, 256, 512},
	{"catalog-api", "arctis/catalog-api", "4.7.2", "Deployment", 250, 600, 384, 768},
	{"search-indexer", "arctis/search-indexer", "2.3.1", "Deployment", 400, 1000, 512, 1024},
	{"recommendation-engine", "arctis/recommendation-engine", "1.9.7", "Deployment", 500, 1500, 1024, 2048},
	{"event-bus", "arctis/event-bus", "1.3.2", "Deployment", 150, 400, 192, 384},
	{"order-service", "arctis/order-service", "5.2.1", "Deployment", 200, 500, 256, 512},
	{"inventory-sync", "arctis/inventory-sync", "1.6.0", "Deployment", 300, 700, 384, 768},
	{"email-sender", "arctis/email-sender", "2.0.3", "Deployment", 100, 250, 128, 256},
	{"webhook-dispatcher", "arctis/webhook-dispatcher", "1.4.1", "Deployment", 150, 400, 192, 384},
	{"media-processor", "arctis/media-processor", "3.2.0", "Deployment", 500, 1500, 512, 2048},
	{"pdf-generator", "arctis/pdf-generator", "1.1.0", "Deployment", 200, 500, 256, 512},
	{"analytics-pipeline", "arctis/analytics-pipeline", "2.1.0", "Deployment", 400, 800, 512, 1024},
	{"feature-flags", "arctis/feature-flags", "1.0.4", "Deployment", 100, 200, 128, 256},
	{"rate-limiter", "arctis/rate-limiter", "1.1.2", "Deployment", 100, 250, 128, 256},
	{"cdn-origin", "arctis/cdn-origin", "2.7.0", "Deployment", 200, 500, 256, 512},
	{"image-resizer", "arctis/image-resizer", "1.3.0", "Deployment", 300, 800, 256, 1024},
	{"audit-logger", "arctis/audit-logger", "1.3.5", "Deployment", 100, 300, 128, 256},
	{"billing-service", "arctis/billing-service", "4.0.2", "Deployment", 200, 500, 256, 512},
	{"subscription-manager", "arctis/subscription-manager", "2.2.0", "Deployment", 150, 400, 192, 384},
	{"permission-service", "arctis/permission-service", "2.4.0", "Deployment", 100, 300, 128, 256},
	{"config-service", "arctis/config-service", "1.0.1", "Deployment", 50, 150, 64, 128},
	{"healthcheck-worker", "arctis/healthcheck-worker", "1.2.0", "Deployment", 50, 150, 64, 128},
	{"scheduler", "arctis/scheduler", "3.5.0", "Deployment", 200, 500, 256, 512},
	{"stream-processor", "arctis/stream-processor", "2.8.0", "Deployment", 400, 1000, 512, 1024},
	{"cache-warmer", "arctis/cache-warmer", "1.0.0", "Deployment", 100, 300, 128, 256},
	{"graphql-gateway", "arctis/graphql-gateway", "2.1.0", "Deployment", 300, 800, 384, 768},
	{"websocket-hub", "arctis/websocket-hub", "1.4.0", "Deployment", 200, 500, 256, 512},
	{"file-storage", "arctis/file-storage", "2.0.1", "Deployment", 150, 400, 192, 512},
	{"export-worker", "arctis/export-worker", "1.1.3", "Deployment", 200, 500, 256, 512},
	{"import-worker", "arctis/import-worker", "1.2.0", "Deployment", 200, 500, 256, 512},
	{"cron-dispatcher", "arctis/cron-dispatcher", "1.0.5", "Deployment", 100, 250, 128, 256},
	{"geo-service", "arctis/geo-service", "1.5.0", "Deployment", 100, 300, 128, 256},
	{"translation-api", "arctis/translation-api", "2.0.0", "Deployment", 200, 500, 256, 512},
	{"ab-testing", "arctis/ab-testing", "1.0.2", "Deployment", 50, 150, 64, 128},
	{"push-notifier", "arctis/push-notifier", "1.3.1", "Deployment", 150, 400, 192, 384},
}

// Worker-pool workloads.
var workerServices = []workload{
	{"postgres", "bitnami/postgresql", "16.2.0", "StatefulSet", 500, 2000, 1024, 4096},
	{"redis-cluster", "bitnami/redis", "7.4.1", "StatefulSet", 200, 500, 512, 2048},
	{"elasticsearch", "bitnami/elasticsearch", "8.16.0", "StatefulSet", 500, 2000, 2048, 4096},
	{"clickhouse", "clickhouse/clickhouse-server", "24.8", "StatefulSet", 500, 2000, 1024, 4096},
	{"mongo", "bitnami/mongodb", "7.0.14", "StatefulSet", 500, 2000, 1024, 4096},
	{"minio", "minio/minio", "2024.10", "StatefulSet", 200, 500, 512, 2048},
	{"pgbouncer", "bitnami/pgbouncer", "1.23.0", "Deployment", 100, 300, 64, 128},
	{"redis-exporter", "oliver006/redis-exporter", "1.63.0", "Deployment", 50, 100, 32, 64},
	{"backup-agent", "arctis/backup-agent", "1.2.0", "Deployment", 100, 300, 128, 256},
	{"replication-manager", "arctis/replication-manager", "1.0.3", "Deployment", 100, 300, 128, 256},
}

var ages = []string{"1d", "2d", "3d", "5d", "7d", "8d", "10d", "12d", "14d", "18d", "21d", "28d", "30d"}

func buildPods() []kube.PodInfo {
	r := newRng(42)
	var pods []kube.PodInfo

	makePod := func(w workload, node string, idx int) kube.PodInfo {
		var podName string
		if w.kind == "StatefulSet" {
			podName = fmt.Sprintf("%s-%d", w.name, idx)
		} else {
			podName = fmt.Sprintf("%s-%s-%s", w.name, r.hex(5), r.hex(5))
		}
		return kube.PodInfo{
			Name:            podName,
			Namespace:       "default",
			Status:          "Running",
			Ready:           "1/1",
			Restarts:        0,
			CPURequestMilli: w.cpuReq,
			CPULimitMilli:   w.cpuLim,
			MemRequestBytes: w.memReqMi * 1024 * 1024,
			MemLimitBytes:   w.memLimMi * 1024 * 1024,
			Age:             ages[r.intn(len(ages))],
			Node:            node,
			Containers:      []kube.ContainerInfo{{Name: w.name, Image: w.image, Tag: w.tag, Status: "Running"}},
			WorkloadName:    w.name,
			WorkloadKind:    w.kind,
		}
	}

	// === Brokers: 4 nodes × 6 pods each = 24 pods ===
	for i := 1; i <= 4; i++ {
		node := fmt.Sprintf("brokers-%d", i)
		for j, w := range brokerWorkloads {
			pods = append(pods, makePod(w, node, i-1+j))
		}
	}

	// === General: 13 nodes, 50–100 pods each ===
	generalNodeNames := make([]string, 13)
	for i := 0; i < 13; i++ {
		generalNodeNames[i] = fmt.Sprintf("general-%d", i+1)
	}
	svcIdx := 0
	for i := 0; i < 13; i++ {
		node := generalNodeNames[i]
		podCount := r.between(50, 100)
		for j := 0; j < podCount; j++ {
			w := generalServices[svcIdx%len(generalServices)]
			svcIdx++
			pods = append(pods, makePod(w, node, j))
		}
	}

	// === Workers: 15 nodes, 12–15 pods each ===
	workerNodeNames := make([]string, 15)
	for i := 0; i < 15; i++ {
		workerNodeNames[i] = fmt.Sprintf("workers-%d", i+1)
	}
	wsvcIdx := 0
	for i := 0; i < 15; i++ {
		node := workerNodeNames[i]
		podCount := r.between(12, 15)
		for j := 0; j < podCount; j++ {
			w := workerServices[wsvcIdx%len(workerServices)]
			wsvcIdx++
			pods = append(pods, makePod(w, node, j))
		}
	}

	// === Apply ~10% red status with clustering ===
	// Mark some pods as unhealthy in grapes (groups of 1, 3, 4, or 4–8).
	totalPods := len(pods)
	targetRed := totalPods / 10 // ~10%
	redCount := 0
	redStatuses := []string{"CrashLoopBackOff", "Error", "ImagePullBackOff"}

	i := 0
	for i < totalPods && redCount < targetRed {
		// Decide next grape size
		roll := r.intn(10)
		var grapeSize int
		switch {
		case roll < 3: // 30% chance: single pod
			grapeSize = 1
		case roll < 5: // 20% chance: grape of 3
			grapeSize = 3
		case roll < 7: // 20% chance: grape of 4
			grapeSize = 4
		default: // 30% chance: grape of 4–8
			grapeSize = r.between(4, 8)
		}
		if redCount+grapeSize > targetRed {
			grapeSize = targetRed - redCount
		}

		// Pick a random starting position
		start := r.intn(totalPods)
		status := redStatuses[r.intn(len(redStatuses))]
		for g := 0; g < grapeSize; g++ {
			idx := (start + g) % totalPods
			if pods[idx].Status == "Running" {
				pods[idx].Status = status
				pods[idx].Ready = "0/1"
				pods[idx].Containers[0].Status = "Waiting"
				if status == "CrashLoopBackOff" {
					pods[idx].Restarts = int32(r.between(10, 150))
				}
				redCount++
			}
		}
		i++
	}

	return pods
}

// --- metrics ---

var hotCPUPods = map[string]bool{
	"analytics-pipeline": true,
	"stream-processor":   true,
}

func buildMetrics(pods []kube.PodInfo) []kube.PodMetrics {
	r := newRng(7777)
	var metrics []kube.PodMetrics
	hotUsed := 0
	for _, p := range pods {
		if p.Status != "Running" {
			continue
		}
		// Wide CPU spread: 5–95% of request → shows full color spectrum
		cpuPct := r.between(5, 95)
		// Wide memory spread: 10–90% of request
		memPct := r.between(10, 90)

		cpuMilli := p.CPURequestMilli * int64(cpuPct) / 100

		// A few pods get red CPU (computed against limit, which is what the UI uses)
		if hotCPUPods[p.WorkloadName] && hotUsed < 2 {
			cpuMilli = p.CPULimitMilli * 92 / 100
			hotUsed++
		}
		if p.WorkloadName == "billing-service" && p.Node == "general-10" && hotUsed >= 2 {
			cpuMilli = p.CPULimitMilli * 89 / 100
		}
		memBytes := p.MemRequestBytes * int64(memPct) / 100

		metrics = append(metrics, kube.PodMetrics{
			Name:      p.Name,
			Namespace: p.Namespace,
			Containers: []kube.ContainerMetrics{
				{
					Name:     p.Containers[0].Name,
					CPUNano:  cpuMilli * 1_000_000,
					CPUMilli: cpuMilli,
					MemBytes: memBytes,
				},
			},
		})
	}
	return metrics
}

// --- fake logs ---

func buildFakeLogText() string {
	r := newRng(9999)
	baseTime := time.Date(2026, 3, 22, 14, 30, 0, 0, time.UTC)
	lines := make([]string, 30)
	for i := range lines {
		ts := baseTime.Add(time.Duration(i) * 347 * time.Millisecond).Format(time.RFC3339Nano)
		switch r.intn(15) {
		case 0:
			lines[i] = fmt.Sprintf(`{"level":"info","ts":"%s","msg":"request completed","method":"GET","path":"/api/v1/health","status":200,"latency_ms":%d}`, ts, r.between(1, 45))
		case 1:
			lines[i] = fmt.Sprintf(`{"level":"info","ts":"%s","msg":"processed event","event_type":"user.updated","duration_ms":%d,"queue_depth":%d}`, ts, r.between(5, 200), r.between(0, 80))
		case 2:
			lines[i] = fmt.Sprintf(`{"level":"info","ts":"%s","msg":"cache hit","key":"session:%s","ttl_remaining":%d}`, ts, r.hex(8), r.between(60, 3600))
		case 3:
			a := r.between(5, 40)
			lines[i] = fmt.Sprintf(`{"level":"debug","ts":"%s","msg":"connection pool stats","active":%d,"idle":%d,"max":50}`, ts, a, 50-a)
		case 4:
			lines[i] = fmt.Sprintf(`{"level":"info","ts":"%s","msg":"request completed","method":"POST","path":"/api/v1/events","status":201,"latency_ms":%d}`, ts, r.between(2, 90))
		case 5:
			lines[i] = fmt.Sprintf(`{"level":"warn","ts":"%s","msg":"slow query detected","query":"SELECT * FROM users WHERE last_active > $1","duration_ms":%d}`, ts, r.between(500, 3000))
		case 6:
			lines[i] = fmt.Sprintf(`{"level":"info","ts":"%s","msg":"worker heartbeat","worker_id":"w-%04d","jobs_processed":%d,"uptime_s":%d}`, ts, r.between(1, 500), r.between(100, 10000), r.between(3600, 86400))
		case 7:
			lines[i] = fmt.Sprintf(`{"level":"info","ts":"%s","msg":"request completed","method":"GET","path":"/api/v1/users/%s","status":200,"latency_ms":%d}`, ts, r.hex(8), r.between(1, 25))
		case 8:
			lines[i] = fmt.Sprintf(`{"level":"debug","ts":"%s","msg":"gc cycle completed","pause_ms":%.1f,"heap_mb":%d}`, ts, float64(r.between(1, 50))/10.0, r.between(64, 512))
		case 9:
			lines[i] = fmt.Sprintf(`{"level":"info","ts":"%s","msg":"kafka consumer","topic":"events","partition":%d,"offset":%d,"lag":%d}`, ts, r.between(0, 7), r.between(500000, 1500000), r.between(0, 40))
		case 10:
			lines[i] = fmt.Sprintf(`{"level":"info","ts":"%s","msg":"grpc call","service":"auth","method":"ValidateToken","duration_ms":%d,"status":"OK"}`, ts, r.between(1, 18))
		case 11:
			lines[i] = fmt.Sprintf(`{"level":"error","ts":"%s","msg":"upstream timeout","service":"recommendation-engine","timeout_ms":5000,"retry":true}`, ts)
		case 12:
			lines[i] = fmt.Sprintf(`{"level":"info","ts":"%s","msg":"batch write completed","table":"events","rows":%d,"duration_ms":%d}`, ts, r.between(50, 500), r.between(10, 200))
		case 13:
			lines[i] = fmt.Sprintf(`{"level":"warn","ts":"%s","msg":"rate limit approaching","client":"10.0.%d.%d","current":%d,"limit":1000}`, ts, r.between(1, 4), r.between(10, 50), r.between(200, 800))
		case 14:
			lines[i] = fmt.Sprintf(`{"level":"info","ts":"%s","msg":"tls handshake","remote":"10.0.%d.%d:%d","proto":"h2","resumed":true}`, ts, r.between(1, 4), r.between(10, 50), r.between(1024, 60000))
		}
	}
	result := ""
	for i, l := range lines {
		if i > 0 {
			result += "\n"
		}
		result += l
	}
	return result
}

// --- fake log streaming ---

var logTemplates = []string{
	`{"level":"info","ts":"%s","msg":"request completed","method":"GET","path":"/api/v1/health","status":200,"latency_ms":%d}`,
	`{"level":"info","ts":"%s","msg":"processed event","event_type":"user.updated","duration_ms":%d,"queue_depth":%d}`,
	`{"level":"info","ts":"%s","msg":"cache hit","key":"session:%s","ttl_remaining":%d}`,
	`{"level":"debug","ts":"%s","msg":"connection pool stats","active":%d,"idle":%d,"max":50}`,
	`{"level":"info","ts":"%s","msg":"request completed","method":"POST","path":"/api/v1/events","status":201,"latency_ms":%d}`,
	`{"level":"warn","ts":"%s","msg":"slow query detected","query":"SELECT * FROM users WHERE ...","duration_ms":%d}`,
	`{"level":"info","ts":"%s","msg":"worker heartbeat","worker_id":"%s","jobs_processed":%d,"uptime_s":%d}`,
	`{"level":"info","ts":"%s","msg":"request completed","method":"GET","path":"/api/v1/users/%s","status":200,"latency_ms":%d}`,
	`{"level":"debug","ts":"%s","msg":"gc cycle completed","pause_ms":%.1f,"heap_mb":%d}`,
	`{"level":"info","ts":"%s","msg":"kafka consumer","topic":"events","partition":%d,"offset":%d,"lag":%d}`,
	`{"level":"info","ts":"%s","msg":"grpc call","service":"auth","method":"ValidateToken","duration_ms":%d,"status":"OK"}`,
	`{"level":"error","ts":"%s","msg":"upstream timeout","service":"recommendation-engine","timeout_ms":5000,"retry":true}`,
	`{"level":"info","ts":"%s","msg":"batch write completed","table":"events","rows":%d,"duration_ms":%d}`,
	`{"level":"warn","ts":"%s","msg":"rate limit approaching","client":"%s","current":%d,"limit":1000}`,
	`{"level":"info","ts":"%s","msg":"tls handshake","remote":"%s:%d","proto":"h2","resumed":true}`,
}

func handleFakeLogs(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	flusher, ok := w.(http.Flusher)
	if !ok {
		w.WriteHeader(200)
		return
	}

	for {
		select {
		case <-r.Context().Done():
			return
		default:
		}

		ts := time.Now().Format(time.RFC3339Nano)
		idx := rand.Intn(len(logTemplates))
		var line string
		switch idx {
		case 0:
			line = fmt.Sprintf(logTemplates[idx], ts, rand.Intn(50)+1)
		case 1:
			line = fmt.Sprintf(logTemplates[idx], ts, rand.Intn(200)+5, rand.Intn(100))
		case 2:
			line = fmt.Sprintf(logTemplates[idx], ts, fmt.Sprintf("%08x", rand.Intn(0xffffffff)), rand.Intn(3600))
		case 3:
			active := rand.Intn(40) + 5
			line = fmt.Sprintf(logTemplates[idx], ts, active, 50-active)
		case 4:
			line = fmt.Sprintf(logTemplates[idx], ts, rand.Intn(100)+2)
		case 5:
			line = fmt.Sprintf(logTemplates[idx], ts, rand.Intn(3000)+500)
		case 6:
			line = fmt.Sprintf(logTemplates[idx], ts, fmt.Sprintf("w-%04d", rand.Intn(500)), rand.Intn(10000)+100, rand.Intn(86400)+3600)
		case 7:
			line = fmt.Sprintf(logTemplates[idx], ts, fmt.Sprintf("%08x", rand.Intn(0xffffffff)), rand.Intn(30)+1)
		case 8:
			line = fmt.Sprintf(logTemplates[idx], ts, float64(rand.Intn(50))/10.0, rand.Intn(512)+64)
		case 9:
			line = fmt.Sprintf(logTemplates[idx], ts, rand.Intn(8), rand.Intn(1000000)+500000, rand.Intn(50))
		case 10:
			line = fmt.Sprintf(logTemplates[idx], ts, rand.Intn(20)+1)
		case 11:
			line = fmt.Sprintf(logTemplates[idx], ts)
		case 12:
			line = fmt.Sprintf(logTemplates[idx], ts, rand.Intn(500)+50, rand.Intn(200)+10)
		case 13:
			line = fmt.Sprintf(logTemplates[idx], ts, fmt.Sprintf("10.0.%d.%d", rand.Intn(4)+1, rand.Intn(50)+10), rand.Intn(800)+200)
		case 14:
			line = fmt.Sprintf(logTemplates[idx], ts, fmt.Sprintf("10.0.%d.%d", rand.Intn(4)+1, rand.Intn(50)+10), rand.Intn(60000)+1024)
		}

		fmt.Fprintf(w, "data: %s\n\n", line)
		flusher.Flush()

		// Random delay between 50ms and 800ms for realistic feel
		time.Sleep(time.Duration(rand.Intn(750)+50) * time.Millisecond)
	}
}
