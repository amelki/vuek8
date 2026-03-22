package demo

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"net/http"

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
	mux.HandleFunc("/api/logs", jsonHandler([]string{}))
	mux.HandleFunc("/api/logs/stream", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(200)
	})
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
		{
			ID: "dev", DisplayName: "arctis-dev",
			ContextName: "arctis-dev", Server: "https://k8s.dev.arctis.io:6443",
			FilePath: "/home/deploy/.kube/arctis-dev.yaml", IsDefault: true,
		},
	}
}

func buildNamespaces() []string {
	return []string{"default", "ingress", "data", "monitoring", "platform", "services", "kube-system"}
}

func buildNodes() []kube.NodeInfo {
	return []kube.NodeInfo{
		{
			Name: "app-pool-a7f8d2e19b", IP: "10.0.1.11", Status: "Ready", Roles: "worker",
			KubeletVersion: "v1.31.2", OS: "linux", Arch: "amd64",
			CPUCapacity: "8", MemoryCapacity: "31.4Gi",
			Labels: map[string]string{
				"node-pool": "app-pool", "topology.kubernetes.io/zone": "eu-west-1a",
				"node.kubernetes.io/instance-type": "m6i.2xlarge",
			},
		},
		{
			Name: "app-pool-b3c9e5f4d7", IP: "10.0.1.24", Status: "Ready", Roles: "worker",
			KubeletVersion: "v1.31.2", OS: "linux", Arch: "amd64",
			CPUCapacity: "8", MemoryCapacity: "31.4Gi",
			Labels: map[string]string{
				"node-pool": "app-pool", "topology.kubernetes.io/zone": "eu-west-1a",
				"node.kubernetes.io/instance-type": "m6i.2xlarge",
			},
		},
		{
			Name: "app-pool-d1e6a8b72c", IP: "10.0.2.17", Status: "Ready", Roles: "worker",
			KubeletVersion: "v1.31.2", OS: "linux", Arch: "amd64",
			CPUCapacity: "8", MemoryCapacity: "31.4Gi",
			Labels: map[string]string{
				"node-pool": "app-pool", "topology.kubernetes.io/zone": "eu-west-1b",
				"node.kubernetes.io/instance-type": "m6i.2xlarge",
			},
		},
		{
			Name: "worker-pool-e2d4f6a83e", IP: "10.0.1.35", Status: "Ready", Roles: "worker",
			KubeletVersion: "v1.31.2", OS: "linux", Arch: "amd64",
			CPUCapacity: "16", MemoryCapacity: "62.8Gi",
			Labels: map[string]string{
				"node-pool": "worker-pool", "topology.kubernetes.io/zone": "eu-west-1a",
				"node.kubernetes.io/instance-type": "r6i.4xlarge",
			},
		},
		{
			Name: "worker-pool-f7b3c1d54a", IP: "10.0.2.42", Status: "Ready", Roles: "worker",
			KubeletVersion: "v1.31.2", OS: "linux", Arch: "amd64",
			CPUCapacity: "16", MemoryCapacity: "62.8Gi",
			Labels: map[string]string{
				"node-pool": "worker-pool", "topology.kubernetes.io/zone": "eu-west-1b",
				"node.kubernetes.io/instance-type": "r6i.4xlarge",
			},
		},
		{
			Name: "infra-pool-c8a2e4d61f", IP: "10.0.3.10", Status: "Ready", Roles: "worker",
			KubeletVersion: "v1.31.2", OS: "linux", Arch: "amd64",
			CPUCapacity: "4", MemoryCapacity: "15.6Gi",
			Labels: map[string]string{
				"node-pool": "infra-pool", "topology.kubernetes.io/zone": "eu-west-1a",
				"node.kubernetes.io/instance-type": "t3.xlarge",
			},
		},
	}
}

