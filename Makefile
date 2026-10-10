.PHONY: setup test build local demo-valid demo-invalid demo-crash demo-slow

# One-time: download Go dependencies for both modules
setup:
	cd lambda/discount && go mod tidy
	cd services/order-service && go mod tidy

# Unit tests for the Lambda and the client (no Docker/AWS needed)
test:
	cd lambda/discount && go test ./...
	cd services/order-service && go test ./...

# Build the Lambda (needs Go; or run: sam build --use-container)
build:
	sam build

# Serve the Lambda API locally on :3001 (leave running; needs Docker)
local: build
	sam local start-lambda --host 0.0.0.0 --port 3001

# Demo scenarios (run in a second terminal while 'make local' is running)
ENVVARS = AWS_REGION=ap-south-1 DISCOUNT_FUNCTION_NAME=DiscountFunction \\
	LAMBDA_ENDPOINT=http://127.0.0.1:3001 AWS_ACCESS_KEY_ID=local AWS_SECRET_ACCESS_KEY=local

demo-valid:
	cd services/order-service && $(ENVVARS) go run ./cmd/discount-demo -case valid
demo-invalid:
	cd services/order-service && $(ENVVARS) go run ./cmd/discount-demo -case invalid
demo-crash:
	cd services/order-service && $(ENVVARS) go run ./cmd/discount-demo -case crash
demo-slow:
	cd services/order-service && $(ENVVARS) go run ./cmd/discount-demo -case slow
