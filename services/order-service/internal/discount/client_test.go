package discount

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/lambda"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// newTestClient points the real AWS SDK at a fake HTTP server.
func newTestClient(url string, timeout time.Duration) *Client {
	lc := lambda.New(lambda.Options{
		Region:           "ap-south-1",
		BaseEndpoint:     aws.String(url),
		Credentials:      credentials.NewStaticCredentialsProvider("test", "test", ""),
		RetryMaxAttempts: 1, // no retries so tests stay fast
	})
	return &Client{lc: lc, fn: "DiscountFunction", timeout: timeout}
}

var sample = Request{CustomerTier: "gold", Items: []Item{{"p1", 100, 1}}}

func TestCalculate(t *testing.T) {
	tests := []struct {
		name    string
		handler http.HandlerFunc
		want    error // nil = success
	}{
		{"success", func(w http.ResponseWriter, r *http.Request) {
			w.Write([]byte(`{"status":"ok","subtotal":100,"discount_percent":10,"discount_amount":10,"total":90}`))
		}, nil},
		{"invalid input (lambda 'error' response)", func(w http.ResponseWriter, r *http.Request) {
			w.Write([]byte(`{"status":"error","code":"INVALID_INPUT","message":"bad tier"}`))
		}, ErrInvalidInput},
		{"function error", func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("X-Amz-Function-Error", "Unhandled")
			w.Write([]byte(`{"errorMessage":"boom","errorType":"errorString"}`))
		}, ErrFunction},
		{"permission denied", func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("X-Amzn-ErrorType", "AccessDeniedException")
			w.WriteHeader(http.StatusForbidden)
			w.Write([]byte(`{"Message":"not authorized to invoke"}`))
		}, ErrAuth},
		{"function not found", func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("X-Amzn-ErrorType", "ResourceNotFoundException")
			w.WriteHeader(http.StatusNotFound)
			w.Write([]byte(`{"Message":"function not found"}`))
		}, ErrUnavailable},
		{"timeout", func(w http.ResponseWriter, r *http.Request) {
			time.Sleep(500 * time.Millisecond)
		}, ErrTimeout},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(tc.handler)
			defer srv.Close()
			c := newTestClient(srv.URL, 150*time.Millisecond)

			res, err := c.Calculate(context.Background(), sample)
			if tc.want == nil {
				if err != nil || res.Total != 90 {
					t.Fatalf("want success, got res=%+v err=%v", res, err)
				}
				return
			}
			if !errors.Is(err, tc.want) {
				t.Fatalf("want %v, got %v", tc.want, err)
			}
		})
	}
}

func TestUnavailableWhenServerDown(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	url := srv.URL
	srv.Close() // nothing listening any more
	c := newTestClient(url, time.Second)
	if _, err := c.Calculate(context.Background(), sample); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("want ErrUnavailable, got %v", err)
	}
}

func TestToGRPC(t *testing.T) {
	cases := map[error]codes.Code{
		ErrInvalidInput: codes.InvalidArgument,
		ErrTimeout:      codes.DeadlineExceeded,
		ErrAuth:         codes.Internal,
		ErrUnavailable:  codes.Unavailable,
		ErrFunction:     codes.Internal,
	}
	for in, want := range cases {
		if got := status.Code(ToGRPC(in)); got != want {
			t.Errorf("%v: want %v got %v", in, want, got)
		}
	}
}
