// Product Service: a tiny in-memory catalog exposed over HTTP.
// The Order Service reads products ONLY through this API.
package main

import (
	"encoding/json"
	"log"
	"net/http"
	"os"
	"time"
)

type Product struct {
	ID    string  `json:"id"`
	Name  string  `json:"name"`
	Price float64 `json:"price"` // Nu.
}

var catalog = []Product{
	{"p1", "Wireless Mouse", 850},
	{"p2", "Mechanical Keyboard", 3200},
	{"p3", "USB-C Hub", 1500},
	{"p4", "24\" Monitor", 12500},
	{"p5", "Laptop Stand", 1200},
	{"p6", "HDMI Cable", 350},
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func main() {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /products", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, catalog)
	})
	mux.HandleFunc("GET /products/{id}", func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		for _, p := range catalog {
			if p.ID == id {
				writeJSON(w, http.StatusOK, p)
				return
			}
		}
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "product not found"})
	})

	port := os.Getenv("PRODUCT_PORT")
	if port == "" {
		port = "8081"
	}
	srv := &http.Server{Addr: ":" + port, Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	log.Printf("product-service listening on :%s", port)
	log.Fatal(srv.ListenAndServe())
}
