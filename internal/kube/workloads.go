package kube

import (
	"context"
	"log"

	appsv1 "k8s.io/api/apps/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

type WorkloadStatus struct {
	Name              string `json:"name"`
	Namespace         string `json:"namespace"`
	Kind              string `json:"kind"`
	Replicas          int32  `json:"replicas"`
	ReadyReplicas     int32  `json:"readyReplicas"`
	UpdatedReplicas   int32  `json:"updatedReplicas"`
	AvailableReplicas int32  `json:"availableReplicas"`
	RolloutStatus     string `json:"rolloutStatus"` // "stable", "progressing", "degraded"
}

func (c *Client) FetchWorkloadStatuses(ctx context.Context) []WorkloadStatus {
	var statuses []WorkloadStatus

	// Fetch Deployments
	var allDeployments []appsv1.Deployment
	deps, err := c.Clientset.AppsV1().Deployments("").List(ctx, metav1.ListOptions{})
	if err == nil {
		allDeployments = deps.Items
	} else {
		log.Printf("workloads: cluster-wide deployment list failed (%v), trying per-namespace", err)
		nsList, nsErr := c.Clientset.CoreV1().Namespaces().List(ctx, metav1.ListOptions{})
		if nsErr == nil {
			for _, ns := range nsList.Items {
				d, dErr := c.Clientset.AppsV1().Deployments(ns.Name).List(ctx, metav1.ListOptions{})
				if dErr == nil {
					allDeployments = append(allDeployments, d.Items...)
				}
			}
		}
		log.Printf("workloads: fetched %d deployments via fallback", len(allDeployments))
	}

	for _, d := range allDeployments {
		desired := int32(1)
		if d.Spec.Replicas != nil {
			desired = *d.Spec.Replicas
		}
		s := d.Status
		// Check if Kubernetes considers the rollout still in progress
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

	// Fetch StatefulSets
	var allStatefulSets []appsv1.StatefulSet
	sss, err := c.Clientset.AppsV1().StatefulSets("").List(ctx, metav1.ListOptions{})
	if err == nil {
		allStatefulSets = sss.Items
	} else {
		log.Printf("workloads: cluster-wide statefulset list failed (%v), trying per-namespace", err)
		nsList, nsErr := c.Clientset.CoreV1().Namespaces().List(ctx, metav1.ListOptions{})
		if nsErr == nil {
			for _, ns := range nsList.Items {
				ss, ssErr := c.Clientset.AppsV1().StatefulSets(ns.Name).List(ctx, metav1.ListOptions{})
				if ssErr == nil {
					allStatefulSets = append(allStatefulSets, ss.Items...)
				}
			}
		}
		log.Printf("workloads: fetched %d statefulsets via fallback", len(allStatefulSets))
	}

	for _, ss := range allStatefulSets {
		desired := int32(1)
		if ss.Spec.Replicas != nil {
			desired = *ss.Spec.Replicas
		}
		s := ss.Status
		status := "stable"
		if s.CurrentRevision != s.UpdateRevision {
			// Actively rolling out — new revision not yet current
			status = "progressing"
		} else if s.UpdatedReplicas < desired || s.ReadyReplicas < desired {
			// Revision matches but not all replicas updated/ready — degraded
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

	return statuses
}
