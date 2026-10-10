# Wiring the client into your existing Order Service

1. Copy `services/order-service/internal/discount/` into your Order Service
   and change the import path in the demo/handler to match your module name.
2. `go get` the dependencies if your service doesn't have them yet:
   `go get github.com/aws/aws-sdk-go-v2/config github.com/aws/aws-sdk-go-v2/service/lambda github.com/aws/smithy-go`
3. Create the client once at startup and inject it into your server struct.
4. Call it inside CreateOrder, after fetching products through the Product
   Service gRPC client (never its database):

```go
type OrderServer struct {
	// ... your existing fields ...
	discount *discount.Client
}

// in main():
dc, err := discount.New(ctx)
if err != nil { log.Fatal(err) }
srv := &OrderServer{ /* ... */, discount: dc }

// in CreateOrder:
items := make([]discount.Item, 0, len(in.Items))
for _, it := range in.Items {
	p, err := s.productClient.GetProduct(ctx, &productpb.GetProductRequest{Id: it.ProductId})
	if err != nil {
		return nil, err
	}
	items = append(items, discount.Item{ProductID: p.Id, UnitPrice: p.Price, Quantity: int(it.Quantity)})
}

res, err := s.discount.Calculate(ctx, discount.Request{CustomerTier: in.CustomerTier, Items: items})
if err != nil {
	log.Printf("discount lambda failed: %v", err) // detail in logs only
	return nil, discount.ToGRPC(err)
}
// use res.Subtotal, res.DiscountAmount, res.Total when saving the order
```

## Docker Compose (Order Service container)

```yaml
order-service:
  environment:
    AWS_REGION: ap-south-1
    DISCOUNT_FUNCTION_NAME: DiscountFunction
    LAMBDA_ENDPOINT: http://host.docker.internal:3001
    AWS_ACCESS_KEY_ID: local
    AWS_SECRET_ACCESS_KEY: local
  extra_hosts:
    - "host.docker.internal:host-gateway"
```
