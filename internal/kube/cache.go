package kube

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/tools/cache"
)

type Cache struct {
	client    *Client
	clusterID string

	mu             sync.RWMutex
	pods           []PodInfo
	nodes          []NodeInfo
	namespaces     []string
	metrics        []PodMetrics
	workloads      []WorkloadStatus
	ready          bool
	liveDataLoaded bool

	// Debounce for informer events
	rebuildCh chan struct{}

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
	c.ready = true
	c.mu.Unlock()
	log.Printf("cache: loaded %d pods from disk cache for %s", len(snap.Pods), c.clusterID)
}

func (c *Cache) saveToDisk() {
	c.mu.RLock()
	snap := diskSnapshot{
		Pods:       c.pods,
		Nodes:      c.nodes,
		Namespaces: c.namespaces,
	}
	c.mu.RUnlock()
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

func (c *Cache) GetMetrics() []PodMetrics {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.metrics
}

func (c *Cache) GetWorkloads() []WorkloadStatus {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.workloads
}

func (c *Cache) GetClient() *Client {
	return c.client
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

// Start begins Watch-based cache using Kubernetes Informers.
// Pods, Nodes, Deployments, and StatefulSets are watched for real-time updates.
// Metrics are polled every 10 seconds (metrics API does not support Watch).
func (c *Cache) Start(ctx context.Context) {
	go c.startInformers(ctx)
}

func (c *Cache) startInformers(ctx context.Context) {
	clientset := c.client.Clientset

	// Step 1: Fetch namespaces (needed for RBAC fallback and namespace list)
	c.setProgress(0, 4, "namespaces")
	nsList, err := clientset.CoreV1().Namespaces().List(ctx, metav1.ListOptions{})
	if err != nil {
		log.Printf("cache: failed to list namespaces: %v", err)
		c.setError(fmt.Sprintf("Cannot list namespaces: %v", err))
		c.mu.Lock()
		c.ready = true
		c.mu.Unlock()
		return
	}
	names := make([]string, 0, len(nsList.Items))
	for _, ns := range nsList.Items {
		names = append(names, ns.Name)
	}
	c.mu.Lock()
	c.namespaces = names
	c.mu.Unlock()

	// Step 2: Detect RBAC — can we list pods cluster-wide?
	c.setProgress(1, 4, "detecting permissions")
	_, clusterWideErr := clientset.CoreV1().Pods("").List(ctx, metav1.ListOptions{Limit: 1})
	clusterWide := clusterWideErr == nil

	// Step 3: Create informer factories
	stopCh := ctx.Done()
	var factories []informers.SharedInformerFactory
	var podInformers []cache.SharedIndexInformer
	var nodeInformer cache.SharedIndexInformer
	var deployInformers []cache.SharedIndexInformer
	var stsInformers []cache.SharedIndexInformer

	noResync := time.Duration(0)

	if clusterWide {
		log.Printf("cache: using cluster-wide informers")
		factory := informers.NewSharedInformerFactory(clientset, noResync)
		factories = append(factories, factory)
		podInformers = append(podInformers, factory.Core().V1().Pods().Informer())
		nodeInformer = factory.Core().V1().Nodes().Informer()
		deployInformers = append(deployInformers, factory.Apps().V1().Deployments().Informer())
		stsInformers = append(stsInformers, factory.Apps().V1().StatefulSets().Informer())
	} else {
		// Probe which namespaces are accessible for each resource type
		type nsAccess struct {
			pods, deploys, sts bool
		}
		access := make(map[string]nsAccess)
		probeCtx, probeCancel := context.WithTimeout(ctx, 15*time.Second)
		for _, ns := range names {
			a := nsAccess{}
			if _, err := clientset.CoreV1().Pods(ns).List(probeCtx, metav1.ListOptions{Limit: 1}); err == nil {
				a.pods = true
			}
			if _, err := clientset.AppsV1().Deployments(ns).List(probeCtx, metav1.ListOptions{Limit: 1}); err == nil {
				a.deploys = true
			}
			if _, err := clientset.AppsV1().StatefulSets(ns).List(probeCtx, metav1.ListOptions{Limit: 1}); err == nil {
				a.sts = true
			}
			access[ns] = a
		}
		probeCancel()

		var podNS, deployNS, stsNS int

		// Node informer is always cluster-wide (nodes are not namespaced)
		nodeFactory := informers.NewSharedInformerFactory(clientset, noResync)
		factories = append(factories, nodeFactory)
		nodeInformer = nodeFactory.Core().V1().Nodes().Informer()

		for _, ns := range names {
			a := access[ns]
			if !a.pods && !a.deploys && !a.sts {
				continue
			}
			factory := informers.NewSharedInformerFactoryWithOptions(clientset, noResync,
				informers.WithNamespace(ns))
			factories = append(factories, factory)
			if a.pods {
				podInformers = append(podInformers, factory.Core().V1().Pods().Informer())
				podNS++
			}
			if a.deploys {
				deployInformers = append(deployInformers, factory.Apps().V1().Deployments().Informer())
				deployNS++
			}
			if a.sts {
				stsInformers = append(stsInformers, factory.Apps().V1().StatefulSets().Informer())
				stsNS++
			}
		}
		log.Printf("cache: RBAC restricted — pods: %d ns, deploys: %d ns, sts: %d ns (of %d total)", podNS, deployNS, stsNS, len(names))
	}

	// Debounced rebuild: coalesce rapid informer events
	c.rebuildCh = make(chan struct{}, 1)
	notify := func() {
		select {
		case c.rebuildCh <- struct{}{}:
		default: // already pending
		}
	}
	handler := cache.ResourceEventHandlerFuncs{
		AddFunc:    func(obj interface{}) { notify() },
		UpdateFunc: func(old, new interface{}) { notify() },
		DeleteFunc: func(obj interface{}) { notify() },
	}
	for _, inf := range podInformers {
		inf.AddEventHandler(handler)
	}
	nodeInformer.AddEventHandler(handler)
	for _, inf := range deployInformers {
		inf.AddEventHandler(handler)
	}
	for _, inf := range stsInformers {
		inf.AddEventHandler(handler)
	}

	// Debounce goroutine: rebuild at most every 500ms
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case <-c.rebuildCh:
				time.Sleep(500 * time.Millisecond)
				// Drain any extra signals
				select {
				case <-c.rebuildCh:
				default:
				}
				c.rebuildFromInformers(podInformers, nodeInformer, deployInformers, stsInformers)
			}
		}
	}()

	// Step 4: Start all informer factories
	c.setProgress(2, 4, "starting watches")
	for _, factory := range factories {
		factory.Start(stopCh)
	}

	// Wait for initial sync with timeout
	c.setProgress(3, 4, "syncing")
	syncCtx, syncCancel := context.WithTimeout(ctx, 30*time.Second)
	allSynced := true
	for _, factory := range factories {
		synced := factory.WaitForCacheSync(syncCtx.Done())
		for typ, ok := range synced {
			if !ok {
				log.Printf("cache: informer failed to sync: %v", typ)
				allSynced = false
			}
		}
	}
	syncCancel()

	if !allSynced {
		log.Printf("cache: some informers failed to sync, data may be incomplete")
	}

	// Build initial state from informer stores
	c.rebuildFromInformers(podInformers, nodeInformer, deployInformers, stsInformers)

	c.mu.Lock()
	c.liveDataLoaded = true
	c.mu.Unlock()
	c.setProgress(4, 4, "done")
	c.setError("")
	c.saveToDisk()

	log.Printf("cache: informers synced, live data ready")

	// Step 5: Metrics polling (metrics API doesn't support Watch)
	// Also periodically refresh namespace list and save to disk
	metricsTicker := time.NewTicker(10 * time.Second)
	nsTicker := time.NewTicker(30 * time.Second)
	saveTicker := time.NewTicker(60 * time.Second)
	defer metricsTicker.Stop()
	defer nsTicker.Stop()
	defer saveTicker.Stop()

	// Initial metrics fetch
	c.refreshMetrics(ctx)

	for {
		select {
		case <-ctx.Done():
			return
		case <-metricsTicker.C:
			c.refreshMetrics(ctx)
		case <-nsTicker.C:
			c.refreshNamespaces(ctx)
		case <-saveTicker.C:
			c.saveToDisk()
		}
	}
}

