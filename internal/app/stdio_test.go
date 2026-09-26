package app

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

// Exercise the real stdio transport in a subprocess so test-runner output can
// never contaminate MCP's stdout stream. The external deadline covers all I/O.
func TestStdioRoundTrip(t *testing.T) {
	if root := os.Getenv("MCP_JSON_STDIO_TEST_ROOT"); root != "" {
		os.Exit(Run(context.Background(), []string{"--root", root}, os.Stdout, os.Stderr, os.Getenv))
	}
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "data.json"), []byte(`[{"id":1},{"id":2}]`), 0600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestStdioRoundTrip$")
	command.Env = append(os.Environ(), "MCP_JSON_STDIO_TEST_ROOT="+root)
	input, err := command.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	output, err := command.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	command.Stderr = os.Stderr
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = input.Close(); _ = command.Wait() }()
	decoder := json.NewDecoder(bufio.NewReader(output))
	sendRaw := func(raw string) {
		t.Helper()
		// Legal JSON whitespace must survive the composed SDK transport.
		if _, err := fmt.Fprintf(input, " \t%s \t\r\n", raw); err != nil {
			t.Fatal(err)
		}
	}
	send := func(raw string) {
		t.Helper()
		var request map[string]any
		if err := json.Unmarshal([]byte(raw), &request); err != nil {
			t.Fatal(err)
		}
		params, ok := request["params"].(map[string]any)
		if !ok {
			params = map[string]any{}
			request["params"] = params
		}
		params["_meta"] = map[string]any{
			"io.modelcontextprotocol/protocolVersion":    "2026-07-28",
			"io.modelcontextprotocol/clientCapabilities": map[string]any{},
			"io.modelcontextprotocol/clientInfo":         map[string]any{"name": "stdio-contract-test", "version": "1"},
		}
		encoded, err := json.Marshal(request)
		if err != nil {
			t.Fatal(err)
		}
		sendRaw(string(encoded))
	}
	receiveResponse := func(id float64) map[string]any {
		t.Helper()
		for {
			var response map[string]any
			if err := decoder.Decode(&response); err != nil {
				t.Fatalf("invalid stdio response: %v", err)
			}
			if response["id"] == nil {
				continue
			} // A protocol notification.
			if response["id"] != id {
				t.Fatalf("unexpected response: %#v", response)
			}
			return response
		}
	}
	receive := func(id float64) map[string]any {
		t.Helper()
		response := receiveResponse(id)
		if response["error"] != nil {
			t.Fatalf("protocol error: %#v", response)
		}
		result, ok := response["result"].(map[string]any)
		if !ok {
			t.Fatalf("missing result: %#v", response)
		}
		return result
	}
	assertProtocolError := func(id float64, label string) {
		t.Helper()
		response := receiveResponse(id)
		if response["error"] == nil {
			t.Fatalf("%s unexpectedly succeeded: %#v", label, response)
		}
	}
	send(`{"jsonrpc":"2.0","id":1,"method":"server/discover","params":{}}`)
	discovered := receive(1)
	versions, ok := discovered["supportedVersions"].([]any)
	if !ok || len(versions) != 1 || !slices.Equal(versions, []any{"2026-07-28"}) {
		t.Fatalf("discovery=%#v", discovered)
	}
	send(`{"jsonrpc":"2.0","id":2,"method":"tools/list","params":{}}`)
	listed := receive(2)
	tools, ok := listed["tools"].([]any)
	if !ok || len(tools) != 3 {
		t.Fatalf("tools=%#v", listed)
	}
	send(`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"json_read","arguments":{"path":"data.json","language":"jsonpath","query":"$[*].id","max_items":1,"max_result_bytes":512}}}`)
	first := receive(3)
	encodedResult, err := json.Marshal(first["structuredContent"])
	if err != nil || len(encodedResult) > 512 {
		t.Fatalf("result budget: %d bytes, %v", len(encodedResult), err)
	}
	if content, ok := first["content"].([]any); !ok || len(content) != 0 {
		t.Fatalf("content=%#v", first)
	}
	structured, ok := first["structuredContent"].(map[string]any)
	if !ok {
		t.Fatalf("structured=%#v", first)
	}
	cursor, ok := structured["next_cursor"].(string)
	if !ok || cursor == "" {
		t.Fatalf("cursor=%#v", structured)
	}
	args, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 4, "method": "tools/call", "params": map[string]any{"name": "json_read", "arguments": map[string]any{"cursor": cursor}}})
	send(string(args))
	second := receive(4)
	structured = second["structuredContent"].(map[string]any)
	if structured["complete"] != true {
		t.Fatalf("last page=%#v", structured)
	}
	send(`{"jsonrpc":"2.0","id":5,"method":"tools/call","params":{"name":"json_open","arguments":{"path":"data.json","PATH":"other.json"}}}`)
	failed := receive(5)
	structured, ok = failed["structuredContent"].(map[string]any)
	if failed["isError"] != true || !ok || structured["code"] != "INVALID_ARGUMENT" {
		t.Fatalf("invalid schema result=%#v", failed)
	}
	// The 2026-07-28 lifecycle uses discovery and per-request metadata. Verify
	// the connection remains usable before exercising legacy-request rejection.
	send(`{"jsonrpc":"2.0","id":6,"method":"server/discover","params":{}}`)
	repeatedDiscovery := receive(6)
	if supported, ok := repeatedDiscovery["supportedVersions"].([]any); !ok || !slices.Equal(supported, []any{"2026-07-28"}) {
		t.Fatalf("repeat discovery=%#v", repeatedDiscovery)
	}
	sendRaw(`{"jsonrpc":"2.0","id":7,"method":"ping","params":{}}`)
	assertProtocolError(7, "legacy ping")
	sendRaw(`{"jsonrpc":"2.0","id":8,"method":"tools/list","params":{}}`)
	assertProtocolError(8, "request without protocol metadata")
	sendRaw(`{"jsonrpc":"2.0","id":9,"method":"tools/call","params":{"_meta":{"io.modelcontextprotocol/protocolVersion":"2026-07-28","io.modelcontextprotocol/clientCapabilities":{}},"name":"json_open","arguments":{"path":"data.json","path":"other.json"}}}`)
	duplicate := receive(9)
	structured, ok = duplicate["structuredContent"].(map[string]any)
	if duplicate["isError"] != true || !ok || structured["code"] != "INVALID_ARGUMENT" {
		t.Fatalf("duplicate arguments result=%#v", duplicate)
	}
	if err := input.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestStdioTransportUsesProjectFrameLimit(t *testing.T) {
	transport := stdioTransport()
	reader, ok := transport.Reader.(*boundedStdioReader)
	if !ok || reader.limit != maxMCPFrameBytes {
		t.Fatalf("reader=%#v, want bounded reader with %d-byte limit", transport.Reader, maxMCPFrameBytes)
	}
}

