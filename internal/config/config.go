package config

import (
	"encoding/json"
	"os"
	"path/filepath"
)

type ClusterPrefs struct {
	DisplayName string `json:"displayName,omitempty"`
	Hidden      bool   `json:"hidden,omitempty"`
	Icon        string `json:"icon,omitempty"` // base64 data URL
}

type Settings struct {
	ShowAllContexts  bool     `json:"showAllContexts,omitempty"`
	SidebarCollapsed bool     `json:"sidebarCollapsed,omitempty"`
	ActiveCluster    string   `json:"activeCluster,omitempty"`
	ActiveTab        string   `json:"activeTab,omitempty"`
	Namespace        string   `json:"namespace,omitempty"`
	Workload         string   `json:"workload,omitempty"`
	PodSearch        string   `json:"podSearch,omitempty"`
	ColorMode        string   `json:"colorMode,omitempty"`
	TopoGroup        string   `json:"topoGroup,omitempty"`
	TopoLabel        string   `json:"topoLabel,omitempty"`
	ListGroup        string   `json:"listGroup,omitempty"`
	ClusterOrder     []string `json:"clusterOrder,omitempty"`
}

type Config struct {
	Clusters  map[string]ClusterPrefs `json:"clusters"`
	Settings  Settings                `json:"settings"`
	InstallID string                  `json:"installId,omitempty"`
}

func configPath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".config", "vuek8", "config.json")
}

func Load() (*Config, error) {
	cfg := &Config{Clusters: make(map[string]ClusterPrefs)}
	data, err := os.ReadFile(configPath())
	if err != nil {
		return cfg, nil // file doesn't exist yet, return empty
	}
	if err := json.Unmarshal(data, cfg); err != nil {
		return cfg, nil
	}
	if cfg.Clusters == nil {
		cfg.Clusters = make(map[string]ClusterPrefs)
	}
	return cfg, nil
}

func (c *Config) Save() error {
	p := configPath()
	if err := os.MkdirAll(filepath.Dir(p), 0755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	tmp := p + ".tmp"
	if err := os.WriteFile(tmp, data, 0644); err != nil {
		return err
	}
	return os.Rename(tmp, p)
}

func (c *Config) GetPrefs(id string) ClusterPrefs {
	return c.Clusters[id]
}

func (c *Config) SetDisplayName(id, name string) {
	prefs := c.Clusters[id]
	prefs.DisplayName = name
	c.Clusters[id] = prefs
}

func (c *Config) SetHidden(id string, hidden bool) {
	prefs := c.Clusters[id]
	prefs.Hidden = hidden
	c.Clusters[id] = prefs
}

func (c *Config) SetIcon(id, icon string) {
	prefs := c.Clusters[id]
	prefs.Icon = icon
	c.Clusters[id] = prefs
}

func (c *Config) GetSettings() Settings {
	return c.Settings
}

func (c *Config) UpdateSettings(s Settings) {
	c.Settings = s
}
