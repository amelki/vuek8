package kube

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
)

func HandleDeletePod(getCache CacheGetter) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Namespace string `json:"namespace"`
			Pod       string `json:"pod"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		c := getCache()
		if c == nil {
			http.Error(w, "no active cluster", http.StatusServiceUnavailable)
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		err := c.GetClient().Clientset.CoreV1().Pods(req.Namespace).Delete(ctx, req.Pod, metav1.DeleteOptions{})
		if err != nil {
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"status": "deleted"})
	}
}

func HandleRestartWorkload(getCache CacheGetter) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Namespace    string `json:"namespace"`
			WorkloadName string `json:"workloadName"`
			WorkloadKind string `json:"workloadKind"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		c := getCache()
		if c == nil {
			http.Error(w, "no active cluster", http.StatusServiceUnavailable)
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		// Patch the template annotation to trigger a rollout restart
		// Same as: kubectl rollout restart deployment/X
		patch := fmt.Sprintf(`{"spec":{"template":{"metadata":{"annotations":{"kubectl.kubernetes.io/restartedAt":"%s"}}}}}`, time.Now().Format(time.RFC3339))
		patchBytes := []byte(patch)
		cs := c.GetClient().Clientset

		var err error
		switch req.WorkloadKind {
		case "Deployment":
			_, err = cs.AppsV1().Deployments(req.Namespace).Patch(ctx, req.WorkloadName, types.StrategicMergePatchType, patchBytes, metav1.PatchOptions{})
		case "StatefulSet":
			_, err = cs.AppsV1().StatefulSets(req.Namespace).Patch(ctx, req.WorkloadName, types.StrategicMergePatchType, patchBytes, metav1.PatchOptions{})
		case "DaemonSet":
			_, err = cs.AppsV1().DaemonSets(req.Namespace).Patch(ctx, req.WorkloadName, types.StrategicMergePatchType, patchBytes, metav1.PatchOptions{})
		default:
			err = fmt.Errorf("unsupported workload kind: %s", req.WorkloadKind)
		}

		if err != nil {
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"status": "restarting"})
	}
}

func HandleScaleWorkload(getCache CacheGetter) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Namespace    string `json:"namespace"`
			WorkloadName string `json:"workloadName"`
			WorkloadKind string `json:"workloadKind"`
			Replicas     int32  `json:"replicas"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		c := getCache()
		if c == nil {
			http.Error(w, "no active cluster", http.StatusServiceUnavailable)
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		patch := fmt.Sprintf(`{"spec":{"replicas":%d}}`, req.Replicas)
		patchBytes := []byte(patch)
		cs := c.GetClient().Clientset

		var err error
		switch req.WorkloadKind {
		case "Deployment":
			_, err = cs.AppsV1().Deployments(req.Namespace).Patch(ctx, req.WorkloadName, types.StrategicMergePatchType, patchBytes, metav1.PatchOptions{})
		case "StatefulSet":
			_, err = cs.AppsV1().StatefulSets(req.Namespace).Patch(ctx, req.WorkloadName, types.StrategicMergePatchType, patchBytes, metav1.PatchOptions{})
		default:
			err = fmt.Errorf("unsupported workload kind for scale: %s", req.WorkloadKind)
		}

		if err != nil {
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"status": "scaled"})
	}
}