func TestStdioRejectsLegacyInitialize(t *testing.T) {
	if root := os.Getenv("MCP_JSON_LEGACY_TEST_ROOT"); root != "" {
		os.Exit(Run(context.Background(), []string{"--root", root}, os.Stdout, os.Stderr, os.Getenv))
	}
	root := t.TempDir()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestStdioRejectsLegacyInitialize$")
	command.Env = append(os.Environ(), "MCP_JSON_LEGACY_TEST_ROOT="+root)
	var stderr bytes.Buffer
	command.Stderr = &stderr
	input, err := command.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	output, err := command.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = input.Close(); _ = command.Wait() }()
	if _, err := fmt.Fprintln(input, `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-11-25","capabilities":{},"clientInfo":{"name":"legacy","version":"1"}}}`); err != nil {
		t.Fatal(err)
	}
	var response map[string]any
	if err := json.NewDecoder(bufio.NewReader(output)).Decode(&response); err != nil {
		t.Fatalf("legacy initialize response: %v; stderr=%s", err, stderr.String())
	}
	if response["error"] == nil {
		t.Fatalf("legacy initialize unexpectedly succeeded: %#v", response)
	}
	if _, err := fmt.Fprintln(input, `{"jsonrpc":"2.0","id":2,"method":"tools/list","params":{}}`); err != nil {
		t.Fatal(err)
	}
	if err := json.NewDecoder(bufio.NewReader(output)).Decode(&response); err != nil {
		t.Fatalf("legacy tools/list response: %v; stderr=%s", err, stderr.String())
	}
	if response["error"] == nil {
		t.Fatalf("legacy initialize established a usable session: %#v", response)
	}
	if err := input.Close(); err != nil {
		t.Fatal(err)
	}
	if err := command.Wait(); err != nil {
		t.Fatalf("legacy subprocess: %v; stderr=%s", err, stderr.String())
	}
}

