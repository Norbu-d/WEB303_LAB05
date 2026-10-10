// Order Service (HTTP): serves the web UI, fetches products from the
// Product Service, calls the discount Lambda, and returns the order.
package main

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"math"
	"net/http"
	"net/url"
	"os"
	"sync"
	"time"

	"order-service/internal/discount"
)

//go:embed web/index.html
var indexHTML []byte

var (
	errNotFound   = errors.New("product not found")
	errProductSvc = errors.New("product service unavailable")
)

type Product struct {
	ID    string  `json:"id"`
	Name  string  `json:"name"`
	Price float64 `json:"price"`
}

type orderItemReq struct {
	ProductID string `json:"product_id"`
	Quantity  int    `json:"quantity"`
}

type orderReq struct {
	CustomerTier string         `json:"customer_tier"`
	Items        []orderItemReq `json:"items"`
	Simulate     string         `json:"simulate"` // demo hook, honoured only if ENABLE_DEMO_HOOKS=true
}

type orderLine struct {
	ProductID string  `json:"product_id"`
	Name      string  `json:"name"`
	UnitPrice float64 `json:"unit_price"`
	Quantity  int     `json:"quantity"`
	LineTotal float64 `json:"line_total"`
}

type order struct {
	ID              string      `json:"id"`
	CreatedAt       time.Time   `json:"created_at"`
	CustomerTier    string      `json:"customer_tier"`
	Lines           []orderLine `json:"lines"`
	Subtotal        float64     `json:"subtotal"`
	DiscountPercent float64     `json:"discount_percent"`
	DiscountAmount  float64     `json:"discount_amount"`
	Total           float64     `json:"total"`
	LambdaMs        int64       `json:"lambda_ms"`
}

type server struct {
	dc         *discount.Client
	productURL string
	httpc      *http.Client
	demo       bool

	mu     sync.Mutex
	seq    int
	orders []order
}

func round2(v float64) float64 { return math.Round(v*100) / 100 }

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, status int, code, msg string) {
	writeJSON(w, status, map[string]string{"code": code, "error": msg})
}

// mapErr turns a discount-client error into an HTTP status + friendly message.
func mapErr(err error) (int, string, string) {
	switch {
	case errors.Is(err, discount.ErrInvalidInput):
		return http.StatusBadRequest, "INVALID_INPUT", err.Error()
	case errors.Is(err, discount.ErrTimeout):
		return http.StatusGatewayTimeout, "DISCOUNT_TIMEOUT", "The discount service timed out. Please try again."
	case errors.Is(err, discount.ErrUnavailable):
		return http.StatusServiceUnavailable, "DISCOUNT_UNAVAILABLE", "The discount service is unavailable. Please try again later."
	case errors.Is(err, discount.ErrAuth):
		return http.StatusBadGateway, "DISCOUNT_AUTH", "The discount service rejected our credentials."
	default:
		return http.StatusBadGateway, "DISCOUNT_ERROR", "Discount calculation failed."
	}
}

// fetchProduct goes through the Product Service API (never its data store).
func (s *server) fetchProduct(ctx context.Context, id string) (*Product, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.productURL+"/products/"+url.PathEscape(id), nil)
	if err != nil {
		return nil, err
	}
	resp, err := s.httpc.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", errProductSvc, err)
	}
	defer resp.Body.Close()

	switch resp.StatusCode {
	case http.StatusOK:
	case http.StatusNotFound:
		return nil, errNotFound
	default:
		return nil, fmt.Errorf("%w: status %d", errProductSvc, resp.StatusCode)
	}
	var p Product
	if err := json.NewDecoder(resp.Body).Decode(&p); err != nil {
		return nil, fmt.Errorf("%w: %v", errProductSvc, err)
	}
	return &p, nil
}

func (s *server) handleProducts(w http.ResponseWriter, r *http.Request) {
	req, err := http.NewRequestWithContext(r.Context(), http.MethodGet, s.productURL+"/products", nil)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "INTERNAL", "could not build request")
		return
	}
	resp, err := s.httpc.Do(req)
	if err != nil || resp.StatusCode != http.StatusOK {
		if resp != nil {
			resp.Body.Close()
		}
		log.Printf("product service error: %v", err)
		writeErr(w, http.StatusBadGateway, "PRODUCT_SERVICE_ERROR", "Product service is unavailable.")
		return
	}
	defer resp.Body.Close()
	w.Header().Set("Content-Type", "application/json")
	_, _ = io.Copy(w, resp.Body)
}

