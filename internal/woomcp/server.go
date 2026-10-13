// Package woomcp exposes a WooCommerce store as MCP tools.
package woomcp

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/kaizakin/mirage/internal/woo"
)

var orderStatuses = []any{
	woo.StatusPending, woo.StatusProcessing, woo.StatusOnHold, woo.StatusCompleted,
	woo.StatusCancelled, woo.StatusRefunded, woo.StatusFailed,
}

// Inputs restrict statuses to the built-in set. Outputs accept any string, because plugins
// register their own statuses and an unknown one must not fail output validation.
var (
	inputSchemas = &jsonschema.ForOptions{TypeSchemas: map[reflect.Type]*jsonschema.Schema{
		reflect.TypeFor[woo.Time]():        {Type: "string", Format: "date-time"},
		reflect.TypeFor[woo.OrderStatus](): {Type: "string", Enum: orderStatuses},
	}}
	outputSchemas = &jsonschema.ForOptions{TypeSchemas: map[reflect.Type]*jsonschema.Schema{
		reflect.TypeFor[woo.Time]():        {Type: "string", Format: "date-time"},
		reflect.TypeFor[woo.OrderStatus](): {Type: "string"},
	}}
)

// schemaFor panics on error: every schema is derived from a static type at startup.
func schemaFor[T any](opts *jsonschema.ForOptions) *jsonschema.Schema {
	s, err := jsonschema.For[T](opts)
	if err != nil {
		panic(fmt.Sprintf("woomcp: schema for %v: %v", reflect.TypeFor[T](), err))
	}
	return s
}

// outputSchemaFor infers T's schema with its slices non-nullable. Go slices infer as
// ["null","array"], which single-type dialects such as Gemini function declarations reject;
// outputs never carry null slices because the woo client returns empty ones.
func outputSchemaFor[T any]() *jsonschema.Schema {
	s := schemaFor[T](outputSchemas)
	dropNullFromArrays(s)
	return s
}

func dropNullFromArrays(s *jsonschema.Schema) {
	if s == nil {
		return
	}
	if len(s.Types) == 2 && slices.Contains(s.Types, "null") && slices.Contains(s.Types, "array") {
		s.Type, s.Types = "array", nil
	}
	for _, p := range s.Properties {
		dropNullFromArrays(p)
	}
	dropNullFromArrays(s.Items)
}

// withMinimumID requires the "id" property to be a positive integer.
func withMinimumID(s *jsonschema.Schema) *jsonschema.Schema {
	s.Properties["id"].Minimum = new(1.0)
	return s
}

type noInput struct{}

type productList struct {
	Products []woo.Product `json:"products"`
}

type createProductInput struct {
	Name         string `json:"name" jsonschema:"Product name shown to shoppers."`
	RegularPrice string `json:"regular_price" jsonschema:"Price as a decimal string in the store currency, such as \"299\" or \"12.50\". No currency symbol or thousands separator."`
	SKU          string `json:"sku,omitempty" jsonschema:"Optional stock-keeping unit. Must be unique in the store."`
	Description  string `json:"description,omitempty" jsonschema:"Optional long description. HTML is allowed."`
}

type listOrdersInput struct {
	After  string          `json:"after,omitempty" jsonschema:"Only orders created at or after this moment: \"today\", a date \"YYYY-MM-DD\" (midnight in the store's timezone), or an RFC 3339 timestamp. Omit for all orders."`
	Status woo.OrderStatus `json:"status,omitempty" jsonschema:"Only orders with this status. Omit for any status."`
}

type orderList struct {
	Orders []woo.Order `json:"orders"`
}

type orderIDInput struct {
	ID int `json:"id" jsonschema:"The order ID, as returned by list_orders."`
}

type updateOrderStatusInput struct {
	ID     int             `json:"id" jsonschema:"The order ID, as returned by list_orders."`
	Status woo.OrderStatus `json:"status" jsonschema:"The new status."`
}

// NewServer returns an MCP server named "woo-mcp" whose tools act on the store behind c.
func NewServer(c *woo.Client, version string) *mcp.Server {
	server := mcp.NewServer(&mcp.Implementation{Name: "woo-mcp", Version: version}, nil)
	t := tools{c}

	mcp.AddTool(server, &mcp.Tool{
		Name: "list_products",
		Description: "List every product in the WooCommerce store, in ascending ID order. " +
			"Prices are decimal strings in the store currency; price is the current selling price and regular_price the list price.",
		InputSchema:  schemaFor[noInput](inputSchemas),
		OutputSchema: outputSchemaFor[productList](),
	}, t.listProducts)

	mcp.AddTool(server, &mcp.Tool{
		Name: "create_product",
		Description: "Create a published simple product and return it as the store saved it. " +
			"regular_price is a decimal string such as \"299\" or \"12.50\" in the store currency. " +
			"The store rejects a SKU that another product already uses.",
		InputSchema:  schemaFor[createProductInput](inputSchemas),
		OutputSchema: outputSchemaFor[woo.Product](),
	}, t.createProduct)

	mcp.AddTool(server, &mcp.Tool{
		Name: "list_orders",
		Description: "List orders, oldest first, optionally filtered by creation date and status. " +
			"Use after to filter by date; 'today' means the store's local day. " +
			"after also accepts a date \"YYYY-MM-DD\" (from midnight in the store's timezone) or an RFC 3339 timestamp such as \"2026-10-20T09:30:00+05:30\". " +
			"Omit after for orders from any date. Totals are decimal strings; date_created_gmt is UTC.",
		InputSchema:  schemaFor[listOrdersInput](inputSchemas),
		OutputSchema: outputSchemaFor[orderList](),
	}, t.listOrders)

	mcp.AddTool(server, &mcp.Tool{
		Name:         "get_order",
		Description:  "Get one order by ID, including its billing contact and line items. Totals are decimal strings; date_created_gmt is UTC.",
		InputSchema:  withMinimumID(schemaFor[orderIDInput](inputSchemas)),
		OutputSchema: outputSchemaFor[woo.Order](),
	}, t.getOrder)

	mcp.AddTool(server, &mcp.Tool{
		Name: "update_order_status",
		Description: "Set an order's status and return the updated order. " +
			"WooCommerce may email the customer and adjust stock on some transitions, such as to completed, cancelled, or refunded. " +
			"Setting refunded does not move money through the payment gateway.",
		InputSchema:  withMinimumID(schemaFor[updateOrderStatusInput](inputSchemas)),
		OutputSchema: outputSchemaFor[woo.Order](),
	}, t.updateOrderStatus)

	return server
}

