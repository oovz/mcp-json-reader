package app

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/oovz/mcp-json-reader/v3/internal/core"
	"github.com/oovz/mcp-json-reader/v3/internal/mcpserver"
	"github.com/oovz/mcp-json-reader/v3/internal/service"
	"github.com/oovz/mcp-json-reader/v3/internal/source"
)

type Config struct {
	Workspaces []string
	Version    bool
	Limits     core.Limits
}

const maxMCPFrameBytes = 16 * 1024 * 1024

func stdioTransport() *mcpserver.StdioTransport {
	return &mcpserver.StdioTransport{
		Reader: newBoundedStdioReader(os.Stdin, maxMCPFrameBytes),
		Writer: noCloseWriter{Writer: os.Stdout},
	}
}

func ParseConfig(arguments []string, getenv func(string) string) (Config, error) {
	limits := core.DefaultLimits()
	config := Config{Limits: limits}
	root := getenv("MCP_JSON_ROOT")
	var configFile string
	flags := flag.NewFlagSet("mcp-json-reader", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	flags.StringVar(&root, "root", root, "filesystem root available to the MCP server")
	flags.StringVar(&configFile, "config", "", "JSON file containing allowed workspace directories")
	flags.BoolVar(&config.Version, "version", false, "print version and exit")
	flags.Int64Var(&config.Limits.MaxPathBytes, "max-path-bytes", limits.MaxPathBytes, "maximum encoded bytes in a source path")
	flags.Int64Var(&config.Limits.MaxQueryBytes, "max-query-bytes", limits.MaxQueryBytes, "maximum encoded bytes in a query expression")
	flags.Int64Var(&config.Limits.MaxScalarBytes, "max-scalar-bytes", limits.MaxScalarBytes, "maximum encoded bytes in a JSON string")
	flags.Int64Var(&config.Limits.MaxNumberBytes, "max-number-bytes", limits.MaxNumberBytes, "maximum encoded bytes in a JSON number")
	flags.IntVar(&config.Limits.MaxDepth, "max-depth", limits.MaxDepth, "maximum JSON container nesting depth")
	flags.IntVar(&config.Limits.MaxObjectMembers, "max-object-members", limits.MaxObjectMembers, "maximum members in one object")
	flags.Int64Var(&config.Limits.MaxObjectKeyBytes, "max-object-key-bytes", limits.MaxObjectKeyBytes, "maximum retained member-name bytes per object")
	flags.Int64Var(&config.Limits.MaxActiveKeyBytes, "max-active-key-bytes", limits.MaxActiveKeyBytes, "maximum retained member-name bytes across active objects")
	flags.Int64Var(&config.Limits.MaxRecordBytes, "max-record-bytes", limits.MaxRecordBytes, "maximum bytes in one JSONL or JSON-sequence record")
	flags.Int64Var(&config.Limits.MaxCandidateBytes, "max-candidate-bytes", limits.MaxCandidateBytes, "maximum bytes in one matched or filter candidate")
	flags.Int64Var(&config.Limits.MaxResultBytes, "max-result-bytes", limits.MaxResultBytes, "maximum serialized json_read structuredContent bytes")
	flags.IntVar(&config.Limits.MaxItems, "max-items", limits.MaxItems, "maximum matches returned per page")
	flags.DurationVar(&config.Limits.MaxScanTime, "max-scan-time", limits.MaxScanTime, "maximum request duration, including scan admission")
	flags.Int64Var(&config.Limits.ProbeBytes, "probe-bytes", limits.ProbeBytes, "maximum prefix bytes examined by probe validation")
	flags.IntVar(&config.Limits.ProbeRecords, "probe-records", limits.ProbeRecords, "maximum complete records examined by probe validation")
	flags.IntVar(&config.Limits.MaxOpenFiles, "max-open-files", limits.MaxOpenFiles, "maximum process-scoped file handles")
	flags.IntVar(&config.Limits.MaxCursors, "max-cursors", limits.MaxCursors, "maximum stored pagination cursors")
	flags.IntVar(&config.Limits.MaxConcurrentScans, "max-concurrent-scans", limits.MaxConcurrentScans, "maximum concurrent parser scans")
	flags.DurationVar(&config.Limits.HandleTTL, "handle-ttl", limits.HandleTTL, "idle lifetime of a file handle")
	flags.DurationVar(&config.Limits.CursorTTL, "cursor-ttl", limits.CursorTTL, "idle lifetime of a pagination cursor")
	if err := flags.Parse(arguments); err != nil {
		return Config{}, err
	}
	if flags.NArg() != 0 {
		return Config{}, fmt.Errorf("unexpected positional arguments: %s", strings.Join(flags.Args(), " "))
	}
	if config.Version {
		return config, nil
	}
	if err := config.Limits.Validate(); err != nil {
		return Config{}, err
	}
	var explicitRoot, explicitConfig bool
	flags.Visit(func(value *flag.Flag) {
		explicitRoot = explicitRoot || value.Name == "root"
		explicitConfig = explicitConfig || value.Name == "config"
	})
	if (explicitRoot || root != "") && strings.TrimSpace(root) == "" {
		return Config{}, errors.New("--root or MCP_JSON_ROOT must name a directory")
	}
	if explicitConfig {
		if strings.TrimSpace(configFile) == "" {
			return Config{}, errors.New("--config must name a file")
		}
		if root != "" {
			return Config{}, errors.New("--config cannot be combined with --root or MCP_JSON_ROOT")
		}
		workspaces, err := loadWorkspaces(configFile)
		if err != nil {
			return Config{}, err
		}
		config.Workspaces = workspaces
	} else if root != "" {
		config.Workspaces = []string{root}
	}
	return config, nil
}

func Run(ctx context.Context, arguments []string, stdout, stderr io.Writer, getenv func(string) string) int {
	config, err := ParseConfig(arguments, getenv)
	if err != nil {
		if errors.Is(err, flag.ErrHelp) {
			_, _ = io.WriteString(stdout, usageText)
			return 0
		}
		_, _ = fmt.Fprintln(stderr, "mcp-json-reader:", err)
		return 2
	}
	if config.Version {
		_, _ = fmt.Fprintf(stdout, "mcp-json-reader %s\n", mcpserver.CurrentVersion())
		return 0
	}
	manager, err := source.NewManager(config.Workspaces, config.Limits, source.ManagerOptions{})
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "mcp-json-reader: cannot open workspace:", err)
		return 1
	}
	defer func() { _ = manager.Shutdown() }()
	server := mcpserver.New(service.New(manager, config.Limits, service.Options{}))
	if err := server.Run(ctx, stdioTransport()); err != nil && !errors.Is(err, context.Canceled) {
		_, _ = fmt.Fprintln(stderr, "mcp-json-reader:", err)
		return 1
	}
	return 0
}

