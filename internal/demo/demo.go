package demo

import (
	_ "embed"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io/fs"
	"math/rand"
	"net/http"
	"sync"
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

	state := &demoState{
		pods:            buildPods(),
		rollingWorkload: "",
	}
	state.metrics = buildMetrics(state.pods)
	state.workloads = state.buildWorkloadStatuses()

	// Rollout controlled via /api/demo/rollout/toggle (POST to toggle, GET to check)
	mux.HandleFunc("/api/demo/rollout/toggle", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "POST" {
			state.mu.Lock()
			if state.rolloutRunning {
				state.rolloutStop = true
			} else {
				state.rolloutStop = false
				go state.simulateRollout()
			}
			state.mu.Unlock()
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"ok":true}`))
	})

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
	mux.HandleFunc("/api/pods", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		state.mu.RLock()
		defer state.mu.RUnlock()
		json.NewEncoder(w).Encode(state.pods)
	})
	mux.HandleFunc("/api/metrics", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		state.mu.RLock()
		defer state.mu.RUnlock()
		json.NewEncoder(w).Encode(state.metrics)
	})
	mux.HandleFunc("/api/workloads", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		state.mu.RLock()
		defer state.mu.RUnlock()
		json.NewEncoder(w).Encode(state.workloads)
	})
	mux.HandleFunc("/api/progress", jsonHandler(kube.Progress{Ready: true}))
	mux.HandleFunc("/api/version", update.HandleVersion)

	mux.HandleFunc("/api/clusters/switch", okHandler)
	mux.HandleFunc("/api/clusters/rename", okHandler)
	mux.HandleFunc("/api/clusters/hide", okHandler)
	mux.HandleFunc("/api/clusters/icon", okHandler)
	mux.HandleFunc("/api/clusters/fetch-icon", cluster.HandleFetchIcon())
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

// --- demo icons (embedded PNGs) ---

//go:embed demo-icon-prod.png
var demoIconProdPNG []byte

//go:embed demo-icon-staging.png
var demoIconStagingPNG []byte

func demoIconDataURL(data []byte) string {
	return "data:image/png;base64," + base64.StdEncoding.EncodeToString(data)
}

// --- clusters ---

func buildClusters() []cluster.ClusterInfo {
	prodIcon := demoIconDataURL(demoIconProdPNG)
	stagingIcon := demoIconDataURL(demoIconStagingPNG)

	return []cluster.ClusterInfo{
		{
			ID: "prod-eu", DisplayName: "Arctis Prod EU",
			ContextName: "arctis-prod-eu", Server: "https://k8s.eu.arctis.io:6443",
			FilePath: "/home/deploy/.kube/arctis-prod.yaml", Active: true, IsDefault: true,
			Icon: prodIcon,
		},
		{
			ID: "prod-us", DisplayName: "Arctis Prod US",
			ContextName: "arctis-prod-us", Server: "https://k8s.us.arctis.io:6443",
			FilePath: "/home/deploy/.kube/arctis-prod.yaml", IsDefault: true,
			Icon: prodIcon,
		},
		{
			ID: "staging", DisplayName: "Arctis Staging",
			ContextName: "arctis-staging", Server: "https://k8s.staging.arctis.io:6443",
			FilePath: "/home/deploy/.kube/arctis-staging.yaml", IsDefault: true,
			Icon: stagingIcon,
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
	replicas  int    // explicit replica count (0 = auto-distribute)
	cpuReq    int64
	cpuLim    int64
	memReqMi  int64
	memLimMi  int64
}

// Production-shaped workloads: a few huge, a few medium, many small.
// Total pods: ~1300 across all workloads.
var allWorkloads = []workload{
	// === Huge workloads (the visual centerpieces) ===
	{"task-runner", "arctis/task-runner", "4.2.1", "Deployment", 480, 200, 500, 256, 512},
	{"shard-group", "arctis/shard-group", "2.7.3", "StatefulSet", 240, 300, 800, 512, 1024},
	{"event-processor", "arctis/event-processor", "3.1.0", "Deployment", 180, 200, 500, 256, 512},

	// === Large workloads ===
	{"caller-pool-a", "arctis/caller", "1.9.2", "StatefulSet", 100, 200, 500, 384, 768},
	{"caller-pool-b", "arctis/caller", "1.9.2", "StatefulSet", 100, 200, 500, 384, 768},
	{"custom-runtime", "arctis/custom-runtime", "5.0.4", "StatefulSet", 70, 300, 800, 512, 1024},

	// === Medium workloads ===
	{"cache", "bitnami/redis", "7.4.1", "StatefulSet", 24, 200, 500, 512, 2048},
	{"api", "arctis/api", "12.4.0", "Deployment", 24, 250, 600, 384, 768},
	{"scheduler", "arctis/scheduler", "3.5.0", "StatefulSet", 20, 200, 500, 256, 512},
	{"gateway", "arctis/gateway", "2.14.0", "Deployment", 18, 200, 500, 256, 512},
	{"browser-rendering", "arctis/browser-rendering", "1.3.0", "Deployment", 10, 500, 1500, 1024, 2048},

	// === Small-medium workloads ===
	{"discovery", "arctis/discovery", "1.0.5", "StatefulSet", 8, 100, 300, 128, 256},
	{"ingress-public", "ingress-nginx/controller", "1.11.2", "Deployment", 6, 200, 500, 256, 512},
	{"ingress-internal", "ingress-nginx/controller", "1.11.2", "Deployment", 6, 200, 500, 256, 512},
	{"analytics-worker", "arctis/analytics-worker", "1.5.0", "Deployment", 5, 200, 500, 256, 512},
	{"shard-broker", "arctis/shard-broker", "1.0.3", "StatefulSet", 4, 150, 400, 192, 384},
	{"rss", "arctis/rss-fetcher", "1.4.0", "StatefulSet", 4, 100, 300, 128, 256},
	{"avatars-website", "arctis/avatars-website", "2.0.0", "Deployment", 4, 100, 250, 128, 256},

	// === Small workloads ===
	{"internal-api", "arctis/internal-api", "2.1.0", "Deployment", 3, 100, 300, 128, 256},
	{"poster-service", "arctis/poster-service", "1.3.0", "Deployment", 3, 100, 300, 128, 256},
	{"url-shortener", "arctis/url-shortener", "1.2.0", "Deployment", 2, 50, 150, 64, 128},
	{"webhook-dispatcher", "arctis/webhook-dispatcher", "1.4.1", "Deployment", 2, 150, 400, 192, 384},
	{"mcp-server", "arctis/mcp-server", "0.8.0", "Deployment", 2, 100, 300, 128, 256},

	// === Singleton workloads ===
	{"tiktok-fetcher", "arctis/tiktok-fetcher", "1.0.2", "StatefulSet", 1, 100, 300, 128, 256},
	{"audio-worker", "arctis/audio-worker", "2.0.1", "StatefulSet", 1, 500, 1500, 1024, 2048},
	{"redis-primary-0", "bitnami/redis", "7.4.1", "StatefulSet", 1, 200, 500, 512, 2048},
	{"redis-primary-1", "bitnami/redis", "7.4.1", "StatefulSet", 1, 200, 500, 512, 2048},
	{"redis-primary-2", "bitnami/redis", "7.4.1", "StatefulSet", 1, 200, 500, 512, 2048},
	{"transcript-worker", "arctis/transcript-worker", "1.2.0", "Deployment", 1, 200, 500, 256, 512},
	{"shell-data", "arctis/shell-data", "1.0.0", "Deployment", 1, 50, 150, 64, 128},
	{"shell", "arctis/shell", "1.0.0", "Deployment", 1, 50, 150, 64, 128},
	{"feature-flags", "arctis/feature-flags", "1.0.4", "Deployment", 1, 100, 200, 128, 256},
	{"audit-logger", "arctis/audit-logger", "1.3.5", "Deployment", 1, 100, 300, 128, 256},
	{"config-service", "arctis/config-service", "1.0.1", "Deployment", 1, 50, 150, 64, 128},
	{"geo-service", "arctis/geo-service", "1.5.0", "Deployment", 1, 100, 300, 128, 256},
	{"translation-api", "arctis/translation-api", "2.0.0", "Deployment", 1, 200, 500, 256, 512},
}

// Stateful brokers running on dedicated broker nodes.
var brokerWorkloads = []workload{
	{"nats-streaming", "nats-streaming", "0.25.6", "StatefulSet", 4, 500, 2000, 1024, 4096},
	{"kafka", "bitnami/kafka", "3.8.0", "StatefulSet", 4, 1000, 3000, 2048, 8192},
	{"zookeeper", "bitnami/zookeeper", "3.9.2", "StatefulSet", 4, 200, 500, 512, 1024},
	{"rabbitmq", "bitnami/rabbitmq", "3.13.7", "StatefulSet", 4, 500, 1500, 1024, 4096},
	{"event-router", "arctis/event-router", "2.4.1", "Deployment", 4, 200, 500, 256, 512},
	{"schema-registry", "confluentinc/schema-registry", "7.7.1", "Deployment", 4, 300, 800, 512, 1024},
}

var ages = []string{"1d", "2d", "3d", "5d", "7d", "8d", "10d", "12d", "14d", "18d", "21d", "28d", "30d"}

func buildPods() []kube.PodInfo {
	r := newRng(42)
	var pods []kube.PodInfo

	ssCounter := map[string]int{} // global counter per StatefulSet name
	makePod := func(w workload, node string, idx int) kube.PodInfo {
		var podName string
		if w.kind == "StatefulSet" {
			n := ssCounter[w.name]
			ssCounter[w.name] = n + 1
			podName = fmt.Sprintf("%s-%d", w.name, n)
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

	// === Broker nodes: 4 nodes, brokerWorkloads spread across them ===
	brokerNodes := []string{"brokers-1", "brokers-2", "brokers-3", "brokers-4"}
	for _, w := range brokerWorkloads {
		for i := 0; i < w.replicas; i++ {
			node := brokerNodes[i%len(brokerNodes)]
			pods = append(pods, makePod(w, node, i))
		}
	}

	// === General nodes (13) + worker nodes (15): spread allWorkloads pods ===
	var generalNodes []string
	for i := 0; i < 13; i++ {
		generalNodes = append(generalNodes, fmt.Sprintf("general-%d", i+1))
	}
	var workerNodes []string
	for i := 0; i < 15; i++ {
		workerNodes = append(workerNodes, fmt.Sprintf("workers-%d", i+1))
	}
	allNodesList := append([]string{}, generalNodes...)
	allNodesList = append(allNodesList, workerNodes...)

	// Distribute each workload's pods across nodes (round-robin per workload,
	// starting at a different offset per workload to spread load evenly)
	for wIdx, w := range allWorkloads {
		startOffset := wIdx % len(allNodesList)
		for i := 0; i < w.replicas; i++ {
			node := allNodesList[(startOffset+i)%len(allNodesList)]
			pods = append(pods, makePod(w, node, i))
		}
	}

	// === Apply ~2% red status with clustering ===
	// Mark some pods as unhealthy in small grapes.
	totalPods := len(pods)
	targetRed := totalPods / 50 // ~2%
	redCount := 0
	redStatuses := []string{"CrashLoopBackOff", "Error", "ImagePullBackOff"}

	i := 0
	for i < totalPods && redCount < targetRed {
		// Decide next grape size — mostly singles with rare pairs
		roll := r.intn(10)
		var grapeSize int
		switch {
		case roll < 7: // 70% chance: single pod
			grapeSize = 1
		case roll < 9: // 20% chance: pair
			grapeSize = 2
		default: // 10% chance: grape of 3
			grapeSize = 3
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

func podNameHash(name string) uint64 {
	var h uint64
	for _, c := range name {
		h = h*31 + uint64(c)
	}
	return h
}

// skewedPct returns a percentage (1..90) skewed toward low values.
// Roughly: 70% chance [5..25], 20% chance [25..55], 10% chance [55..90].
func skewedPct(r *rng) int {
	roll := r.intn(10)
	switch {
	case roll < 7:
		return r.between(5, 25)
	case roll < 9:
		return r.between(25, 55)
	default:
		return r.between(55, 90)
	}
}

func buildMetrics(pods []kube.PodInfo) []kube.PodMetrics {
	var metrics []kube.PodMetrics
	hotUsed := 0
	for _, p := range pods {
		if p.Status != "Running" {
			continue
		}
		// Per-pod deterministic RNG based on pod name — stable across rebuilds
		r := newRng(podNameHash(p.Name))
		// Skewed distribution: most pods are idle/low-usage, few are warm, rare are hot
		cpuPct := skewedPct(r)
		memPct := skewedPct(r)

		cpuMilli := p.CPURequestMilli * int64(cpuPct) / 100

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

// --- Rollout simulation ---

type demoState struct {
	mu               sync.RWMutex
	pods             []kube.PodInfo
	metrics          []kube.PodMetrics
	workloads        []kube.WorkloadStatus
	rollingWorkload  string  // name of workload currently rolling out
	rolloutTotal     int32   // total pods to roll
	rolloutProgress  float64 // 0.0 to 1.0, advances smoothly
	rolloutRunning   bool   // is a rollout goroutine active
	rolloutStop      bool   // signal to stop the rollout
}

func (s *demoState) buildWorkloadStatuses() []kube.WorkloadStatus {
	// Count pods per workload
	type wlInfo struct {
		kind    string
		ns      string
		total   int32
		ready   int32
		updated int32
	}
	wls := map[string]*wlInfo{}
	for _, p := range s.pods {
		key := p.WorkloadName
		if key == "" {
			continue
		}
		w, ok := wls[key]
		if !ok {
			w = &wlInfo{kind: p.WorkloadKind, ns: p.Namespace}
			wls[key] = w
		}
		w.total++
		if p.Status == "Running" {
			w.ready++
			w.updated++
		}
	}

	var statuses []kube.WorkloadStatus
	for name, w := range wls {
		status := "stable"
		if name == s.rollingWorkload {
			status = "progressing"
			w.updated = int32(s.rolloutProgress * float64(s.rolloutTotal))
			w.total = s.rolloutTotal
		} else if w.ready < w.total && w.total > 0 {
			// Some pods not ready but not actively rolling
			if w.ready == 0 {
				status = "degraded"
			}
		}
		statuses = append(statuses, kube.WorkloadStatus{
			Name:              name,
			Namespace:         w.ns,
			Kind:              w.kind,
			Replicas:          w.total,
			ReadyReplicas:     w.ready,
			UpdatedReplicas:   w.updated,
			AvailableReplicas: w.ready,
			RolloutStatus:     status,
		})
	}
	return statuses
}

func (s *demoState) findRunningIndices(workloadName string) []int {
	var indices []int
	for i, p := range s.pods {
		if p.WorkloadName == workloadName && p.Status == "Running" {
			indices = append(indices, i)
		}
	}
	return indices
}

func (s *demoState) simulateRollout() {
	s.mu.Lock()
	s.rolloutRunning = true
	s.mu.Unlock()

	defer func() {
		s.mu.Lock()
		s.rolloutRunning = false
		s.rollingWorkload = ""
		s.workloads = s.buildWorkloadStatuses()
		s.mu.Unlock()
	}()

	r := rand.New(rand.NewSource(time.Now().UnixNano()))

	shouldStop := func() bool {
		s.mu.RLock()
		defer s.mu.RUnlock()
		return s.rolloutStop
	}

	sleepOrStop := func(d time.Duration) bool {
		// Sleep in small increments to respond to stop quickly
		end := time.Now().Add(d)
		for time.Now().Before(end) {
			if shouldStop() {
				return true
			}
			time.Sleep(200 * time.Millisecond)
		}
		return shouldStop()
	}

	for {
		if shouldStop() {
			return
		}
		// Find a workload with enough pods
		s.mu.RLock()
		counts := map[string]int{}
		for _, p := range s.pods {
			if p.Status == "Running" && p.WorkloadKind == "Deployment" {
				counts[p.WorkloadName]++
			}
		}
		s.mu.RUnlock()

		wlName := "api"
		if counts[wlName] == 0 {
			time.Sleep(5 * time.Second)
			continue
		}
		total := counts[wlName]
		fmt.Printf("demo: rolling out %s (%d pods)\n", wlName, total)

		// Mark rollout in progress
		s.mu.Lock()
		s.rollingWorkload = wlName
		s.rolloutTotal = int32(total)
		s.rolloutProgress = 0
		s.workloads = s.buildWorkloadStatuses()
		s.mu.Unlock()

		// Collect running pods to roll
		s.mu.RLock()
		var podNames []string
		var template kube.PodInfo
		for _, p := range s.pods {
			if p.WorkloadName == wlName && p.Status == "Running" {
				podNames = append(podNames, p.Name)
				template = p
			}
		}
		s.mu.RUnlock()

		// Roll in batches of ~25% (like maxSurge/maxUnavailable)
		batchSize := len(podNames) / 4
		if batchSize < 2 {
			batchSize = 2
		}

		for len(podNames) > 0 {
			batch := batchSize
			if batch > len(podNames) {
				batch = len(podNames)
			}
			batchNames := podNames[:batch]
			podNames = podNames[batch:]

			// Generate new names for this batch
			newNames := make([]string, batch)
			for i := range newNames {
				newNames[i] = wlName + "-" + fmt.Sprintf("%05x", r.Intn(0xfffff)) + "-" + fmt.Sprintf("%05x", r.Intn(0xfffff))
			}

			batchFraction := float64(batch) / float64(total)

			// Step 1: Add new ContainerCreating pods (card grows)
			s.mu.Lock()
			s.rolloutProgress += batchFraction * 0.33
			for i, newName := range newNames {
				_ = batchNames[i]
				s.pods = append(s.pods, kube.PodInfo{
					Name:            newName,
					Namespace:       template.Namespace,
					Status:          "ContainerCreating",
					Ready:           "0/1",
					Age:             "0s",
					Node:            template.Node,
					Containers:      []kube.ContainerInfo{{Name: template.Containers[0].Name, Image: template.Containers[0].Image, Tag: template.Containers[0].Tag, Status: "Waiting"}},
					WorkloadName:    wlName,
					WorkloadKind:    template.WorkloadKind,
					CPURequestMilli: template.CPURequestMilli,
					CPULimitMilli:   template.CPULimitMilli,
					MemRequestBytes: template.MemRequestBytes,
					MemLimitBytes:   template.MemLimitBytes,
				})
			}
			s.metrics = buildMetrics(s.pods)
			s.workloads = s.buildWorkloadStatuses()
			s.mu.Unlock()
			if sleepOrStop(1500 * time.Millisecond) { return }

			// Step 2: New pods → Running, old pods → Terminating
			s.mu.Lock()
			s.rolloutProgress += batchFraction * 0.33
			newNameSet := map[string]bool{}
			oldNameSet := map[string]bool{}
			for _, n := range newNames {
				newNameSet[n] = true
			}
			for _, n := range batchNames {
				oldNameSet[n] = true
			}
			for k := range s.pods {
				if newNameSet[s.pods[k].Name] {
					s.pods[k].Status = "Running"
					s.pods[k].Ready = "1/1"
					s.pods[k].Containers[0].Status = "Running"
					s.pods[k].Age = "3s"
				}
				if oldNameSet[s.pods[k].Name] {
					s.pods[k].Status = "Terminating"
					s.pods[k].Containers[0].Status = "Terminating"
				}
			}
			s.metrics = buildMetrics(s.pods)
			s.workloads = s.buildWorkloadStatuses()
			s.mu.Unlock()
			if sleepOrStop(1500 * time.Millisecond) { return }

			// Step 3: Remove old pods, advance progress
			s.mu.Lock()
			s.rolloutProgress += batchFraction * 0.34
			if s.rolloutProgress > 1 { s.rolloutProgress = 1 }
			newPods := make([]kube.PodInfo, 0, len(s.pods)-batch)
			for _, p := range s.pods {
				if !oldNameSet[p.Name] {
					newPods = append(newPods, p)
				}
			}
			s.pods = newPods
			s.metrics = buildMetrics(s.pods)
			s.workloads = s.buildWorkloadStatuses()
			s.mu.Unlock()

			if sleepOrStop(1500 * time.Millisecond) { return }
		}

		// Mark rollout complete
		s.mu.Lock()
		s.rollingWorkload = ""
		s.workloads = s.buildWorkloadStatuses()
		s.mu.Unlock()
		fmt.Printf("demo: rollout of %s complete\n", wlName)
		return // one rollout per trigger
	}
}

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
