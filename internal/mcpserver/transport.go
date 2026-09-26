package mcpserver

import (
	"context"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"io"

	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// StdioTransport applies the current-protocol contract before the SDK's legacy
// initialization gate. Reader must enforce one bounded JSON object per frame.
// This also rejects batches, which the SDK otherwise gates through a private
// connection interface that is unavailable to transport adapters.
type StdioTransport struct {
	Reader io.ReadCloser
	Writer io.WriteCloser
}

func (transport *StdioTransport) Connect(ctx context.Context) (mcp.Connection, error) {
	delegate := &mcp.IOTransport{
		Reader: transport.Reader, Writer: transport.Writer,
		// The physical-frame reader owns the complete input budget.
		MaxLineLength: -1,
	}
	connection, err := delegate.Connect(ctx)
	if err != nil {
		return nil, err
	}
	return &protocolConnection{Connection: connection}, nil
}

type protocolConnection struct{ mcp.Connection }

func (connection *protocolConnection) Read(ctx context.Context) (jsonrpc.Message, error) {
	for {
		message, err := connection.Connection.Read(ctx)
		if err != nil {
			return nil, err
		}
		request, ok := message.(*jsonrpc.Request)
		if !ok || !request.IsCall() {
			return message, nil
		}
		if err := validateProtocolMetadata(request.Params); err != nil {
			if err := connection.Connection.Write(ctx, &jsonrpc.Response{ID: request.ID, Error: err}); err != nil {
				return nil, err
			}
			continue
		}
		return message, nil
	}
}

func validateProtocolMetadata(raw []byte) error {
	var params struct {
		Meta map[string]jsontext.Value `json:"_meta"`
	}
	// Keep duplicate tool arguments intact for the tool's strict validator.
	if err := json.Unmarshal(raw, &params, jsontext.AllowDuplicateNames(true)); err != nil || params.Meta == nil {
		return invalidProtocolMetadata("request params must include an _meta object")
	}
	var version string
	if err := json.Unmarshal(params.Meta[mcp.MetaKeyProtocolVersion], &version); err != nil || version == "" {
		return invalidProtocolMetadata("_meta must include a protocolVersion string")
	}
	var capabilities map[string]jsontext.Value
	if err := json.Unmarshal(params.Meta[mcp.MetaKeyClientCapabilities], &capabilities); err != nil || capabilities == nil {
		return invalidProtocolMetadata("_meta must include a clientCapabilities object")
	}
	if version != currentProtocolVersion {
		return unsupportedProtocolError(version)
	}
	return nil
}

func invalidProtocolMetadata(message string) error {
	return &jsonrpc.Error{Code: jsonrpc.CodeInvalidParams, Message: message}
}
