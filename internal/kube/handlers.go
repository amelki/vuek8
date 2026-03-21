package kube

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
)

type NodeInfo struct {
	Name           string `json:"name"`
	Status         string `json:"status"`
	Roles          string `json:"roles"`
	KubeletVersion string `json:"kubeletVersion"`
	OS             string `json:"os"`
	Arch           string `json:"arch"`
	CPUCapacity    string `json:"cpuCapacity"`
	MemoryCapacity string `json:"memoryCapacity"`
}

type ContainerInfo struct {
	Name   string `json:"name"`
	Image  string `json:"image"`
	Tag    string `json:"tag"`
	Status string `json:"status"`
}

type PodInfo struct {
	Name            string          `json:"name"`
	Namespace       string          `json:"namespace"`
	Status          string          `json:"status"`
	Ready           string          `json:"ready"`
	Restarts        int32           `json:"restarts"`
	CPURequestMilli int64           `json:"cpuRequestMilli"`
	CPULimitMilli   int64           `json:"cpuLimitMilli"`
	MemRequestBytes int64           `json:"memRequestBytes"`
	MemLimitBytes   int64           `json:"memLimitBytes"`
	Age          string          `json:"age"`
	Node         string          `json:"node"`
	Containers   []ContainerInfo `json:"containers"`
	WorkloadName string          `json:"workloadName"`
	WorkloadKind string          `json:"workloadKind"`
}

// --- Cache-based handlers (instant responses) ---

type CacheGetter func() *Cache

func HandleCachedNamespaces(getCache CacheGetter) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		c := getCache()
		if c == nil {
			json.NewEncoder(w).Encode([]string{})
			return
		}
		json.NewEncoder(w).Encode(c.GetNamespaces())
	}
}

func HandleCachedNodes(getCache CacheGetter) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		c := getCache()
		if c == nil {
			json.NewEncoder(w).Encode([]NodeInfo{})
			return
		}
		json.NewEncoder(w).Encode(c.GetNodes())
	}
}

func HandleCachedPods(getCache CacheGetter) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		c := getCache()
		if c == nil {
			json.NewEncoder(w).Encode([]PodInfo{})
			return
		}
		json.NewEncoder(w).Encode(c.GetPods())
	}
}

func HandleProgress(getCache CacheGetter) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		c := getCache()
		if c == nil {
			json.NewEncoder(w).Encode(Progress{})
			return
		}
		json.NewEncoder(w).Encode(c.GetProgress())
	}
}

func HandleCachedMetrics(getCache CacheGetter) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		c := getCache()
		if c == nil {
			json.NewEncoder(w).Encode([]PodMetrics{})
			return
		}
		json.NewEncoder(w).Encode(c.GetMetrics())
	}
}

// --- Data conversion (used by cache) ---

func (c *Client) buildNodes(nodeList *corev1.NodeList) []NodeInfo {
	nodes := make([]NodeInfo, 0, len(nodeList.Items))
	for _, node := range nodeList.Items {
		status := "NotReady"
		for _, cond := range node.Status.Conditions {
			if cond.Type == corev1.NodeReady && cond.Status == corev1.ConditionTrue {
				status = "Ready"
				break
			}
		}

		roles := ""
		for label := range node.Labels {
			if label == "node-role.kubernetes.io/master" || label == "node-role.kubernetes.io/control-plane" {
				roles = "control-plane"
			}
		}
		if roles == "" {
			roles = "worker"
		}

		nodes = append(nodes, NodeInfo{
			Name:           node.Name,
			Status:         status,
			Roles:          roles,
			KubeletVersion: node.Status.NodeInfo.KubeletVersion,
			OS:             node.Status.NodeInfo.OperatingSystem,
			Arch:           node.Status.NodeInfo.Architecture,
			CPUCapacity:    node.Status.Capacity.Cpu().String(),
			MemoryCapacity: formatMemory(node.Status.Capacity.Memory().Value()),
		})
	}
	return nodes
}

