package services

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"

	"cloud.google.com/go/auth/credentials"
	"cloud.google.com/go/storage"
	"google.golang.org/api/iterator"
	"google.golang.org/api/option"
)

// newGCSBlobClient opens a native Cloud Storage client with Application
// Default Credentials. The storage client caches that token and refreshes it
// before expiry. STORAGE_EMULATOR_HOST skips ADC so tests can point at a
// local JSON/XML endpoint.
func newGCSBlobClient(ctx context.Context, bucket string) (blobClient, error) {
	client, err := newGCSClient(ctx)
	if err != nil {
		return nil, err
	}
	return &gcsBlobClient{client: client, bucket: bucket}, nil
}

func newGCSClient(ctx context.Context) (*storage.Client, error) {
	if os.Getenv("STORAGE_EMULATOR_HOST") != "" {
		client, err := storage.NewClient(ctx)
		if err != nil {
			return nil, fmt.Errorf("GCS client: %w", err)
		}
		return client, nil
	}
	creds, err := credentials.DetectDefault(&credentials.DetectOptions{
		Scopes: []string{storage.ScopeReadOnly},
	})
	if err != nil {
		return nil, fmt.Errorf("GCS application default credentials are not available: %w", err)
	}
	client, err := storage.NewClient(ctx, option.WithAuthCredentials(creds))
	if err != nil {
		return nil, fmt.Errorf("GCS client: %w", err)
	}
	return client, nil
}

type gcsBlobClient struct {
	client *storage.Client
	bucket string
}

func (c *gcsBlobClient) List(ctx context.Context, prefix string) ([]blobObject, error) {
	it := c.client.Bucket(c.bucket).Objects(ctx, &storage.Query{Prefix: prefix})
	var objects []blobObject
	for {
		attrs, err := it.Next()
		if err == iterator.Done {
			break
		}
		if err != nil {
			return nil, err
		}
		objects = append(objects, blobObject{
			Key:          attrs.Name,
			LastModified: attrs.Updated,
			Size:         attrs.Size,
		})
	}
	return objects, nil
}

func (c *gcsBlobClient) Open(ctx context.Context, key string) (io.ReadCloser, error) {
	reader, err := c.client.Bucket(c.bucket).Object(key).NewReader(ctx)
	if errors.Is(err, storage.ErrObjectNotExist) {
		return nil, fmt.Errorf("%w: %w", errObjectGone, err)
	}
	return reader, err
}