func TestStdioRejectsOversizedFrame(t *testing.T) {
	if root := os.Getenv("MCP_JSON_FRAME_TEST_ROOT"); root != "" {
		os.Exit(Run(context.Background(), []string{"--root", root}, os.Stdout, os.Stderr, os.Getenv))
	}
	root := t.TempDir()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestStdioRejectsOversizedFrame$")
	command.Env = append(os.Environ(), "MCP_JSON_FRAME_TEST_ROOT="+root)
	var stderr bytes.Buffer
	command.Stderr = &stderr
	input, err := command.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := command.StdoutPipe(); err != nil {
		t.Fatal(err)
	}
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	frame := `{"jsonrpc":"2.0","id":1,"method":"server/discover","params":{"_meta":{"io.modelcontextprotocol/protocolVersion":"2026-07-28","io.modelcontextprotocol/clientCapabilities":{},"io.modelcontextprotocol/clientInfo":{"name":"frame-test","version":"1"}},"padding":"` + strings.Repeat("x", maxMCPFrameBytes) + `"}}`
	if _, err := fmt.Fprintln(input, frame); err != nil {
		t.Fatal(err)
	}
	if err := input.Close(); err != nil {
		t.Fatal(err)
	}
	if err := command.Wait(); err == nil {
		t.Fatalf("oversized frame unexpectedly succeeded; stderr=%s", stderr.String())
	}
	if !strings.Contains(strings.ToLower(stderr.String()), "line length") && !strings.Contains(strings.ToLower(stderr.String()), "frame") {
		t.Fatalf("oversized frame error lacks bounded-transport diagnostic: %s", stderr.String())
	}
}

