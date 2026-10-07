package services

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/smithy-go/logging"
)

// S3-compatible stores often return objects without a checksum header. The
// SDK then logs one line per object read, every poll, which buries real
// errors in the log.
func TestS3ClientDoesNotLogSkippedChecksumPerObject(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `{"nodeId":"node-a"}`)
	}))
	defer server.Close()

	var mu sync.Mutex
	var logged []string
	awsCfg := aws.Config{
		Region:      "test",
		Credentials: credentials.NewStaticCredentialsProvider("access", "secret", ""),
		HTTPClient:  server.Client(),
		// What config.LoadDefaultConfig sets.
		ResponseChecksumValidation: aws.ResponseChecksumValidationWhenSupported,
		Logger: logging.LoggerFunc(func(c logging.Classification, format string, v ...any) {
			mu.Lock()
			defer mu.Unlock()
			logged = append(logged, fmt.Sprintf(format, v...))
		}),
	}
	client := newS3Client(awsCfg, ObjectStoreConfig{Endpoint: server.URL, UsePathStyle: true})
	blobs := &s3BlobClient{client: client, bucket: "bucket"}
	body, err := blobs.Open(context.Background(), "network/2026/05/08/13/45/00.json")
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if _, err := io.ReadAll(body); err != nil {
		t.Fatalf("read: %v", err)
	}
	_ = body.Close()

	mu.Lock()
	defer mu.Unlock()
	for _, line := range logged {
		if strings.Contains(line, "checksum") {
			t.Fatalf("SDK logged %q for an object read", line)
		}
	}
}
