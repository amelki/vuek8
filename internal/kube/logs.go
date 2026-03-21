package kube

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"time"

	corev1 "k8s.io/api/core/v1"
)

func HandleLogs(getCache CacheGetter) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		c := getCache()
		if c == nil {
			http.Error(w, "no active cluster", http.StatusServiceUnavailable)
			return
		}

		ns := r.URL.Query().Get("namespace")
		pod := r.URL.Query().Get("pod")
		container := r.URL.Query().Get("container")
		tailStr := r.URL.Query().Get("tail")

		if ns == "" || pod == "" {
			http.Error(w, "namespace and pod required", http.StatusBadRequest)
			return
		}

		tail := int64(100)
		if tailStr != "" {
			if n, err := strconv.ParseInt(tailStr, 10, 64); err == nil {
				tail = n
			}
		}

		ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
		defer cancel()

		opts := &corev1.PodLogOptions{
			TailLines: &tail,
		}
		if container != "" {
			opts.Container = container
		}

		req := c.client.Clientset.CoreV1().Pods(ns).GetLogs(pod, opts)
		stream, err := req.Stream(ctx)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		defer stream.Close()

		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		io.Copy(w, stream)
	}
}

// HandleLogsStream streams logs in real-time via SSE
func HandleLogsStream(getCache CacheGetter) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		c := getCache()
		if c == nil {
			http.Error(w, "no active cluster", http.StatusServiceUnavailable)
			return
		}

		flusher, ok := w.(http.Flusher)
		if !ok {
			http.Error(w, "streaming not supported", http.StatusInternalServerError)
			return
		}

		ns := r.URL.Query().Get("namespace")
		pod := r.URL.Query().Get("pod")
		container := r.URL.Query().Get("container")

		if ns == "" || pod == "" {
			http.Error(w, "namespace and pod required", http.StatusBadRequest)
			return
		}

		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")

		tail := int64(100)
		opts := &corev1.PodLogOptions{
			Follow:    true,
			TailLines: &tail,
		}
		if container != "" {
			opts.Container = container
		}

		ctx := r.Context()
		req := c.client.Clientset.CoreV1().Pods(ns).GetLogs(pod, opts)
		stream, err := req.Stream(ctx)
		if err != nil {
			fmt.Fprintf(w, "event: error\ndata: %s\n\n", err.Error())
			flusher.Flush()
			return
		}
		defer stream.Close()

		scanner := bufio.NewScanner(stream)
		scanner.Buffer(make([]byte, 0, 256*1024), 256*1024)

		for scanner.Scan() {
			select {
			case <-ctx.Done():
				return
			default:
				fmt.Fprintf(w, "data: %s\n\n", scanner.Text())
				flusher.Flush()
			}
		}
	}
}

// HandleLogsDownload returns full logs as a downloadable file
func HandleLogsDownload(getCache CacheGetter) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		c := getCache()
		if c == nil {
			http.Error(w, "no active cluster", http.StatusServiceUnavailable)
			return
		}

		ns := r.URL.Query().Get("namespace")
		pod := r.URL.Query().Get("pod")
		container := r.URL.Query().Get("container")

		if ns == "" || pod == "" {
			http.Error(w, "namespace and pod required", http.StatusBadRequest)
			return
		}

		ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
		defer cancel()

		opts := &corev1.PodLogOptions{}
		if container != "" {
			opts.Container = container
		}

		req := c.client.Clientset.CoreV1().Pods(ns).GetLogs(pod, opts)
		stream, err := req.Stream(ctx)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		defer stream.Close()

		filename := fmt.Sprintf("%s_%s.log", pod, container)
		if container == "" {
			filename = fmt.Sprintf("%s.log", pod)
		}

		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", filename))
		io.Copy(w, stream)
	}
}