type tools struct{ c *woo.Client }

func (t tools) listProducts(ctx context.Context, _ *mcp.CallToolRequest, _ noInput) (*mcp.CallToolResult, productList, error) {
	products, err := t.c.ListProducts(ctx)
	if err != nil {
		return nil, productList{}, describe(err)
	}
	return nil, productList{Products: products}, nil
}

var decimalPrice = regexp.MustCompile(`^[0-9]+(\.[0-9]+)?$`)

func (t tools) createProduct(ctx context.Context, _ *mcp.CallToolRequest, in createProductInput) (*mcp.CallToolResult, woo.Product, error) {
	if strings.TrimSpace(in.Name) == "" {
		return nil, woo.Product{}, errors.New("name must not be empty")
	}
	if !decimalPrice.MatchString(in.RegularPrice) {
		return nil, woo.Product{}, fmt.Errorf(`regular_price must be a decimal string such as "299" or "12.50", with no currency symbol or separators; got %q`, in.RegularPrice)
	}
	p, err := t.c.CreateProduct(ctx, woo.NewProduct{
		Name:         in.Name,
		RegularPrice: in.RegularPrice,
		SKU:          in.SKU,
		Description:  in.Description,
	})
	if err != nil {
		return nil, woo.Product{}, describe(err)
	}
	return nil, *p, nil
}

func (t tools) listOrders(ctx context.Context, _ *mcp.CallToolRequest, in listOrdersInput) (*mcp.CallToolResult, orderList, error) {
	after, err := t.resolveAfter(ctx, in.After)
	if err != nil {
		return nil, orderList{}, err
	}
	orders, err := t.c.ListOrders(ctx, after, in.Status)
	if err != nil {
		return nil, orderList{}, describe(err)
	}
	for i := range orders {
		orders[i] = withLineItems(orders[i])
	}
	return nil, orderList{Orders: orders}, nil
}

// resolveAfter turns list_orders' after argument into an instant. The zero time means no date filter.
func (t tools) resolveAfter(ctx context.Context, after string) (time.Time, error) {
	if after == "" {
		return time.Time{}, nil
	}
	if after == "today" {
		start, err := t.c.StartOfToday(ctx)
		if err != nil {
			return time.Time{}, describe(err)
		}
		return start, nil
	}
	if date, err := time.Parse(time.DateOnly, after); err == nil {
		loc, err := t.c.Location(ctx)
		if err != nil {
			return time.Time{}, describe(err)
		}
		y, m, d := date.Date()
		return woo.StartOfDay(y, m, d, loc), nil
	}
	if ts, err := time.Parse(time.RFC3339, after); err == nil {
		return ts, nil
	}
	return time.Time{}, fmt.Errorf(`after must be "today", a date "YYYY-MM-DD", or an RFC 3339 timestamp such as "2026-10-20T09:30:00+05:30"; got %q`, after)
}

func (t tools) getOrder(ctx context.Context, _ *mcp.CallToolRequest, in orderIDInput) (*mcp.CallToolResult, woo.Order, error) {
	o, err := t.c.GetOrder(ctx, in.ID)
	if err != nil {
		return nil, woo.Order{}, describe(err)
	}
	return nil, withLineItems(*o), nil
}

func (t tools) updateOrderStatus(ctx context.Context, _ *mcp.CallToolRequest, in updateOrderStatusInput) (*mcp.CallToolResult, woo.Order, error) {
	o, err := t.c.UpdateOrderStatus(ctx, in.ID, in.Status)
	if err != nil {
		return nil, woo.Order{}, describe(err)
	}
	return nil, withLineItems(*o), nil
}

// withLineItems replaces a missing line_items with an empty list, as the output schema
// promises an array.
func withLineItems(o woo.Order) woo.Order {
	if o.LineItems == nil {
		o.LineItems = []woo.LineItem{}
	}
	return o
}

// describe rewrites a woo client error as a tool error a model can act on.
func describe(err error) error {
	if apiErr, ok := errors.AsType[*woo.APIError](err); ok {
		status := fmt.Sprintf("HTTP %d", apiErr.StatusCode)
		if apiErr.Code != "" {
			status += " " + apiErr.Code
		}
		return fmt.Errorf("WooCommerce rejected the request (%s): %s", status, apiErr.Message)
	}
	return fmt.Errorf("the WooCommerce store could not be reached: %w", err)
}
