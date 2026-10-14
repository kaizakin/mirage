// Package currencymcp is a small MCP server that converts between currencies at fixed rates.
package currencymcp

import (
	"context"
	"fmt"
	"maps"
	"math"
	"slices"
	"time"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// unitsPerUSD is a fixed snapshot; the server exists to demo the gateway, not to quote markets.
var unitsPerUSD = map[string]float64{
	"USD": 1, "EUR": 0.92, "GBP": 0.79, "INR": 83.2, "JPY": 150.1, "AUD": 1.52, "CAD": 1.37,
}

type convertInput struct {
	Amount float64 `json:"amount" jsonschema:"Amount to convert, in the from currency."`
	From   string  `json:"from" jsonschema:"ISO 4217 code of the amount's currency."`
	To     string  `json:"to" jsonschema:"ISO 4217 code to convert into."`
}

type convertOutput struct {
	Result float64 `json:"result" jsonschema:"Converted amount, rounded to 2 decimal places."`
	Rate   float64 `json:"rate" jsonschema:"Units of to per one unit of from."`
}

type slowEchoInput struct {
	Seconds int    `json:"seconds" jsonschema:"How long to wait before answering."`
	Message string `json:"message,omitempty" jsonschema:"Text to echo back."`
}

type slowEchoOutput struct {
	Message string `json:"message"`
}

// NewServer returns an MCP server named "currency-mcp" with the convert and slow_echo tools.
func NewServer(version string) *mcp.Server {
	server := mcp.NewServer(&mcp.Implementation{Name: "currency-mcp", Version: version}, nil)

	convertSchema, err := jsonschema.For[convertInput](nil)
	if err != nil {
		panic(err)
	}
	var codes []any
	for _, c := range slices.Sorted(maps.Keys(unitsPerUSD)) {
		codes = append(codes, c)
	}
	convertSchema.Properties["from"].Enum = codes
	convertSchema.Properties["to"].Enum = codes
	convertSchema.Properties["amount"].Minimum = new(0.0)
	mcp.AddTool(server, &mcp.Tool{
		Name:        "convert",
		Description: "Convert an amount between currencies at fixed reference rates (not live market rates).",
		InputSchema: convertSchema,
	}, convert)

	echoSchema, err := jsonschema.For[slowEchoInput](nil)
	if err != nil {
		panic(err)
	}
	echoSchema.Properties["seconds"].Minimum, echoSchema.Properties["seconds"].Maximum = new(0.0), new(120.0)
	mcp.AddTool(server, &mcp.Tool{
		Name:        "slow_echo",
		Description: "Test helper for timeout handling: waits the given number of seconds, then echoes message. Do not use for real work.",
		InputSchema: echoSchema,
	}, slowEcho)

	return server
}

func convert(_ context.Context, _ *mcp.CallToolRequest, in convertInput) (*mcp.CallToolResult, convertOutput, error) {
	rate := unitsPerUSD[in.To] / unitsPerUSD[in.From]
	return nil, convertOutput{Result: math.Round(in.Amount*rate*100) / 100, Rate: rate}, nil
}

func slowEcho(ctx context.Context, _ *mcp.CallToolRequest, in slowEchoInput) (*mcp.CallToolResult, slowEchoOutput, error) {
	select {
	case <-time.After(time.Duration(in.Seconds) * time.Second):
		return nil, slowEchoOutput{Message: in.Message}, nil
	case <-ctx.Done():
		return nil, slowEchoOutput{}, fmt.Errorf("slow_echo canceled before %ds elapsed: %w", in.Seconds, ctx.Err())
	}
}