func TestStdioRejectsOversizedSubsequentFrame(t *testing.T) {
	if root := os.Getenv("MCP_JSON_FRAME_SEQUENCE_TEST_ROOT"); root != "" {
		os.Exit(Run(context.Background(), []string{"--root", root}, os.Stdout, os.Stderr, os.Getenv))
	}
	root := t.TempDir()
	inputPath := filepath.Join(t.TempDir(), "frames.jsonl")
	first := `{"jsonrpc":"2.0","method":"notifications/progress","params":{"progressToken":"primer","progress":1,"total":2,"padding":"` + strings.Repeat("x", 8*1024*1024) + `"}}`
	second := `{"jsonrpc":"2.0","method":"notifications/progress","params":{"progressToken":"oversized","progress":2,"total":2,"padding":"` + strings.Repeat("x", maxMCPFrameBytes) + `"}}`
	if err := os.WriteFile(inputPath, []byte(first+"\n"+second+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	input, err := os.Open(inputPath)
	if err != nil {
		t.Fatal(err)
	}
	defer input.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestStdioRejectsOversizedSubsequentFrame$")
	command.Env = append(os.Environ(), "MCP_JSON_FRAME_SEQUENCE_TEST_ROOT="+root)
	command.Stdin = input
	command.Stdout = io.Discard
	var stderr bytes.Buffer
	command.Stderr = &stderr
	if err := command.Run(); err == nil {
		t.Fatalf("oversized subsequent frame unexpectedly succeeded; stderr=%s", stderr.String())
	}
	if !strings.Contains(strings.ToLower(stderr.String()), "line length") && !strings.Contains(strings.ToLower(stderr.String()), "frame") {
		t.Fatalf("oversized subsequent frame error lacks bounded-transport diagnostic: %s", stderr.String())
	}
}

func TestStdioAcceptsNearLimitMixedDelimiters(t *testing.T) {
	if root := os.Getenv("MCP_JSON_MIXED_FRAME_TEST_ROOT"); root != "" {
		os.Exit(Run(context.Background(), []string{"--root", root}, os.Stdout, os.Stderr, os.Getenv))
	}
	root := t.TempDir()
	frame := func(token string, padding int) string {
		prefix := `{"jsonrpc":"2.0","method":"notifications/progress","params":{"_meta":{"io.modelcontextprotocol/protocolVersion":"2026-07-28","io.modelcontextprotocol/clientCapabilities":{},"io.modelcontextprotocol/clientInfo":{"name":"mixed-frame-test","version":"1"}},"progressToken":"` + token + `","progress":1,"total":2,"padding":"`
		suffix := `"}}`
		if padding < 0 {
			t.Fatalf("negative padding: %d", padding)
		}
		return prefix + strings.Repeat("x", padding) + suffix
	}
	first := frame("primer", 512)
	secondPrefix := frame("near-limit", 0)
	secondPadding := maxMCPFrameBytes - 1 - len(secondPrefix)
	second := frame("near-limit", secondPadding)
	if len(second) != maxMCPFrameBytes-1 {
		t.Fatalf("second JSON bytes=%d, want %d", len(second), maxMCPFrameBytes-1)
	}
	inputPath := filepath.Join(t.TempDir(), "frames.jsonl")
	if err := os.WriteFile(inputPath, []byte(first+"\r\n"+second+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	input, err := os.Open(inputPath)
	if err != nil {
		t.Fatal(err)
	}
	defer input.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestStdioAcceptsNearLimitMixedDelimiters$")
	command.Env = append(os.Environ(), "MCP_JSON_MIXED_FRAME_TEST_ROOT="+root)
	command.Stdin = input
	command.Stdout = io.Discard
	var stderr bytes.Buffer
	command.Stderr = &stderr
	if err := command.Run(); err != nil {
		t.Fatalf("valid mixed-delimiter frames were rejected: %v; stderr=%s", err, stderr.String())
	}
}

func TestStdioRejectsMalformedPhysicalFrames(t *testing.T) {
	if root := os.Getenv("MCP_JSON_MALFORMED_FRAME_TEST_ROOT"); root != "" {
		os.Exit(Run(context.Background(), []string{"--root", root}, os.Stdout, os.Stderr, os.Getenv))
	}
	request := `{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{"_meta":{"io.modelcontextprotocol/protocolVersion":"2026-07-28","io.modelcontextprotocol/clientCapabilities":{}}}}`
	for _, test := range []struct{ name, input string }{
		{"multiline", "{\n" + strings.Repeat(strings.Repeat(" ", 1024)+"\n", 17408) + request[1:] + "\n"},
		{"multiple values", request + " " + request + "\n"},
		{"batch", "[" + request + "]\n"},
		{"oversized suffix", request + strings.Repeat(" ", maxMCPFrameBytes) + "\n"},
	} {
		t.Run(test.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
			defer cancel()
			command := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestStdioRejectsMalformedPhysicalFrames$")
			command.Env = append(os.Environ(), "MCP_JSON_MALFORMED_FRAME_TEST_ROOT="+t.TempDir())
			command.Stdin = strings.NewReader(test.input)
			var stdout, stderr bytes.Buffer
			command.Stdout, command.Stderr = &stdout, &stderr
			if err := command.Run(); err == nil {
				t.Fatalf("malformed frame accepted; stdout=%s stderr=%s", stdout.String(), stderr.String())
			}
			if ctx.Err() != nil || stdout.Len() != 0 || !strings.Contains(stderr.String(), "frame") {
				t.Fatalf("frame was not rejected before dispatch: context=%v stdout=%s stderr=%s", ctx.Err(), stdout.String(), stderr.String())
			}
		})
	}
}

func TestStdioRequestMetadataErrorsAndRecovery(t *testing.T) {
	if root := os.Getenv("MCP_JSON_METADATA_TEST_ROOT"); root != "" {
		os.Exit(Run(context.Background(), []string{"--root", root}, os.Stdout, os.Stderr, os.Getenv))
	}
	for _, test := range []struct {
		name, params string
		code         float64
	}{
		{"missing meta", `{}`, -32602},
		{"null params", `null`, -32602},
		{"null meta", `{"_meta":null}`, -32602},
		{"array meta", `{"_meta":[]}`, -32602},
		{"missing version", `{"_meta":{"io.modelcontextprotocol/clientCapabilities":{}}}`, -32602},
		{"numeric version", `{"_meta":{"io.modelcontextprotocol/protocolVersion":7,"io.modelcontextprotocol/clientCapabilities":{}}}`, -32602},
		{"null version", `{"_meta":{"io.modelcontextprotocol/protocolVersion":null,"io.modelcontextprotocol/clientCapabilities":{}}}`, -32602},
		{"missing capabilities", `{"_meta":{"io.modelcontextprotocol/protocolVersion":"2026-07-28"}}`, -32602},
		{"null capabilities", `{"_meta":{"io.modelcontextprotocol/protocolVersion":"2026-07-28","io.modelcontextprotocol/clientCapabilities":null}}`, -32602},
		{"array capabilities", `{"_meta":{"io.modelcontextprotocol/protocolVersion":"2026-07-28","io.modelcontextprotocol/clientCapabilities":[]}}`, -32602},
		{"old version", `{"_meta":{"io.modelcontextprotocol/protocolVersion":"2025-11-25","io.modelcontextprotocol/clientCapabilities":{}}}`, -32022},
		{"future version", `{"_meta":{"io.modelcontextprotocol/protocolVersion":"2099-01-01","io.modelcontextprotocol/clientCapabilities":{}}}`, -32022},
	} {
		t.Run(test.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
			defer cancel()
			command := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestStdioRequestMetadataErrorsAndRecovery$")
			command.Env = append(os.Environ(), "MCP_JSON_METADATA_TEST_ROOT="+t.TempDir())
			input, err := command.StdinPipe()
			if err != nil {
				t.Fatal(err)
			}
			output, err := command.StdoutPipe()
			if err != nil {
				t.Fatal(err)
			}
			command.Stderr = os.Stderr
			if err := command.Start(); err != nil {
				t.Fatal(err)
			}
			defer func() { _ = input.Close(); _ = command.Wait() }()
			decoder := json.NewDecoder(output)
			for id := 1; id <= 4; id++ {
				params := test.params
				if id%2 == 0 {
					params = `{"_meta":{"io.modelcontextprotocol/protocolVersion":"2026-07-28","io.modelcontextprotocol/clientCapabilities":{}}}`
				}
				method := "server/discover"
				if id > 2 {
					method = "tools/list"
				}
				if _, err := fmt.Fprintf(input, "{\"jsonrpc\":\"2.0\",\"id\":%d,\"method\":%q,\"params\":%s}\n", id, method, params); err != nil {
					t.Fatal(err)
				}
				var response map[string]any
				if err := decoder.Decode(&response); err != nil {
					t.Fatal(err)
				}
				if response["id"] != float64(id) {
					t.Fatalf("response=%v", response)
				}
				if id%2 == 0 {
					if response["error"] != nil || response["result"] == nil {
						t.Fatalf("valid request without optional clientInfo failed: %v", response)
					}
				} else {
					failure, ok := response["error"].(map[string]any)
					if !ok || failure["code"] != test.code {
						t.Fatalf("id=%d response=%v, want code %v", id, response, test.code)
					}
				}
			}
		})
	}
}