// rebuildFromInformers reads all informer stores and rebuilds the cached data.
func (c *Cache) rebuildFromInformers(
	podInformers []cache.SharedIndexInformer,
	nodeInformer cache.SharedIndexInformer,
	deployInformers []cache.SharedIndexInformer,
	stsInformers []cache.SharedIndexInformer,
) {
	// Rebuild pods
	var allPods []corev1.Pod
	for _, inf := range podInformers {
		for _, obj := range inf.GetStore().List() {
			if pod, ok := obj.(*corev1.Pod); ok {
				allPods = append(allPods, *pod)
			}
		}
	}
	pods := c.client.convertPods(allPods)
	// Stable sort so pod order doesn't shuffle between rebuilds
	sort.Slice(pods, func(i, j int) bool {
		if pods[i].Namespace != pods[j].Namespace {
			return pods[i].Namespace < pods[j].Namespace
		}
		return pods[i].Name < pods[j].Name
	})

	// Rebuild nodes
	var nodeList corev1.NodeList
	if nodeInformer != nil {
		for _, obj := range nodeInformer.GetStore().List() {
			if node, ok := obj.(*corev1.Node); ok {
				nodeList.Items = append(nodeList.Items, *node)
			}
		}
	}
	nodes := c.client.buildNodes(&nodeList)

	// Rebuild workload statuses
	workloads := c.buildWorkloadStatuses(deployInformers, stsInformers)

	c.mu.Lock()
	c.pods = pods
	c.nodes = nodes
	c.workloads = workloads
	c.ready = true
	c.mu.Unlock()
}

