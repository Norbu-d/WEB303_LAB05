// Demo CLI: exercises the Lambda client end to end and prints the gRPC
// status the Order Service would return. Use it for lab screenshots.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"

	"order-service/internal/discount"

	"google.golang.org/grpc/status"
)

func main() {
	scenario := flag.String("case", "valid", "valid | invalid | crash | slow")
	flag.Parse()

	req := discount.Request{
		CustomerTier: "gold",
		Items: []discount.Item{
			{ProductID: "p1", UnitPrice: 1200, Quantity: 6},
			{ProductID: "p2", UnitPrice: 350.5, Quantity: 4},
		},
	}
	switch *scenario {
	case "invalid":
		req.CustomerTier = "platinum"
	case "crash", "slow":
		req.Simulate = *scenario
	}

	c, err := discount.New(context.Background())
	if err != nil {
		fmt.Println("config error:", err)
		os.Exit(1)
	}

	res, err := c.Calculate(context.Background(), req)
	if err != nil {
		st := status.Convert(discount.ToGRPC(err))
		fmt.Printf("FAILED\n  internal error : %v\n  gRPC response  : %s - %s\n", err, st.Code(), st.Message())
		os.Exit(2)
	}
	fmt.Printf("OK\n  subtotal : %.2f\n  discount : %.2f (%.0f%%)\n  total    : %.2f\n",
		res.Subtotal, res.DiscountAmount, res.DiscountPercent, res.Total)
}
