// Package discount calls the DiscountFunction AWS Lambda and maps every
// failure class (Part E) to a typed error.
package discount

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"strconv"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/lambda"
	"github.com/aws/smithy-go"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// Typed errors so callers can react to each failure class.
var (
	ErrInvalidInput = errors.New("invalid input")
	ErrFunction     = errors.New("lambda function error")
	ErrUnavailable  = errors.New("lambda unavailable")
	ErrAuth         = errors.New("lambda auth/permission failure")
	ErrTimeout      = errors.New("lambda timeout")
)

type Item struct {
	ProductID string  `json:"product_id"`
	UnitPrice float64 `json:"unit_price"`
	Quantity  int     `json:"quantity"`
}

type Request struct {
	CustomerTier string `json:"customer_tier"`
	Items        []Item `json:"items"`
	Simulate     string `json:"simulate,omitempty"` // demo/test hook only
}

type Result struct {
	Status          string  `json:"status"`
	Code            string  `json:"code"`
	Message         string  `json:"message"`
	Subtotal        float64 `json:"subtotal"`
	DiscountPercent float64 `json:"discount_percent"`
	DiscountAmount  float64 `json:"discount_amount"`
	Total           float64 `json:"total"`
}

type Client struct {
	lc      *lambda.Client
	fn      string
	timeout time.Duration
}

// New reads everything from env; no credentials are hard-coded.
// Env: AWS_REGION, DISCOUNT_FUNCTION_NAME, optional LAMBDA_ENDPOINT (SAM local),
// optional DISCOUNT_TIMEOUT_MS (default 3000). Credentials come from the
// standard AWS chain (env vars, shared profile, or an IAM role).
func New(ctx context.Context) (*Client, error) {
	region := os.Getenv("AWS_REGION")
	fn := os.Getenv("DISCOUNT_FUNCTION_NAME")
	if region == "" || fn == "" {
		return nil, errors.New("AWS_REGION and DISCOUNT_FUNCTION_NAME must be set")
	}
	cfg, err := config.LoadDefaultConfig(ctx, config.WithRegion(region))
	if err != nil {
		return nil, fmt.Errorf("load aws config: %w", err)
	}
	lc := lambda.NewFromConfig(cfg, func(o *lambda.Options) {
		if ep := os.Getenv("LAMBDA_ENDPOINT"); ep != "" { // local SAM only
			o.BaseEndpoint = aws.String(ep)
		}
	})

	timeout := 3 * time.Second
	if ms, err := strconv.Atoi(os.Getenv("DISCOUNT_TIMEOUT_MS")); err == nil && ms > 0 {
		timeout = time.Duration(ms) * time.Millisecond
	}
	return &Client{lc: lc, fn: fn, timeout: timeout}, nil
}

// Calculate invokes the Lambda and returns the result or a typed error.
func (c *Client) Calculate(ctx context.Context, req Request) (*Result, error) {
	payload, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidInput, err)
	}

	ctx, cancel := context.WithTimeout(ctx, c.timeout) // hard cap on the whole call
	defer cancel()

	out, err := c.lc.Invoke(ctx, &lambda.InvokeInput{
		FunctionName: aws.String(c.fn),
		Payload:      payload,
	})
	if err != nil {
		return nil, classify(err)
	}

	// Lambda ran but the function itself failed (panic / returned error)
	if out.FunctionError != nil {
		return nil, fmt.Errorf("%w: %s", ErrFunction, *out.FunctionError)
	}

	var res Result
	if err := json.Unmarshal(out.Payload, &res); err != nil {
		return nil, fmt.Errorf("%w: bad response: %v", ErrFunction, err)
	}
	if res.Status != "ok" {
		if res.Code == "INVALID_INPUT" {
			return nil, fmt.Errorf("%w: %s", ErrInvalidInput, res.Message)
		}
		return nil, fmt.Errorf("%w: %s", ErrFunction, res.Message)
	}
	return &res, nil
}

// classify maps SDK/transport errors to our typed errors.
func classify(err error) error {
	var ne net.Error
	if errors.Is(err, context.DeadlineExceeded) || (errors.As(err, &ne) && ne.Timeout()) {
		return fmt.Errorf("%w: %v", ErrTimeout, err)
	}
	var apiErr smithy.APIError
	if errors.As(err, &apiErr) {
		switch apiErr.ErrorCode() {
		case "AccessDeniedException", "UnrecognizedClientException",
			"InvalidSignatureException", "ExpiredTokenException", "InvalidClientTokenId":
			return fmt.Errorf("%w: %s", ErrAuth, apiErr.ErrorCode())
		case "ResourceNotFoundException", "ServiceException", "TooManyRequestsException":
			return fmt.Errorf("%w: %s", ErrUnavailable, apiErr.ErrorCode())
		}
	}
	return fmt.Errorf("%w: %v", ErrUnavailable, err) // connection refused, DNS, etc.
}

// ToGRPC converts to a gRPC status with a meaningful, non-leaky message.
// Full detail stays in server logs.
func ToGRPC(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, ErrInvalidInput):
		return status.Error(codes.InvalidArgument, err.Error())
	case errors.Is(err, ErrTimeout):
		return status.Error(codes.DeadlineExceeded, "discount service timed out")
	case errors.Is(err, ErrAuth):
		return status.Error(codes.Internal, "discount service is not authorized")
	case errors.Is(err, ErrUnavailable):
		return status.Error(codes.Unavailable, "discount service unavailable")
	default:
		return status.Error(codes.Internal, "discount calculation failed")
	}
}

// WithTimeout returns a copy of the client with a different timeout
// (used by the demo warm-up endpoint; SAM local cold starts are slow).
func (c *Client) WithTimeout(d time.Duration) *Client {
	cp := *c
	cp.timeout = d
	return &cp
}