// buildWorkloadStatuses computes workload statuses from informer stores.
func (c *Cache) buildWorkloadStatuses(
	deployInformers []cache.SharedIndexInformer,
	stsInformers []cache.SharedIndexInformer,
) []WorkloadStatus {
	var statuses []WorkloadStatus

	for _, inf := range deployInformers {
		for _, obj := range inf.GetStore().List() {
			d, ok := obj.(*appsv1.Deployment)
			if !ok {
				continue
			}
			desired := int32(1)
			if d.Spec.Replicas != nil {
				desired = *d.Spec.Replicas
			}
			s := d.Status
			rolloutActive := false
			for _, cond := range s.Conditions {
				if cond.Type == appsv1.DeploymentProgressing && cond.Status == "True" && cond.Reason != "NewReplicaSetAvailable" {
					rolloutActive = true
				}
			}
			status := "stable"
			if s.UpdatedReplicas < desired || rolloutActive {
				status = "progressing"
			} else if s.ReadyReplicas < desired || s.AvailableReplicas < desired {
				status = "degraded"
			}
			if s.ReadyReplicas == 0 && desired > 0 {
				status = "degraded"
			}
			statuses = append(statuses, WorkloadStatus{
				Name:              d.Name,
				Namespace:         d.Namespace,
				Kind:              "Deployment",
				Replicas:          desired,
				ReadyReplicas:     s.ReadyReplicas,
				UpdatedReplicas:   s.UpdatedReplicas,
				AvailableReplicas: s.AvailableReplicas,
				RolloutStatus:     status,
			})
		}
	}

	for _, inf := range stsInformers {
		for _, obj := range inf.GetStore().List() {
			ss, ok := obj.(*appsv1.StatefulSet)
			if !ok {
				continue
			}
			desired := int32(1)
			if ss.Spec.Replicas != nil {
				desired = *ss.Spec.Replicas
			}
			s := ss.Status
			status := "stable"
			if s.CurrentRevision != s.UpdateRevision {
				status = "progressing"
			} else if s.UpdatedReplicas < desired || s.ReadyReplicas < desired {
				status = "degraded"
			}
			if s.ReadyReplicas == 0 && desired > 0 {
				status = "degraded"
			}
			statuses = append(statuses, WorkloadStatus{
				Name:              ss.Name,
				Namespace:         ss.Namespace,
				Kind:              "StatefulSet",
				Replicas:          desired,
				ReadyReplicas:     s.ReadyReplicas,
				UpdatedReplicas:   s.UpdatedReplicas,
				AvailableReplicas: s.ReadyReplicas,
				RolloutStatus:     status,
			})
		}
	}

	return statuses
}

func (c *Cache) refreshMetrics(ctx context.Context) {
	c.mu.RLock()
	names := c.namespaces
	c.mu.RUnlock()
	metrics := c.client.FetchAllMetrics(ctx, names)
	c.mu.Lock()
	c.metrics = metrics
	c.mu.Unlock()
}

func (c *Cache) refreshNamespaces(ctx context.Context) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	nsList, err := c.client.Clientset.CoreV1().Namespaces().List(ctx, metav1.ListOptions{})
	if err != nil {
		return
	}
	names := make([]string, 0, len(nsList.Items))
	for _, ns := range nsList.Items {
		names = append(names, ns.Name)
	}
	c.mu.Lock()
	c.namespaces = names
	c.mu.Unlock()
}
