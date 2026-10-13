package app

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestParseWorkspaceConfiguration(t *testing.T) {
	workspaces := []string{t.TempDir(), t.TempDir()}
	data, err := json.Marshal(map[string]any{"workspaces": workspaces})
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	config, err := ParseConfig([]string{"--config", path}, func(string) string { return "" })
	if err != nil || !reflect.DeepEqual(config.Workspaces, workspaces) {
		t.Fatalf("config = %#v, %v", config, err)
	}
	for _, rootArgs := range [][]string{{"--root", workspaces[0]}, nil} {
		args := append([]string{"--config", path}, rootArgs...)
		_, err := ParseConfig(args, func(string) string {
			if rootArgs == nil {
				return workspaces[0]
			}
			return ""
		})
		if err == nil {
			t.Fatalf("ParseConfig(%v) accepted conflicting roots", args)
		}
	}
}

func TestInvalidWorkspaceConfigurationDoesNotFallBackToGlobal(t *testing.T) {
	for _, data := range []string{
		`{"workspaces":["relative"]}`, `{"workspaces":[""]}`, `{"workspace":[]}`,
		`{"workspaces":"wrong"}`, `{"workspaces":[1]}`, `[]`, `null`, `{`,
		`{}`, `{"workspaces":null}`, `{"Workspaces":[]}`,
		`{"workspaces":["/allowed"],"Workspaces":[]}`,
		`{"workspaces":[],"workspaces":[]}`, `{"workspaces":[]} {}`,
		`{"workspaces":[null]}`, `{"workspaces":["/\ud800"]}`,
		"{\"workspaces\":[\"/\xff\"]}",
		strings.Repeat(" ", (1<<20)+1),
	} {
		path := filepath.Join(t.TempDir(), "config.json")
		if err := os.WriteFile(path, []byte(data), 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := ParseConfig([]string{"--config", path}, func(string) string { return "" }); err == nil {
			t.Fatalf("accepted invalid configuration %.80q", data)
		}
	}
	for _, args := range [][]string{{"--config", ""}, {"--config", filepath.Join(t.TempDir(), "missing")}} {
		if _, err := ParseConfig(args, func(string) string { return "" }); err == nil {
			t.Fatalf("accepted invalid config arguments %v", args)
		}
	}
}

func TestEmptyWorkspaceListAllowsGlobalAccess(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte(`{"workspaces":[]}`), 0600); err != nil {
		t.Fatal(err)
	}
	config, err := ParseConfig([]string{"--config", path}, func(string) string { return "" })
	if err != nil || len(config.Workspaces) != 0 {
		t.Fatalf("config = %#v, %v", config, err)
	}
}

func TestMissingConfiguredWorkspaceStopsStartup(t *testing.T) {
	root := t.TempDir()
	configPath := filepath.Join(t.TempDir(), "config.json")
	data, err := json.Marshal(map[string]any{"workspaces": []string{root, filepath.Join(root, "missing")}})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(configPath, data, 0600); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	if code := Run(context.Background(), []string{"--config", configPath}, &stdout, &stderr, func(string) string { return "" }); code != 1 || stdout.Len() != 0 {
		t.Fatalf("startup = %d, stdout=%q, stderr=%q", code, stdout.String(), stderr.String())
	}
}
