package kube

import (
	"context"

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
	deployments, err := c.Clientset.AppsV1().Deployments("").List(ctx, metav1.ListOptions{})
	if err == nil {
		for _, d := range deployments.Items {
			desired := int32(1)
			if d.Spec.Replicas != nil {
				desired = *d.Spec.Replicas
			}
			s := d.Status
			status := "stable"
			if s.UpdatedReplicas < desired || s.ReadyReplicas < s.UpdatedReplicas || s.AvailableReplicas < desired {
				status = "progressing"
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

	// Fetch StatefulSets
	statefulsets, err := c.Clientset.AppsV1().StatefulSets("").List(ctx, metav1.ListOptions{})
	if err == nil {
		for _, ss := range statefulsets.Items {
			desired := int32(1)
			if ss.Spec.Replicas != nil {
				desired = *ss.Spec.Replicas
			}
			s := ss.Status
			status := "stable"
			if s.UpdatedReplicas < desired || s.ReadyReplicas < desired {
				status = "progressing"
			}
			if s.CurrentRevision != s.UpdateRevision {
				status = "progressing"
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
				AvailableReplicas: s.ReadyReplicas, // StatefulSets don't have AvailableReplicas
				RolloutStatus:     status,
			})
		}
	}

	return statuses
}
