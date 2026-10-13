package woo

import (
	"context"
	"net/http"
	"net/url"
	"strconv"
	"time"
)

// OrderStatus is a WooCommerce order status slug.
type OrderStatus string

// Order statuses built into WooCommerce. Plugins can register others.
const (
	StatusPending    OrderStatus = "pending"
	StatusProcessing OrderStatus = "processing"
	StatusOnHold     OrderStatus = "on-hold"
	StatusCompleted  OrderStatus = "completed"
	StatusCancelled  OrderStatus = "cancelled"
	StatusRefunded   OrderStatus = "refunded"
	StatusFailed     OrderStatus = "failed"
)

// Order is a WooCommerce order. Total and LineItem.Total are decimal strings.
type Order struct {
	ID            int         `json:"id"`
	Number        string      `json:"number"`
	Status        OrderStatus `json:"status"`
	Currency      string      `json:"currency"`
	Total         string      `json:"total"`
	PaymentMethod string      `json:"payment_method"`
	CustomerNote  string      `json:"customer_note"`
	DateCreated   Time        `json:"date_created_gmt"`
	Billing       Address     `json:"billing"`
	LineItems     []LineItem  `json:"line_items"`
}

// Address is the contact part of an order's billing address.
type Address struct {
	FirstName string `json:"first_name"`
	LastName  string `json:"last_name"`
	Email     string `json:"email"`
	Phone     string `json:"phone"`
}

// LineItem is one product line of an order. Total is a decimal string.
type LineItem struct {
	ProductID int    `json:"product_id"`
	Name      string `json:"name"`
	Quantity  int    `json:"quantity"`
	Total     string `json:"total"`
}

// ListOrders returns orders created at or after `after`, optionally filtered by status ("" means any).
// A zero `after` returns orders from any date.
func (c *Client) ListOrders(ctx context.Context, after time.Time, status OrderStatus) ([]Order, error) {
	q := url.Values{}
	if !after.IsZero() {
		// WordPress shifts a zoned timestamp (…Z, …+00:00) into store-local time before comparing it with the GMT
		// column, so send naive UTC. Its `after` is exclusive and second-granular, hence the one-second step back.
		q.Set("after", after.UTC().Add(-time.Second).Format(wooTimeLayout))
		q.Set("dates_are_gmt", "true")
	}
	if status != "" {
		q.Set("status", string(status))
	}
	return listAll[Order](ctx, c, "/orders", q)
}

// GetOrder returns the order with the given ID.
func (c *Client) GetOrder(ctx context.Context, id int) (*Order, error) {
	var out Order
	if _, err := c.do(ctx, http.MethodGet, orderPath(id), nil, nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// UpdateOrderStatus sets the order's status and returns the updated order.
func (c *Client) UpdateOrderStatus(ctx context.Context, id int, status OrderStatus) (*Order, error) {
	var out Order
	body := orderStatusUpdate{Status: status}
	if _, err := c.do(ctx, http.MethodPut, orderPath(id), nil, body, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

type orderStatusUpdate struct {
	Status OrderStatus `json:"status"`
}

func orderPath(id int) string {
	return apiPrefix + "/orders/" + strconv.Itoa(id)
}
