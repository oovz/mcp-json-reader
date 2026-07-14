package app

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseConfigRequiresRootAndAcceptsEnvironmentDefault(t *testing.T) {
	if _, err := ParseConfig(nil, func(string) string { return "" }); err == nil {
		t.Fatal("ParseConfig succeeded without --root or MCP_JSON_ROOT")
	}
	config, err := ParseConfig([]string{"--max-depth", "64"}, func(name string) string {
		if name == "MCP_JSON_ROOT" {
			return `C:\workspace\data`
		}
		return ""
	})
	if err != nil {
		t.Fatalf("ParseConfig error: %v", err)
	}
	if config.Root != `C:\workspace\data` || config.Limits.MaxDepth != 64 {
		t.Fatalf("config = %#v", config)
	}
}

func TestParseConfigRejectsInconsistentLimits(t *testing.T) {
	_, err := ParseConfig([]string{"--root", ".", "--max-scalar-bytes", "4", "--max-number-bytes", "5"}, func(string) string { return "" })
	if err == nil || !strings.Contains(err.Error(), "max_number_bytes") {
		t.Fatalf("ParseConfig error = %v, want max_number_bytes validation", err)
	}
}

func TestRunVersionDoesNotRequireRoot(t *testing.T) {
	var stdout, stderr bytes.Buffer
	exitCode := Run(context.Background(), []string{"--version"}, &stdout, &stderr, func(string) string { return "" })
	if exitCode != 0 {
		t.Fatalf("Run exit = %d, stderr = %s", exitCode, stderr.String())
	}
	if got := strings.TrimSpace(stdout.String()); got != "mcp-json-reader (devel)" {
		t.Fatalf("version output = %q", got)
	}
	if stderr.Len() != 0 {
		t.Fatalf("version stderr = %q", stderr.String())
	}
}

func TestRunHelpDoesNotRequireRoot(t *testing.T) {
	var stdout, stderr bytes.Buffer
	exitCode := Run(context.Background(), []string{"--help"}, &stdout, &stderr, func(string) string { return "" })
	if exitCode != 0 {
		t.Fatalf("Run exit = %d, stderr = %s", exitCode, stderr.String())
	}
	for _, flag := range []string{"--root", "--max-scalar-bytes", "--max-concurrent-scans"} {
		if !strings.Contains(stdout.String(), flag) {
			t.Errorf("help does not contain %s: %s", flag, stdout.String())
		}
	}
	if stderr.Len() != 0 {
		t.Fatalf("help stderr = %q", stderr.String())
	}
}

func TestParseConfigRejectsInvalidFlagsAndNonPositiveLimits(t *testing.T) {
	tests := []struct {
		name string
		args []string
	}{
		{name: "unknown flag", args: []string{"--root", ".", "--unknown"}},
		{name: "missing flag value", args: []string{"--root"}},
		{name: "positional argument", args: []string{"--root", ".", "data.json"}},
	}
	for _, flagName := range []string{
		"--max-path-bytes", "--max-query-bytes", "--max-scalar-bytes", "--max-number-bytes",
		"--max-depth", "--max-object-members", "--max-object-key-bytes", "--max-active-key-bytes",
		"--max-record-bytes", "--max-candidate-bytes", "--max-result-bytes", "--max-items",
		"--probe-bytes", "--probe-records", "--max-open-files", "--max-cursors", "--max-concurrent-scans",
	} {
		tests = append(tests, struct {
			name string
			args []string
		}{name: flagName + " zero", args: []string{"--root", ".", flagName, "0"}})
	}
	for _, flagName := range []string{"--max-scan-time", "--handle-ttl", "--cursor-ttl"} {
		tests = append(tests, struct {
			name string
			args []string
		}{name: flagName + " zero", args: []string{"--root", ".", flagName, "0s"}})
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := ParseConfig(test.args, func(string) string { return "" }); err == nil {
				t.Fatalf("ParseConfig(%v) succeeded, want an error", test.args)
			}
		})
	}
}

func TestParseConfigRootFlagOverridesEnvironment(t *testing.T) {
	config, err := ParseConfig([]string{"--root", "flag-root"}, func(name string) string {
		if name == "MCP_JSON_ROOT" {
			return "environment-root"
		}
		return ""
	})
	if err != nil {
		t.Fatal(err)
	}
	if config.Root != "flag-root" {
		t.Fatalf("root = %q, want explicit flag to override environment", config.Root)
	}
}

func TestRunReportsConfigurationErrorsWithoutWritingProtocolOutput(t *testing.T) {
	for _, args := range [][]string{
		nil,
		{"--root", ".", "unexpected"},
		{"--root", ".", "--max-items", "0"},
		{"--unknown"},
	} {
		var stdout, stderr bytes.Buffer
		exitCode := Run(context.Background(), args, &stdout, &stderr, func(string) string { return "" })
		if exitCode != 2 {
			t.Errorf("Run(%v) exit = %d, want 2", args, exitCode)
		}
		if stdout.Len() != 0 {
			t.Errorf("Run(%v) stdout = %q, want empty protocol stream", args, stdout.String())
		}
		if !strings.HasPrefix(stderr.String(), "mcp-json-reader:") {
			t.Errorf("Run(%v) stderr = %q, want program prefix", args, stderr.String())
		}
	}
}

func TestRunReportsRootOpenFailure(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "missing")
	if _, err := os.Stat(missing); !os.IsNotExist(err) {
		t.Fatalf("test root unexpectedly exists: %v", err)
	}
	var stdout, stderr bytes.Buffer
	exitCode := Run(context.Background(), []string{"--root", missing}, &stdout, &stderr, func(string) string { return "" })
	if exitCode != 1 {
		t.Fatalf("Run exit = %d, stderr = %q, want 1", exitCode, stderr.String())
	}
	if stdout.Len() != 0 {
		t.Fatalf("stdout = %q, want empty protocol stream", stdout.String())
	}
	if !strings.Contains(stderr.String(), "cannot open root") {
		t.Fatalf("stderr = %q, want root-open diagnostic", stderr.String())
	}
}