func (c *Client) convertPods(allPodItems []corev1.Pod) []PodInfo {
	pods := make([]PodInfo, 0, len(allPodItems))
	for _, pod := range allPodItems {
		csMap := make(map[string]string)
		for _, cs := range pod.Status.ContainerStatuses {
			if cs.State.Running != nil {
				csMap[cs.Name] = "Running"
			} else if cs.State.Waiting != nil {
				csMap[cs.Name] = cs.State.Waiting.Reason
			} else if cs.State.Terminated != nil {
				csMap[cs.Name] = cs.State.Terminated.Reason
			}
		}

		containers := make([]ContainerInfo, 0, len(pod.Spec.Containers))
		for _, c := range pod.Spec.Containers {
			image := c.Image
			tag := "latest"
			if i := strings.LastIndex(image, ":"); i != -1 {
				tag = image[i+1:]
				image = image[:i]
			}
			status := csMap[c.Name]
			if status == "" {
				status = "Waiting"
			}
			containers = append(containers, ContainerInfo{
				Name:   c.Name,
				Image:  image,
				Tag:    tag,
				Status: status,
			})
		}

		wName, wKind := workloadInfo(pod)

		// Sum CPU/memory requests and limits across all containers
		var cpuReq, cpuLim, memReq, memLim int64
		for _, c := range pod.Spec.Containers {
			if v, ok := c.Resources.Requests["cpu"]; ok {
				cpuReq += v.MilliValue()
			}
			if v, ok := c.Resources.Limits["cpu"]; ok {
				cpuLim += v.MilliValue()
			}
			if v, ok := c.Resources.Requests["memory"]; ok {
				memReq += v.Value()
			}
			if v, ok := c.Resources.Limits["memory"]; ok {
				memLim += v.Value()
			}
		}

		pods = append(pods, PodInfo{
			Name:            pod.Name,
			Namespace:       pod.Namespace,
			Status:          podStatus(pod),
			Ready:           podReady(pod),
			Restarts:        podRestarts(pod),
			CPURequestMilli: cpuReq,
			CPULimitMilli:   cpuLim,
			MemRequestBytes: memReq,
			MemLimitBytes:   memLim,
			Age:             formatAge(pod.CreationTimestamp.Time),
			Node:            pod.Spec.NodeName,
			Containers:      containers,
			WorkloadName:    wName,
			WorkloadKind:    wKind,
		})
	}

	return pods
}

func workloadInfo(pod corev1.Pod) (name, kind string) {
	if len(pod.OwnerReferences) == 0 {
		return pod.Name, "Pod"
	}
	owner := pod.OwnerReferences[0]
	switch owner.Kind {
	case "ReplicaSet":
		parts := strings.Split(owner.Name, "-")
		if len(parts) > 1 {
			return strings.Join(parts[:len(parts)-1], "-"), "Deployment"
		}
		return owner.Name, "Deployment"
	case "StatefulSet":
		return owner.Name, "StatefulSet"
	case "DaemonSet":
		return owner.Name, "DaemonSet"
	case "Job":
		return owner.Name, "Job"
	default:
		return owner.Name, owner.Kind
	}
}

func podStatus(pod corev1.Pod) string {
	if pod.DeletionTimestamp != nil {
		return "Terminating"
	}
	for _, cs := range pod.Status.ContainerStatuses {
		if cs.State.Waiting != nil {
			return cs.State.Waiting.Reason
		}
		if cs.State.Terminated != nil {
			return cs.State.Terminated.Reason
		}
	}
	for _, cs := range pod.Status.InitContainerStatuses {
		if cs.State.Waiting != nil {
			return "Init:" + cs.State.Waiting.Reason
		}
		if cs.State.Terminated != nil && cs.State.Terminated.ExitCode != 0 {
			return "Init:Error"
		}
	}
	return string(pod.Status.Phase)
}

func podReady(pod corev1.Pod) string {
	ready := 0
	total := len(pod.Spec.Containers)
	for _, cs := range pod.Status.ContainerStatuses {
		if cs.Ready {
			ready++
		}
	}
	return fmt.Sprintf("%d/%d", ready, total)
}

func podRestarts(pod corev1.Pod) int32 {
	var restarts int32
	for _, cs := range pod.Status.ContainerStatuses {
		restarts += cs.RestartCount
	}
	return restarts
}

func formatAge(t time.Time) string {
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%3ds", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%3dm", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%3dh", int(d.Hours()))
	default:
		return fmt.Sprintf("%3dd", int(d.Hours()/24))
	}
}

func formatMemory(bytes int64) string {
	gi := float64(bytes) / (1024 * 1024 * 1024)
	return fmt.Sprintf("%.1fGi", gi)
}
