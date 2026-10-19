package woo

import (
	"context"
	"net/http"
)

// Money amounts stay decimal strings, as WooCommerce sends them, so no precision is lost to float64.

type Product struct {
	ID           int    `json:"id"`
	Name         string `json:"name"`
	Slug         string `json:"slug"`
	Status       string `json:"status"`
	SKU          string `json:"sku"`
	Price        string `json:"price"`
	RegularPrice string `json:"regular_price"`
	StockStatus  string `json:"stock_status"`
	Permalink    string `json:"permalink"`
	DateCreated  Time   `json:"date_created_gmt"`
}

type NewProduct struct {
	Name         string `json:"name"`
	RegularPrice string `json:"regular_price"`
	SKU          string `json:"sku,omitempty"`
	Description  string `json:"description,omitempty"`
}

func (c *Client) ListProducts(ctx context.Context) ([]Product, error) {
	return listAll[Product](ctx, c, "/products", nil)
}

func (c *Client) CreateProduct(ctx context.Context, p NewProduct) (*Product, error) {
	var out Product
	if _, err := c.do(ctx, http.MethodPost, apiPrefix+"/products", nil, p, &out); err != nil {
		return nil, err
	}
	return &out, nil
}
