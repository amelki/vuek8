package kube

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sync"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

type Cache struct {
	client    *Client
	clusterID string

	mu             sync.RWMutex
	pods           []PodInfo
	nodes          []NodeInfo
	namespaces     []string
	ready          bool
	liveDataLoaded bool

	// Progress tracking for initial load
	progressMu sync.RWMutex
	current    int
	total      int
	currentNS  string
	lastError  string
}

type diskSnapshot struct {
	Pods       []PodInfo  `json:"pods"`
	Nodes      []NodeInfo `json:"nodes"`
	Namespaces []string   `json:"namespaces"`
}

func diskCachePath(clusterID string) string {
	home, _ := os.UserHomeDir()
	// Use a hash-safe filename
	safe := ""
	for _, c := range clusterID {
		if (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '-' || c == '_' {
			safe += string(c)
		} else {
			safe += "_"
		}
	}
	return filepath.Join(home, ".config", "kglance", "cache", safe+".json")
}

func NewCache(client *Client, clusterID string) *Cache {
	c := &Cache{client: client, clusterID: clusterID}
	// Try loading disk cache for instant display
	c.loadFromDisk()
	return c
}

func (c *Cache) loadFromDisk() {
	path := diskCachePath(c.clusterID)
	data, err := os.ReadFile(path)
	if err != nil {
		return
	}
	var snap diskSnapshot
	if err := json.Unmarshal(data, &snap); err != nil {
		return
	}
	c.mu.Lock()
	c.pods = snap.Pods
	c.nodes = snap.Nodes
	c.namespaces = snap.Namespaces
	c.ready = true // show cached data immediately
	c.mu.Unlock()
	log.Printf("cache: loaded %d pods from disk cache for %s", len(snap.Pods), c.clusterID)
}

func (c *Cache) saveToDisk() {
	snap := diskSnapshot{
		Pods:       c.pods,
		Nodes:      c.nodes,
		Namespaces: c.namespaces,
	}
	data, err := json.Marshal(snap)
	if err != nil {
		return
	}
	path := diskCachePath(c.clusterID)
	os.MkdirAll(filepath.Dir(path), 0755)
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0644); err != nil {
		return
	}
	os.Rename(tmp, path)
}

func (c *Cache) IsReady() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.ready
}

func (c *Cache) GetPods() []PodInfo {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.pods
}

func (c *Cache) GetNodes() []NodeInfo {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.nodes
}

func (c *Cache) GetNamespaces() []string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.namespaces
}

type Progress struct {
	Current   int    `json:"current"`
	Total     int    `json:"total"`
	Namespace string `json:"namespace"`
	Ready     bool   `json:"ready"`
	Loading   bool   `json:"loading"`
	Error     string `json:"error,omitempty"`
}

func (c *Cache) GetProgress() Progress {
	c.progressMu.RLock()
	defer c.progressMu.RUnlock()
	c.mu.RLock()
	loading := !c.liveDataLoaded
	c.mu.RUnlock()
	return Progress{
		Current:   c.current,
		Total:     c.total,
		Namespace: c.currentNS,
		Ready:     c.IsReady(),
		Loading:   loading,
		Error:     c.lastError,
	}
}

func (c *Cache) setError(err string) {
	c.progressMu.Lock()
	c.lastError = err
	c.progressMu.Unlock()
}

func (c *Cache) setProgress(current, total int, ns string) {
	c.progressMu.Lock()
	c.current = current
	c.total = total
	c.currentNS = ns
	c.progressMu.Unlock()
}

// Start begins background refresh loop (non-blocking)
func (c *Cache) Start(ctx context.Context) {
	go func() {
		// Initial load with progress tracking
		c.refresh(ctx, true)

		// Then refresh every 3 seconds
		ticker := time.NewTicker(3 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				c.refresh(ctx, false)
			}
		}
	}()
}

func (c *Cache) refresh(ctx context.Context, initial bool) {
	timeout := 60 * time.Second
	if initial {
		timeout = 15 * time.Second
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	// Fetch nodes
	nodeList, err := c.client.Clientset.CoreV1().Nodes().List(ctx, metav1.ListOptions{})
	if err != nil {
		log.Printf("cache: failed to list nodes: %v", err)
		if initial {
			c.setError(fmt.Sprintf("Cannot reach cluster: %v", err))
			c.mu.Lock()
			c.ready = true // mark ready so frontend stops waiting
			c.mu.Unlock()
			return
		}
	} else {
		nodes := c.client.buildNodes(nodeList)
		c.mu.Lock()
		c.nodes = nodes
		c.mu.Unlock()
	}

	// Fetch namespaces
	nsList, err := c.client.Clientset.CoreV1().Namespaces().List(ctx, metav1.ListOptions{})
	if err != nil {
		log.Printf("cache: failed to list namespaces: %v", err)
		if initial {
			c.setError(fmt.Sprintf("Cannot list namespaces: %v", err))
			c.mu.Lock()
			c.ready = true
			c.mu.Unlock()
		}
		return
	}

	names := make([]string, 0, len(nsList.Items))
	for _, ns := range nsList.Items {
		names = append(names, ns.Name)
	}

	c.mu.Lock()
	c.namespaces = names
	c.mu.Unlock()

	// Fetch pods - try cluster-wide first
	var allPodItems []corev1.Pod
	podList, err := c.client.Clientset.CoreV1().Pods("").List(ctx, metav1.ListOptions{})
	if err == nil {
		allPodItems = podList.Items
		if initial {
			c.setProgress(1, 1, "done")
		}
	} else {
		// Fallback: per namespace
		total := len(nsList.Items)
		for i, ns := range nsList.Items {
			if initial {
				c.setProgress(i, total, ns.Name)
			}
			pl, plErr := c.client.Clientset.CoreV1().Pods(ns.Name).List(ctx, metav1.ListOptions{})
			if plErr != nil {
				continue
			}
			allPodItems = append(allPodItems, pl.Items...)
		}
		if initial {
			c.setProgress(total, total, "done")
		}
	}

	pods := c.client.convertPods(allPodItems)
	c.mu.Lock()
	c.pods = pods
	c.ready = true
	c.liveDataLoaded = true
	c.mu.Unlock()
	// Clear error only on success
	c.setError("")
	// Save to disk for instant loading next time
	c.saveToDisk()
}
