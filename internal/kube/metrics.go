package kube

import (
	"context"
	"encoding/json"
	"fmt"
	"time"
)

type PodMetrics struct {
	Name       string             `json:"name"`
	Namespace  string             `json:"namespace"`
	Containers []ContainerMetrics `json:"containers"`
}

type ContainerMetrics struct {
	Name     string `json:"name"`
	CPUNano  int64  `json:"cpuNano"`  // CPU usage in nanocores
	CPUMilli int64  `json:"cpuMilli"` // CPU usage in millicores
	MemBytes int64  `json:"memBytes"` // Memory usage in bytes
}

// metricsAPIResponse matches the metrics.k8s.io/v1beta1 PodMetricsList
type metricsAPIResponse struct {
	Items []struct {
		Metadata struct {
			Name      string `json:"name"`
			Namespace string `json:"namespace"`
		} `json:"metadata"`
		Containers []struct {
			Name  string `json:"name"`
			Usage struct {
				CPU    string `json:"cpu"`
				Memory string `json:"memory"`
			} `json:"usage"`
		} `json:"containers"`
	} `json:"items"`
}

func (c *Client) FetchMetrics(ctx context.Context, namespace string) ([]PodMetrics, error) {
	path := "/apis/metrics.k8s.io/v1beta1/pods"
	if namespace != "" {
		path = fmt.Sprintf("/apis/metrics.k8s.io/v1beta1/namespaces/%s/pods", namespace)
	}

	data, err := c.Clientset.RESTClient().Get().AbsPath(path).DoRaw(ctx)
	if err != nil {
		return nil, err
	}

	var resp metricsAPIResponse
	if err := json.Unmarshal(data, &resp); err != nil {
		return nil, err
	}

	var result []PodMetrics
	for _, item := range resp.Items {
		pm := PodMetrics{
			Name:      item.Metadata.Name,
			Namespace: item.Metadata.Namespace,
		}
		for _, c := range item.Containers {
			cm := ContainerMetrics{
				Name:     c.Name,
				CPUNano:  parseCPU(c.Usage.CPU),
				MemBytes: parseMemory(c.Usage.Memory),
			}
			cm.CPUMilli = cm.CPUNano / 1_000_000
			pm.Containers = append(pm.Containers, cm)
		}
		result = append(result, pm)
	}
	return result, nil
}

// parseCPU parses Kubernetes CPU values like "250m", "1", "100n", "202560000n"
// The metrics API typically returns nanocores (with or without 'n' suffix)
func parseCPU(s string) int64 {
	if s == "" {
		return 0
	}
	var n int64
	if s[len(s)-1] == 'n' {
		// nanocores (explicit suffix)
		fmt.Sscanf(s, "%dn", &n)
		return n
	}
	if s[len(s)-1] == 'm' {
		// millicores
		fmt.Sscanf(s, "%dm", &n)
		return n * 1_000_000
	}
	// Bare number — from metrics API this is nanocores,
	// but could be whole cores in pod specs. Heuristic:
	// if > 1000, it's nanocores; otherwise whole cores.
	fmt.Sscanf(s, "%d", &n)
	if n > 1000 {
		return n // nanocores
	}
	return n * 1_000_000_000 // whole cores
}

// parseMemory parses Kubernetes memory values like "128Mi", "1Gi", "500Ki"
func parseMemory(s string) int64 {
	if s == "" {
		return 0
	}
	var n int64
	if len(s) >= 2 {
		suffix := s[len(s)-2:]
		switch suffix {
		case "Ki":
			fmt.Sscanf(s, "%dKi", &n)
			return n * 1024
		case "Mi":
			fmt.Sscanf(s, "%dMi", &n)
			return n * 1024 * 1024
		case "Gi":
			fmt.Sscanf(s, "%dGi", &n)
			return n * 1024 * 1024 * 1024
		}
	}
	fmt.Sscanf(s, "%d", &n)
	return n
}

// FetchAllMetrics fetches metrics across all namespaces, with fallback
func (c *Client) FetchAllMetrics(ctx context.Context, namespaces []string) []PodMetrics {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	// Try cluster-wide first
	metrics, err := c.FetchMetrics(ctx, "")
	if err == nil {
		return metrics
	}

	// Fallback: per namespace
	var all []PodMetrics
	for _, ns := range namespaces {
		m, err := c.FetchMetrics(ctx, ns)
		if err != nil {
			continue
		}
		all = append(all, m...)
	}
	return all
}