func (s *server) handleCreateOrder(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	var req orderReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "BAD_REQUEST", "invalid JSON body")
		return
	}
	if len(req.Items) > 50 {
		writeErr(w, http.StatusBadRequest, "BAD_REQUEST", "too many items (max 50)")
		return
	}

	// 1) Get product info through the Product Service API
	lines := make([]orderLine, 0, len(req.Items))
	ditems := make([]discount.Item, 0, len(req.Items))
	for _, it := range req.Items {
		p, err := s.fetchProduct(r.Context(), it.ProductID)
		if errors.Is(err, errNotFound) {
			writeErr(w, http.StatusNotFound, "PRODUCT_NOT_FOUND", "unknown product: "+it.ProductID)
			return
		}
		if err != nil {
			log.Printf("fetch product %q: %v", it.ProductID, err)
			writeErr(w, http.StatusBadGateway, "PRODUCT_SERVICE_ERROR", "Product service is unavailable.")
			return
		}
		lines = append(lines, orderLine{
			ProductID: p.ID, Name: p.Name, UnitPrice: p.Price,
			Quantity: it.Quantity, LineTotal: round2(p.Price * float64(it.Quantity)),
		})
		ditems = append(ditems, discount.Item{ProductID: p.ID, UnitPrice: p.Price, Quantity: it.Quantity})
	}

	// 2) Send it to the Lambda
	dreq := discount.Request{CustomerTier: req.CustomerTier, Items: ditems}
	if s.demo {
		dreq.Simulate = req.Simulate
	}
	start := time.Now()
	res, err := s.dc.Calculate(r.Context(), dreq)
	ms := time.Since(start).Milliseconds()
	if err != nil {
		log.Printf("discount lambda failed after %dms: %v", ms, err) // full detail in logs only
		status, code, msg := mapErr(err)
		writeErr(w, status, code, msg)
		return
	}

	// 3) Use the result: save and return the order
	s.mu.Lock()
	s.seq++
	o := order{
		ID: fmt.Sprintf("ORD-%04d", s.seq), CreatedAt: time.Now(), CustomerTier: req.CustomerTier,
		Lines: lines, Subtotal: res.Subtotal, DiscountPercent: res.DiscountPercent,
		DiscountAmount: res.DiscountAmount, Total: res.Total, LambdaMs: ms,
	}
	s.orders = append(s.orders, o)
	s.mu.Unlock()

	writeJSON(w, http.StatusCreated, o)
}

func (s *server) handleListOrders(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := len(s.orders)
	start := 0
	if n > 10 {
		start = n - 10
	}
	out := make([]order, 0, n-start)
	for i := n - 1; i >= start; i-- { // newest first
		out = append(out, s.orders[i])
	}
	writeJSON(w, http.StatusOK, out)
}

// Demo only: SAM local starts a fresh container on the first call, which is slow.
func (s *server) handleWarmup(w http.ResponseWriter, r *http.Request) {
	if !s.demo {
		writeErr(w, http.StatusNotFound, "NOT_FOUND", "demo hooks are disabled")
		return
	}
	start := time.Now()
	_, err := s.dc.WithTimeout(30*time.Second).Calculate(r.Context(), discount.Request{
		CustomerTier: "standard",
		Items:        []discount.Item{{ProductID: "warmup", UnitPrice: 1, Quantity: 1}},
	})
	if err != nil {
		log.Printf("warm-up failed: %v", err)
		status, code, msg := mapErr(err)
		writeErr(w, status, code, msg)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"status": "warm", "ms": time.Since(start).Milliseconds()})
}

func env(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func main() {
	dc, err := discount.New(context.Background())
	if err != nil {
		log.Fatalf("discount client: %v", err)
	}
	s := &server{
		dc:         dc,
		productURL: env("PRODUCT_SERVICE_URL", "http://localhost:8081"),
		httpc:      &http.Client{Timeout: 3 * time.Second},
		demo:       os.Getenv("ENABLE_DEMO_HOOKS") == "true",
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write(indexHTML)
	})
	mux.HandleFunc("GET /api/config", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]bool{"demo": s.demo})
	})
	mux.HandleFunc("GET /api/products", s.handleProducts)
	mux.HandleFunc("POST /api/orders", s.handleCreateOrder)
	mux.HandleFunc("GET /api/orders", s.handleListOrders)
	mux.HandleFunc("POST /api/warmup", s.handleWarmup)

	addr := ":" + env("ORDER_PORT", "8080")
	srv := &http.Server{Addr: addr, Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	log.Printf("order-service listening on %s (product service: %s, demo hooks: %v)", addr, s.productURL, s.demo)
	log.Fatal(srv.ListenAndServe())
}