const usageText = `Usage: mcp-json-reader [options]

Without --root, MCP_JSON_ROOT, or configured workspaces, the server can read
any regular file permitted by the operating system. Relative paths use the
process working directory. With one workspace, paths are relative to it;
with multiple workspaces, source paths must be absolute.

Options:
  --root PATH                  restrict access to one directory (or MCP_JSON_ROOT)
  --config FILE                JSON configuration: {"workspaces":["/absolute/path"]}
  --version                    print version and exit
  --max-path-bytes N           maximum encoded source-path bytes
  --max-query-bytes N          maximum encoded query bytes
  --max-scalar-bytes N         maximum encoded string-token bytes
  --max-number-bytes N         maximum encoded number-token bytes
  --max-depth N                maximum JSON container depth
  --max-object-members N       maximum members in one object
  --max-object-key-bytes N     maximum retained key bytes per object
  --max-active-key-bytes N     maximum retained key bytes across active objects
  --max-record-bytes N         maximum JSONL or JSON-sequence record bytes
  --max-candidate-bytes N      maximum bytes in one match/filter candidate
  --max-result-bytes N         maximum serialized json_read structuredContent bytes
  --max-items N                maximum matches per page
  --max-scan-time DURATION     maximum request duration, including scan admission
  --probe-bytes N              bounded probe prefix
  --probe-records N            bounded complete records per probe
  --max-open-files N           maximum process-scoped file handles
  --max-cursors N              maximum stored pagination cursors
  --max-concurrent-scans N     maximum concurrent parser scans
  --handle-ttl DURATION        idle handle lifetime
  --cursor-ttl DURATION        idle cursor lifetime
  --help                       show this help
`
