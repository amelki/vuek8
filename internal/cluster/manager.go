package cluster

import (
	"context"
	"fmt"
	"sync"

	"kglance/internal/config"
	"kglance/internal/kube"
)

type ClusterInfo struct {
	ID          string `json:"id"`
	DisplayName string `json:"displayName"`
	ContextName string `json:"contextName"`
	Server      string `json:"server"`
	FilePath    string `json:"filePath"`
	Hidden      bool   `json:"hidden"`
	Active      bool   `json:"active"`
	IsDefault   bool   `json:"isDefault"`
}

type Manager struct {
	mu          sync.RWMutex
	discovered  []DiscoveredCluster
	cfg         *config.Config
	activeID    string
	activeCache *kube.Cache
	cancelFn    context.CancelFunc
}

func NewManager(initialKubeconfig string) (*Manager, error) {
	cfg, _ := config.Load()
	discovered := Discover()

	mgr := &Manager{
		discovered: discovered,
		cfg:        cfg,
	}

	// Pick initial cluster
	initialID := ""
	if initialKubeconfig != "" {
		// Find matching discovered cluster
		for _, d := range discovered {
			if d.KubeconfigPath == initialKubeconfig {
				initialID = d.ID
				break
			}
		}
		// If not found in discovery, add it manually
		if initialID == "" && len(discovered) > 0 {
			initialID = discovered[0].ID
		}
	} else if len(discovered) > 0 {
		// Pick first non-hidden cluster
		for _, d := range discovered {
			if !cfg.GetPrefs(d.ID).Hidden {
				initialID = d.ID
				break
			}
		}
		if initialID == "" {
			initialID = discovered[0].ID
		}
	}

	if initialID != "" {
		if err := mgr.SwitchTo(initialID); err != nil {
			// Try to start anyway, just without a working cache
			fmt.Printf("Warning: failed to connect to initial cluster: %v\n", err)
		}
	}

	return mgr, nil
}

func (m *Manager) ListClusters() []ClusterInfo {
	m.mu.RLock()
	defer m.mu.RUnlock()

	infos := make([]ClusterInfo, 0, len(m.discovered))
	for _, d := range m.discovered {
		prefs := m.cfg.GetPrefs(d.ID)
		displayName := prefs.DisplayName
		if displayName == "" {
			displayName = d.ContextName
		}
		infos = append(infos, ClusterInfo{
			ID:          d.ID,
			DisplayName: displayName,
			ContextName: d.ContextName,
			Server:      d.ClusterServer,
			FilePath:    d.KubeconfigPath,
			Hidden:      prefs.Hidden,
			Active:      d.ID == m.activeID,
			IsDefault:   d.IsDefault,
		})
	}
	return infos
}

func (m *Manager) ActiveID() string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.activeID
}

func (m *Manager) GetCache() *kube.Cache {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.activeCache
}

func (m *Manager) SwitchTo(id string) error {
	// Find the cluster
	var target *DiscoveredCluster
	for _, d := range m.discovered {
		if d.ID == id {
			d := d
			target = &d
			break
		}
	}
	if target == nil {
		return fmt.Errorf("cluster not found: %s", id)
	}

	// Create new client
	client, err := kube.NewClientWithContext(target.KubeconfigPath, target.ContextName)
	if err != nil {
		return fmt.Errorf("failed to create client: %w", err)
	}

	m.mu.Lock()
	// Stop old cache
	if m.cancelFn != nil {
		m.cancelFn()
	}

	// Start new cache
	ctx, cancel := context.WithCancel(context.Background())
	cache := kube.NewCache(client)
	m.activeCache = cache
	m.activeID = id
	m.cancelFn = cancel
	m.mu.Unlock()

	cache.Start(ctx)
	return nil
}

func (m *Manager) Rename(id, displayName string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.cfg.SetDisplayName(id, displayName)
	return m.cfg.Save()
}

func (m *Manager) SetHidden(id string, hidden bool) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.cfg.SetHidden(id, hidden)
	return m.cfg.Save()
}