func buildPods() []kube.PodInfo {
	var pods []kube.PodInfo

	// --- platform namespace ---
	pods = append(pods, deployment("api-gateway", "platform", 3, []string{
		"app-pool-a7f8d2e19b", "app-pool-b3c9e5f4d7", "app-pool-d1e6a8b72c",
	}, "arctis/api-gateway", "2.14.0", 200, 500, 256, 512, "12d")...)

	pods = append(pods, deployment("auth-service", "platform", 2, []string{
		"app-pool-a7f8d2e19b", "app-pool-d1e6a8b72c",
	}, "arctis/auth-service", "1.8.3", 150, 400, 192, 384, "12d")...)

	pods = append(pods, deployment("session-manager", "platform", 2, []string{
		"app-pool-b3c9e5f4d7", "app-pool-a7f8d2e19b",
	}, "arctis/session-manager", "1.2.1", 100, 300, 128, 256, "8d")...)

	pods = append(pods, deployment("user-service", "platform", 2, []string{
		"app-pool-d1e6a8b72c", "app-pool-b3c9e5f4d7",
	}, "arctis/user-service", "3.1.0", 150, 400, 192, 384, "12d")...)

	// --- services namespace ---
	pods = append(pods, deployment("catalog-api", "services", 3, []string{
		"app-pool-a7f8d2e19b", "app-pool-b3c9e5f4d7", "app-pool-d1e6a8b72c",
	}, "arctis/catalog-api", "4.7.2", 250, 600, 384, 768, "5d")...)

	pods = append(pods, deployment("search-indexer", "services", 2, []string{
		"worker-pool-e2d4f6a83e", "worker-pool-f7b3c1d54a",
	}, "arctis/search-indexer", "2.3.1", 500, 1000, 512, 1024, "5d")...)
	pods[len(pods)-1].Restarts = 3 // one pod has restarts

	pods = append(pods, deployment("notification-worker", "services", 2, []string{
		"worker-pool-e2d4f6a83e", "worker-pool-f7b3c1d54a",
	}, "arctis/notification-worker", "1.5.0", 200, 500, 256, 512, "12d")...)

	pods = append(pods, deployment("payment-processor", "services", 2, []string{
		"app-pool-a7f8d2e19b", "app-pool-d1e6a8b72c",
	}, "arctis/payment-processor", "3.0.4", 200, 500, 256, 512, "12d")...)

	pods = append(pods, deployment("analytics-pipeline", "services", 3, []string{
		"worker-pool-e2d4f6a83e", "worker-pool-f7b3c1d54a", "worker-pool-e2d4f6a83e",
	}, "arctis/analytics-pipeline", "2.1.0", 400, 800, 512, 1024, "3d")...)
	// Make the last analytics pod Pending (unscheduled)
	pods[len(pods)-1].Status = "Pending"
	pods[len(pods)-1].Ready = "0/1"
	pods[len(pods)-1].Node = ""
	pods[len(pods)-1].Containers[0].Status = "Waiting"

	pods = append(pods, deployment("recommendation-engine", "services", 2, []string{
		"worker-pool-e2d4f6a83e", "worker-pool-f7b3c1d54a",
	}, "arctis/recommendation-engine", "1.9.7", 600, 1500, 1024, 2048, "8d")...)

	pods = append(pods, deployment("event-bus", "services", 2, []string{
		"app-pool-b3c9e5f4d7", "app-pool-d1e6a8b72c",
	}, "arctis/event-bus", "1.3.2", 150, 400, 192, 384, "12d")...)

	// --- data namespace ---
	pods = append(pods, statefulset("postgres", "data", 3, []string{
		"worker-pool-e2d4f6a83e", "worker-pool-f7b3c1d54a", "worker-pool-e2d4f6a83e",
	}, "bitnami/postgresql", "16.2.0", 500, 2000, 1024, 4096, "28d")...)

	pods = append(pods, statefulset("redis-cluster", "data", 3, []string{
		"worker-pool-e2d4f6a83e", "worker-pool-f7b3c1d54a", "worker-pool-e2d4f6a83e",
	}, "bitnami/redis", "7.4.1", 200, 500, 512, 2048, "28d")...)

	pods = append(pods, statefulset("kafka", "data", 3, []string{
		"worker-pool-f7b3c1d54a", "worker-pool-e2d4f6a83e", "worker-pool-f7b3c1d54a",
	}, "bitnami/kafka", "3.8.0", 500, 1500, 1024, 4096, "21d")...)

	// --- monitoring namespace ---
	pods = append(pods, statefulset("prometheus", "monitoring", 1, []string{
		"infra-pool-c8a2e4d61f",
	}, "prom/prometheus", "2.54.0", 300, 1000, 512, 2048, "30d")...)

	pods = append(pods, deployment("grafana", "monitoring", 1, []string{
		"infra-pool-c8a2e4d61f",
	}, "grafana/grafana", "11.3.0", 100, 300, 128, 512, "30d")...)

	pods = append(pods, statefulset("loki", "monitoring", 1, []string{
		"infra-pool-c8a2e4d61f",
	}, "grafana/loki", "3.2.0", 200, 500, 256, 1024, "30d")...)

	// --- ingress namespace ---
	ingressNodes := []string{
		"app-pool-a7f8d2e19b", "app-pool-b3c9e5f4d7", "app-pool-d1e6a8b72c",
		"worker-pool-e2d4f6a83e", "worker-pool-f7b3c1d54a", "infra-pool-c8a2e4d61f",
	}
	pods = append(pods, daemonset("nginx-ingress", "ingress", ingressNodes,
		"ingress-nginx/controller", "1.11.3", 100, 200, 128, 256, "30d")...)

	// --- kube-system namespace ---
	pods = append(pods, deployment("coredns", "kube-system", 2, []string{
		"infra-pool-c8a2e4d61f", "app-pool-a7f8d2e19b",
	}, "coredns/coredns", "1.11.3", 100, 200, 70, 170, "30d")...)

	pods = append(pods, daemonset("kube-proxy", "kube-system", ingressNodes,
		"kube-proxy", "v1.31.2", 50, 0, 64, 0, "30d")...)

	pods = append(pods, deployment("metrics-server", "kube-system", 1, []string{
		"infra-pool-c8a2e4d61f",
	}, "metrics-server/metrics-server", "0.7.2", 100, 300, 128, 512, "30d")...)

	return pods
}

