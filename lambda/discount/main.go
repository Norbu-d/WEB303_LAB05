package main

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/aws/aws-lambda-go/lambda"
)

type Item struct {
	ProductID string  `json:"product_id"`
	UnitPrice float64 `json:"unit_price"`
	Quantity  int     `json:"quantity"`
}

type Request struct {
	CustomerTier string `json:"customer_tier"` // standard | silver | gold
	Items        []Item `json:"items"`
	// Test hook ("crash" | "slow") used to demonstrate Part E failures.
	// Remove before any real production use.
	Simulate string `json:"simulate,omitempty"`
}

type Response struct {
	Status          string  `json:"status"` // "ok" | "error"
	Code            string  `json:"code,omitempty"`
	Message         string  `json:"message,omitempty"`
	Subtotal        float64 `json:"subtotal,omitempty"`
	DiscountPercent float64 `json:"discount_percent"`
	DiscountAmount  float64 `json:"discount_amount"`
	Total           float64 `json:"total,omitempty"`
}

var tierRate = map[string]float64{"standard": 0, "silver": 5, "gold": 10}

const (
	bulkThreshold = 10   // total units needed for the bulk bonus
	bulkBonus     = 5.0  // extra percent
	maxDiscount   = 20.0 // cap in percent
)

func round2(v float64) float64 { return math.Round(v*100) / 100 }

func invalid(msg string) Response {
	return Response{Status: "error", Code: "INVALID_INPUT", Message: msg}
}

func validate(r Request) error {
	if len(r.Items) == 0 {
		return errors.New("items must not be empty")
	}
	if _, ok := tierRate[strings.ToLower(r.CustomerTier)]; !ok {
		return fmt.Errorf("unknown customer_tier %q", r.CustomerTier)
	}
	for i, it := range r.Items {
		if it.UnitPrice <= 0 {
			return fmt.Errorf("items[%d].unit_price must be > 0", i)
		}
		if it.Quantity <= 0 {
			return fmt.Errorf("items[%d].quantity must be > 0", i)
		}
	}
	return nil
}

func handler(ctx context.Context, req Request) (Response, error) {
	switch req.Simulate {
	case "crash": // unhandled error -> Lambda FunctionError
		return Response{}, errors.New("simulated internal failure")
	case "slow": // exceeds client timeout
		select {
		case <-time.After(6 * time.Second):
		case <-ctx.Done():
			return Response{}, ctx.Err()
		}
	}

	// Business-level validation: structured error response, not a crash
	if err := validate(req); err != nil {
		return invalid(err.Error()), nil
	}

	var subtotal float64
	var units int
	for _, it := range req.Items {
		subtotal += it.UnitPrice * float64(it.Quantity)
		units += it.Quantity
	}

	pct := tierRate[strings.ToLower(req.CustomerTier)]
	if units >= bulkThreshold {
		pct += bulkBonus
	}
	if pct > maxDiscount {
		pct = maxDiscount
	}

	discount := round2(subtotal * pct / 100)
	return Response{
		Status:          "ok",
		Subtotal:        round2(subtotal),
		DiscountPercent: pct,
		DiscountAmount:  discount,
		Total:           round2(subtotal - discount),
	}, nil
}

func main() { lambda.Start(handler) }
