package app

import (
	"encoding/json/v2"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

func loadWorkspaces(path string) ([]string, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("cannot open configuration: %w", err)
	}
	defer func() { _ = file.Close() }()
	const maxConfigBytes = 1 << 20
	data, err := io.ReadAll(io.LimitReader(file, maxConfigBytes+1))
	if err != nil {
		return nil, fmt.Errorf("cannot read configuration: %w", err)
	}
	if len(data) > maxConfigBytes {
		return nil, fmt.Errorf("configuration exceeds %d bytes", maxConfigBytes)
	}
	var config struct {
		Workspaces []string `json:"workspaces"`
	}
	if err := json.Unmarshal(data, &config, json.RejectUnknownMembers(true)); err != nil {
		return nil, fmt.Errorf("invalid configuration: %w", err)
	}
	if config.Workspaces == nil {
		return nil, fmt.Errorf("configuration must contain a workspaces array")
	}
	for _, workspace := range config.Workspaces {
		if strings.TrimSpace(workspace) == "" || !filepath.IsAbs(workspace) {
			return nil, fmt.Errorf("configured workspaces must be absolute directory paths")
		}
	}
	return config.Workspaces, nil
}
