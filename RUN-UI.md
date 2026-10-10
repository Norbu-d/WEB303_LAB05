# Run the UI (PowerShell, 3 windows, all from the project root)

Window 1 - Lambda (SAM local, keep running)
    sam build
    sam local start-lambda --host 0.0.0.0 --port 3001 --warm-containers LAZY

Window 2 - Product Service
    cd services\order-service
    go run ./cmd/product-service

Window 3 - Order Service + UI
    cd services\order-service
    $env:AWS_REGION="ap-south-1"
    $env:DISCOUNT_FUNCTION_NAME="DiscountFunction"
    $env:LAMBDA_ENDPOINT="http://127.0.0.1:3001"
    $env:AWS_ACCESS_KEY_ID="local"
    $env:AWS_SECRET_ACCESS_KEY="local"
    $env:ENABLE_DEMO_HOOKS="true"
    go run ./cmd/server

Open http://localhost:8080
