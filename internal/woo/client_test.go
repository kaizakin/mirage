package woo

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestLocation(t *testing.T) {
	cases := []struct {
		name, index string
		wantOffset  int // seconds east of UTC in January
	}{
		// gmt_offset is the current (summer) offset; a client that ignored timezone_string would get -4h in January.
		{"timezone string wins over offset", `{"timezone_string":"America/New_York","gmt_offset":-4}`, -18000},
		{"fractional offset", `{"timezone_string":"","gmt_offset":5.5}`, 19800},
		{"manual offset as string", `{"timezone_string":"","gmt_offset":"-3.5"}`, -12600},
		{"manual offset as number", `{"timezone_string":"","gmt_offset":0}`, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/wp-json/" {
					http.NotFound(w, r)
					return
				}
				fmt.Fprint(w, tc.index)
			}))
			defer srv.Close()

			loc, err := New(srv.URL, "u", "p").Location(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			_, off := time.Date(2026, 1, 15, 12, 0, 0, 0, loc).Zone()
			if off != tc.wantOffset {
				t.Fatalf("offset = %d, want %d", off, tc.wantOffset)
			}
		})
	}
}

func TestListOrdersFollowsPagesAndSendsGMTFilter(t *testing.T) {
	after := time.Date(2026, 10, 20, 0, 0, 0, 0, time.FixedZone("IST", 19800))
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if user, pass, _ := r.BasicAuth(); user != "u" || pass != "p" {
			t.Errorf("basic auth = %q/%q", user, pass)
		}
		if q.Get("after") != "2026-10-19T18:29:59" || q.Get("dates_are_gmt") != "true" || q.Get("status") != "processing" {
			t.Errorf("query = %v", q)
		}
		w.Header().Set("X-WP-Total", "3")
		w.Header().Set("X-WP-TotalPages", "2")
		switch q.Get("page") {
		case "1":
			fmt.Fprint(w, `[{"id":1,"status":"processing","date_created_gmt":"2026-10-19T19:00:00"},{"id":2}]`)
		case "2":
			fmt.Fprint(w, `[{"id":3}]`)
		default:
			t.Errorf("unexpected page %q", q.Get("page"))
		}
	}))
	defer srv.Close()

	orders, err := New(srv.URL, "u", "p").ListOrders(context.Background(), after, StatusProcessing)
	if err != nil {
		t.Fatal(err)
	}
	if len(orders) != 3 || orders[2].ID != 3 {
		t.Fatalf("orders = %+v", orders)
	}
	if want := time.Date(2026, 10, 19, 19, 0, 0, 0, time.UTC); !orders[0].DateCreated.Equal(want) {
		t.Fatalf("date_created_gmt = %v, want %v", orders[0].DateCreated, want)
	}
}

func TestAPIError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		fmt.Fprint(w, `{"code":"woocommerce_rest_shop_order_invalid_id","message":"Invalid ID.","data":{"status":404}}`)
	}))
	defer srv.Close()

	_, err := New(srv.URL, "u", "p").GetOrder(context.Background(), 999)
	var apiErr *APIError
	if !errors.As(err, &apiErr) || apiErr.StatusCode != 404 || apiErr.Code != "woocommerce_rest_shop_order_invalid_id" {
		t.Fatalf("err = %v", err)
	}
}

func TestStartOfDaySkipsMissingMidnight(t *testing.T) {
	santiago, err := time.LoadLocation("America/Santiago")
	if err != nil {
		t.Fatal(err)
	}
	// Chile springs forward at 00:00 on 2026-09-06; the day starts at 01:00 -03.
	got := startOfDay(2026, time.September, 6, santiago)
	if want := time.Date(2026, 9, 6, 4, 0, 0, 0, time.UTC); !got.Equal(want) {
		t.Fatalf("start of day = %v, want %v", got, want.In(santiago))
	}
}

func TestListRejectsMissingPaginationHeader(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `[{"id":1}]`)
	}))
	defer srv.Close()

	if _, err := New(srv.URL, "u", "p").ListProducts(context.Background()); err == nil {
		t.Fatal("expected an error when X-WP-TotalPages is missing")
	}
}