// deployment creates N replica pods for a Deployment workload.
func deployment(name, ns string, replicas int, nodes []string, image, tag string, cpuReq, cpuLim, memReqMi, memLimMi int64, age string) []kube.PodInfo {
	return makePods(name, ns, "Deployment", replicas, nodes, image, tag, cpuReq, cpuLim, memReqMi, memLimMi, age)
}

// statefulset creates N replica pods for a StatefulSet workload.
func statefulset(name, ns string, replicas int, nodes []string, image, tag string, cpuReq, cpuLim, memReqMi, memLimMi int64, age string) []kube.PodInfo {
	return makePods(name, ns, "StatefulSet", replicas, nodes, image, tag, cpuReq, cpuLim, memReqMi, memLimMi, age)
}

// daemonset creates pods on each specified node for a DaemonSet workload.
func daemonset(name, ns string, nodes []string, image, tag string, cpuReq, cpuLim, memReqMi, memLimMi int64, age string) []kube.PodInfo {
	return makePods(name, ns, "DaemonSet", len(nodes), nodes, image, tag, cpuReq, cpuLim, memReqMi, memLimMi, age)
}

var podHashes = []string{"7f4d2", "8a3e1", "c9b5f", "d2e8a", "e1f7c", "3b6d9", "a4c8e", "f5d1b", "6e2a7", "b8f3c", "1d5e9", "9c7a4"}

func makePods(name, ns, kind string, count int, nodes []string, image, tag string, cpuReq, cpuLim, memReqMi, memLimMi int64, age string) []kube.PodInfo {
	pods := make([]kube.PodInfo, 0, count)
	for i := 0; i < count; i++ {
		hash := podHashes[i%len(podHashes)]
		podName := name + "-" + hash
		if kind == "StatefulSet" {
			podName = fmt.Sprintf("%s-%d", name, i)
		}
		nodeName := ""
		if i < len(nodes) {
			nodeName = nodes[i]
		}
		// Container name: last segment of workload name
		containerName := name
		pods = append(pods, kube.PodInfo{
			Name:            podName,
			Namespace:       ns,
			Status:          "Running",
			Ready:           "1/1",
			Restarts:        0,
			CPURequestMilli: cpuReq,
			CPULimitMilli:   cpuLim,
			MemRequestBytes: memReqMi * 1024 * 1024,
			MemLimitBytes:   memLimMi * 1024 * 1024,
			Age:             age,
			Node:            nodeName,
			Containers: []kube.ContainerInfo{
				{Name: containerName, Image: image, Tag: tag, Status: "Running"},
			},
			WorkloadName: name,
			WorkloadKind: kind,
		})
	}
	return pods
}

func buildMetrics(pods []kube.PodInfo) []kube.PodMetrics {
	// Generate realistic metrics: ~30-65% of CPU request, ~40-75% of memory request
	var metrics []kube.PodMetrics
	seed := 42
	for _, p := range pods {
		if p.Status != "Running" {
			continue
		}
		seed = (seed*1103515245 + 12345) & 0x7fffffff
		cpuPct := 30 + (seed % 36)             // 30-65% of request
		seed = (seed*1103515245 + 12345) & 0x7fffffff
		memPct := 40 + (seed % 36)             // 40-75% of request

		cpuMilli := p.CPURequestMilli * int64(cpuPct) / 100
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
