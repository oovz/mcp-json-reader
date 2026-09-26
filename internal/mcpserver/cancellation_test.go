package mcpserver

import (
	"context"
	"io"
	"net"
	"testing"
	"time"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestCurrentProtocolCancellationReachesToolAndKeepsConnectionUsable(t *testing.T) {
	server := New(nil)
	started := make(chan struct{})
	cancelObserved := make(chan struct{})
	server.AddTool(&mcp.Tool{
		Name:        "blocked",
		Description: "test-only blocked tool",
		InputSchema: &jsonschema.Schema{Type: "object"},
	}, func(ctx context.Context, _ *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		close(started)
		<-ctx.Done()
		close(cancelObserved)
		return &mcp.CallToolResult{Content: []mcp.Content{}}, nil
	})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	serverPipe, clientPipe := net.Pipe()
	t.Cleanup(func() { _ = serverPipe.Close(); _ = clientPipe.Close() })
	serverTransport := &StdioTransport{Reader: serverPipe, Writer: transportTestWriter{serverPipe}}
	clientTransport := &mcp.IOTransport{Reader: clientPipe, Writer: transportTestWriter{clientPipe}}
	serverDone := make(chan error, 1)
	go func() { serverDone <- server.Run(ctx, serverTransport) }()
	client := mcp.NewClient(&mcp.Implementation{Name: "cancellation-test", Version: "1.0.0"}, nil)
	session, err := client.Connect(ctx, clientTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = session.Close()
		<-serverDone
	}()

	callContext, stopCall := context.WithCancel(ctx)
	callDone := make(chan error, 1)
	go func() {
		_, callErr := session.CallTool(callContext, &mcp.CallToolParams{Name: "blocked", Arguments: map[string]any{}})
		callDone <- callErr
	}()
	select {
	case <-started:
	case <-ctx.Done():
		t.Fatal("blocked tool did not start")
	}
	stopCall()
	select {
	case <-cancelObserved:
	case <-ctx.Done():
		t.Fatal("tool did not observe request cancellation")
	}
	select {
	case <-callDone:
	case <-ctx.Done():
		t.Fatal("cancelled tool call did not return")
	}
	if _, err := session.ListTools(ctx, nil); err != nil {
		t.Fatalf("connection unusable after request cancellation: %v", err)
	}
}

type transportTestWriter struct{ io.Writer }

func (transportTestWriter) Close() error { return nil }

func TestStdioTransportCloseUnblocksRead(t *testing.T) {
	serverPipe, clientPipe := net.Pipe()
	t.Cleanup(func() { _ = serverPipe.Close(); _ = clientPipe.Close() })
	transport := &StdioTransport{Reader: serverPipe, Writer: transportTestWriter{serverPipe}}
	connection, err := transport.Connect(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	readDone := make(chan error, 1)
	go func() { _, err := connection.Read(t.Context()); readDone <- err }()
	if err := connection.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-readDone:
		if err == nil {
			t.Fatal("read succeeded after close")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("close did not unblock read")
	}
}
