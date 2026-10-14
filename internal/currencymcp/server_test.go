package currencymcp

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func connect(t *testing.T) *mcp.ClientSession {
	t.Helper()
	serverTransport, clientTransport := mcp.NewInMemoryTransports()
	ss, err := NewServer("test").Connect(t.Context(), serverTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ss.Close() })
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "test"}, nil).Connect(t.Context(), clientTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cs.Close() })
	return cs
}

func TestConvertUsesFixedRates(t *testing.T) {
	res, err := connect(t).CallTool(t.Context(), &mcp.CallToolParams{
		Name:      "convert",
		Arguments: map[string]any{"amount": 100, "from": "USD", "to": "INR"},
	})
	if err != nil {
		t.Fatal(err)
	}
	var out convertOutput
	raw, _ := json.Marshal(res.StructuredContent)
	if err := json.Unmarshal(raw, &out); err != nil || res.IsError {
		t.Fatalf("result = %+v (isError %v), err %v", res.StructuredContent, res.IsError, err)
	}
	if out.Result != 8320 || out.Rate != 83.2 {
		t.Fatalf("100 USD -> INR = %+v, want result 8320 at rate 83.2", out)
	}
}

func TestConvertRejectsUnknownCurrency(t *testing.T) {
	res, err := connect(t).CallTool(t.Context(), &mcp.CallToolParams{
		Name:      "convert",
		Arguments: map[string]any{"amount": 1, "from": "USD", "to": "XYZ"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !res.IsError {
		t.Fatalf("converting to XYZ succeeded: %+v", res.StructuredContent)
	}
}

func TestSlowEchoStopsWhenCanceled(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 50*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, _, err := slowEcho(ctx, nil, slowEchoInput{Seconds: 120, Message: "hi"})
	if !errors.Is(err, context.DeadlineExceeded) || time.Since(start) > 2*time.Second {
		t.Fatalf("err = %v after %v, want prompt DeadlineExceeded", err, time.Since(start))
	}
}

func TestSlowEchoEchoes(t *testing.T) {
	_, out, err := slowEcho(t.Context(), nil, slowEchoInput{Seconds: 0, Message: "hi"})
	if err != nil || out.Message != "hi" {
		t.Fatalf("out = %+v, err = %v", out, err)
	}
}
