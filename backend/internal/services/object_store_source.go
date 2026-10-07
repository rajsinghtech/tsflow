package services

import (
	"bufio"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"path"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/credentials/stscreds"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/sts"
	"github.com/klauspost/compress/zstd"
	tsconfig "github.com/rajsinghtech/tsflow/backend/internal/config"
	"github.com/rajsinghtech/tsflow/backend/internal/database"
)

type ObjectStoreConfig struct {
	Bucket       string
	Prefix       string
	Endpoint     string
	Region       string
	AccessKey    string
	SecretKey    string
	UsePathStyle bool
	Lookback     time.Duration
	MaxObjects   int
	// AuthMode is static, aws_default, gcs_adc, or empty. Empty keeps the
	// historical static provider when keys are set.
	AuthMode string
	// RoleARN, when set with aws_default, is assumed using the default chain.
	// WebIdentityTokenFile selects AssumeRoleWithWebIdentity for that role
	// and does not consult the process AWS credential environment.
	RoleARN              string
	WebIdentityTokenFile string
	// stsEndpoint overrides the STS endpoint for AssumeRoleWithWebIdentity.
	// Tests set it. Production leaves it empty.
	stsEndpoint string
}

type blobObject struct {
	Key          string
	LastModified time.Time
	Size         int64
}

// blobClient lists and opens objects. S3 and GCS each have an implementation.
// One client is created per tailnet reader and reused for every poll.
type blobClient interface {
	List(ctx context.Context, prefix string) ([]blobObject, error)
	Open(ctx context.Context, key string) (io.ReadCloser, error)
}

// maxObjectReadAttempts is how many polls an unreadable object is retried
// before it is recorded as ingested with no flows. Objects are immutable, so
// a body that is malformed, truncated, or gone stays that way, and retrying
// it forever pins the poll cursor while the scanned window keeps growing.
const maxObjectReadAttempts = 5

type ObjectStoreSource struct {
	cfg   ObjectStoreConfig
	blobs blobClient

	// readFailures counts consecutive failed reads per object key. It lives
	// in memory, so a restart grants every object a fresh budget.
	failuresMu   sync.Mutex
	readFailures map[string]int
}

type flowObject struct {
	key          string
	lastModified time.Time
	size         int64
	logTime      time.Time
}

func NewObjectStoreSource(ctx context.Context, cfg ObjectStoreConfig) (*ObjectStoreSource, error) {
	cfg, err := normalizeObjectStoreConfig(cfg)
	if err != nil {
		return nil, err
	}
	if cfg.AuthMode == tsconfig.ObjectStoreAuthGCSADC {
		blobs, err := newGCSBlobClient(ctx, cfg.Bucket)
		if err != nil {
			return nil, err
		}
		return &ObjectStoreSource{cfg: cfg, blobs: blobs}, nil
	}
	awsCfg, err := loadObjectStoreAWSConfig(ctx, cfg)
	if err != nil {
		return nil, err
	}

	client := newS3Client(awsCfg, cfg)
	return &ObjectStoreSource{cfg: cfg, blobs: &s3BlobClient{client: client, bucket: cfg.Bucket}}, nil
}

func newS3Client(awsCfg aws.Config, cfg ObjectStoreConfig) *s3.Client {
	return s3.NewFromConfig(awsCfg, func(o *s3.Options) {
		o.UsePathStyle = cfg.UsePathStyle
		if cfg.Endpoint != "" {
			o.BaseEndpoint = aws.String(cfg.Endpoint)
		}
		// Many S3-compatible stores return objects without a checksum header,
		// and the SDK would log that once per object on every poll.
		o.DisableLogOutputChecksumValidationSkipped = true
	})
}

type s3BlobClient struct {
	client *s3.Client
	bucket string
}

func (c *s3BlobClient) List(ctx context.Context, prefix string) ([]blobObject, error) {
	paginator := s3.NewListObjectsV2Paginator(c.client, &s3.ListObjectsV2Input{
		Bucket: aws.String(c.bucket),
		Prefix: aws.String(prefix),
	})
	var objects []blobObject
	for paginator.HasMorePages() {
		page, err := paginator.NextPage(ctx)
		if err != nil {
			return nil, err
		}
		for _, item := range page.Contents {
			lastModified := time.Time{}
			if item.LastModified != nil {
				lastModified = *item.LastModified
			}
			objects = append(objects, blobObject{
				Key:          aws.ToString(item.Key),
				LastModified: lastModified,
				Size:         aws.ToInt64(item.Size),
			})
		}
	}
	return objects, nil
}

func (c *s3BlobClient) Open(ctx context.Context, key string) (io.ReadCloser, error) {
	out, err := c.client.GetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(c.bucket),
		Key:    aws.String(key),
	})
	if err != nil {
		var status interface{ HTTPStatusCode() int }
		if errors.As(err, &status) && status.HTTPStatusCode() == http.StatusNotFound {
			return nil, fmt.Errorf("%w: %w", errObjectGone, err)
		}
		return nil, err
	}
	return out.Body, nil
}

// errObjectGone marks an object that was listed but no longer exists, for
// example after a lifecycle rule deleted it between LIST and GET.
var errObjectGone = errors.New("object no longer exists")

// objectContentError marks a body that was fetched but cannot be decoded:
// malformed JSON, a corrupt or truncated compressed stream, or an oversized
// line. Objects are immutable, so retrying does not repair these.
type objectContentError struct{ err error }

func (e objectContentError) Error() string { return e.err.Error() }
func (e objectContentError) Unwrap() error { return e.err }

// permanentReadError reports whether a failed read will fail the same way on
// every retry. Authorization, throttling, server, and network errors are
// not permanent: giving up on those would drop data during an outage.
func permanentReadError(err error) bool {
	var content objectContentError
	return errors.Is(err, errObjectGone) || errors.As(err, &content)
}

func normalizeObjectStoreConfig(cfg ObjectStoreConfig) (ObjectStoreConfig, error) {
	cfg.AuthMode = strings.ToLower(strings.TrimSpace(cfg.AuthMode))
	switch cfg.AuthMode {
	case "", tsconfig.ObjectStoreAuthStatic, tsconfig.ObjectStoreAuthAWSDefault, tsconfig.ObjectStoreAuthGCSADC:
	default:
		return ObjectStoreConfig{}, fmt.Errorf("object store auth %q is not supported yet", cfg.AuthMode)
	}
	if cfg.AuthMode == tsconfig.ObjectStoreAuthGCSADC && strings.TrimSpace(cfg.Endpoint) != "" {
		return ObjectStoreConfig{}, fmt.Errorf("GCS application default credentials do not use an S3 endpoint")
	}
	if cfg.Bucket == "" {
		return ObjectStoreConfig{}, fmt.Errorf("object-store bucket is required")
	}
	if cfg.Endpoint != "" {
		endpoint, err := url.Parse(cfg.Endpoint)
		if err != nil || endpoint.Scheme == "" || endpoint.Host == "" {
			return ObjectStoreConfig{}, fmt.Errorf("object-store endpoint must be a valid absolute URL")
		}
		if endpoint.Scheme != "http" && endpoint.Scheme != "https" {
			return ObjectStoreConfig{}, fmt.Errorf("object-store endpoint must use http or https")
		}
	}
	cfg.RoleARN = strings.TrimSpace(cfg.RoleARN)
	cfg.WebIdentityTokenFile = strings.TrimSpace(cfg.WebIdentityTokenFile)
	if cfg.RoleARN != "" && cfg.AuthMode != tsconfig.ObjectStoreAuthAWSDefault {
		return ObjectStoreConfig{}, fmt.Errorf("object-store role ARN requires auth %s", tsconfig.ObjectStoreAuthAWSDefault)
	}
	if cfg.WebIdentityTokenFile != "" && cfg.AuthMode != tsconfig.ObjectStoreAuthAWSDefault {
		return ObjectStoreConfig{}, fmt.Errorf("object-store web identity token file requires auth %s", tsconfig.ObjectStoreAuthAWSDefault)
	}
	if cfg.WebIdentityTokenFile != "" && cfg.RoleARN == "" {
		return ObjectStoreConfig{}, fmt.Errorf("object-store web identity token file requires a role ARN")
	}
	if cfg.WebIdentityTokenFile != "" {
		body, err := os.ReadFile(cfg.WebIdentityTokenFile)
		if err != nil {
			return ObjectStoreConfig{}, fmt.Errorf("object-store web identity token file: %w", err)
		}
		if strings.TrimSpace(string(body)) == "" {
			return ObjectStoreConfig{}, fmt.Errorf("object-store web identity token file %s is empty", cfg.WebIdentityTokenFile)
		}
	}
	if cfg.Prefix == "" {
		cfg.Prefix = "network/"
	}
	// aws_default uses the SDK region, or the region on the config. The
	// historical garage default is only for the static S3-compatible path.
	if cfg.Region == "" && cfg.AuthMode != tsconfig.ObjectStoreAuthAWSDefault && cfg.AuthMode != tsconfig.ObjectStoreAuthGCSADC {
		cfg.Region = "garage"
	}
	if cfg.Lookback <= 0 {
		cfg.Lookback = 15 * time.Minute
	}
	if cfg.MaxObjects <= 0 {
		cfg.MaxObjects = 500
	}
	return cfg, nil
}

func loadObjectStoreAWSConfig(ctx context.Context, cfg ObjectStoreConfig) (aws.Config, error) {
	if cfg.AuthMode == tsconfig.ObjectStoreAuthAWSDefault && strings.TrimSpace(cfg.WebIdentityTokenFile) != "" {
		// A token file is this tailnet's identity. LoadDefaultConfig would
		// read AWS_ROLE_ARN and AWS_WEB_IDENTITY_TOKEN_FILE and apply one
		// role to every client in the process.
		return loadWebIdentityAWSConfig(cfg)
	}
	loadOptions := []func(*config.LoadOptions) error{}
	if cfg.Region != "" {
		loadOptions = append(loadOptions, config.WithRegion(cfg.Region))
	}
	// aws_default must not install a static provider. Keys copied from
	// AWS_ACCESS_KEY_ID belong to the SDK chain, and tsflow static key
	// variables are rejected before this runs.
	if cfg.AuthMode != tsconfig.ObjectStoreAuthAWSDefault && (cfg.AccessKey != "" || cfg.SecretKey != "") {
		loadOptions = append(loadOptions, config.WithCredentialsProvider(
			credentials.NewStaticCredentialsProvider(cfg.AccessKey, cfg.SecretKey, ""),
		))
	}
	awsCfg, err := config.LoadDefaultConfig(ctx, loadOptions...)
	if err != nil {
		return aws.Config{}, fmt.Errorf("failed to load object-store config: %w", err)
	}
	if cfg.AuthMode == tsconfig.ObjectStoreAuthAWSDefault && strings.TrimSpace(cfg.RoleARN) != "" {
		// Cache the assumed role. Without this, every GetObject calls STS.
		awsCfg.Credentials = aws.NewCredentialsCache(stscreds.NewAssumeRoleProvider(
			sts.NewFromConfig(awsCfg),
			strings.TrimSpace(cfg.RoleARN),
			func(options *stscreds.AssumeRoleOptions) {
				options.RoleSessionName = "tsflow"
			},
		))
	}
	return awsCfg, nil
}

// tokenFileRetriever reads an OIDC token at credential refresh time and
// drops surrounding whitespace. Projected tokens and Google ID tokens often
// end with a newline. stscreds.IdentityTokenFile does not trim.
type tokenFileRetriever string

func (f tokenFileRetriever) GetIdentityToken() ([]byte, error) {
	body, err := os.ReadFile(string(f))
	if err != nil {
		return nil, fmt.Errorf("web identity token file: %w", err)
	}
	token := bytes.TrimSpace(body)
	if len(token) == 0 {
		return nil, fmt.Errorf("web identity token file %s is empty", string(f))
	}
	return token, nil
}

func loadWebIdentityAWSConfig(cfg ObjectStoreConfig) (aws.Config, error) {
	region := strings.TrimSpace(cfg.Region)
	roleARN := strings.TrimSpace(cfg.RoleARN)
	tokenFile := strings.TrimSpace(cfg.WebIdentityTokenFile)
	options := sts.Options{Region: region}
	if endpoint := strings.TrimSpace(cfg.stsEndpoint); endpoint != "" {
		options.BaseEndpoint = aws.String(endpoint)
	}
	// Do not use config.LoadDefaultConfig here. That chain reads
	// AWS_ACCESS_KEY_ID, AWS_ROLE_ARN, and AWS_WEB_IDENTITY_TOKEN_FILE, so
	// every tailnet in the process would assume the same role.
	// AssumeRoleWithWebIdentity is unsigned. The OIDC token is the credential.
	client := sts.New(options)
	provider := stscreds.NewWebIdentityRoleProvider(
		client,
		roleARN,
		tokenFileRetriever(tokenFile),
		func(o *stscreds.WebIdentityRoleOptions) {
			o.RoleSessionName = "tsflow"
		},
	)
	return aws.Config{
		Region:      region,
		Credentials: aws.NewCredentialsCache(provider),
	}, nil
}

func (s *ObjectStoreSource) Poll(ctx context.Context, p *Poller, start, end time.Time) (int, int, time.Time, error) {
	if p == nil || p.store == nil {
		return 0, 0, start, fmt.Errorf("poller store is required")
	}
	// Metadata hydration repairs an auxiliary index for already-ingested
	// objects. A stale or unreadable historical object must not prevent newer
	// objects from being listed and ingested, so retain the error and continue.
	if err := s.hydrateMissingMetadata(ctx, p); err != nil {
		log.Printf("Warning: historical metadata hydration incomplete: %v", err)
	}

	objects, err := s.listObjects(ctx, start.Add(-s.cfg.Lookback), end)
	if err != nil {
		return 0, 0, time.Time{}, err
	}
	if len(objects) == 0 {
		return 0, 0, end, nil
	}
	if len(objects) > s.cfg.MaxObjects {
		log.Printf("Object-store poll found %d candidate objects; processing up to %d oldest un-ingested objects", len(objects), s.cfg.MaxObjects)
	}

	processedObjects := 0
	processedFlows := 0
	lastProcessed := start
	var firstReadErr error
	var blockedAt time.Time
	// Select un-ingested objects after checking the ingestion guard. This keeps
	// a timestamp-only cursor progressing even when more than MaxObjects share
	// the same timestamp and the first page has already been seen.
	selected := make([]flowObject, 0, minInt(len(objects), s.cfg.MaxObjects))
	for _, obj := range objects {
		seen, err := p.store.IsObjectIngested(ctx, p.tailnetIDOrDefault(), obj.key)
		if err != nil {
			return processedObjects, processedFlows, lastProcessed, err
		}
		if seen {
			if obj.logTime.After(lastProcessed) {
				lastProcessed = obj.logTime
			}
			continue
		}
		if len(selected) >= s.cfg.MaxObjects {
			break
		}
		selected = append(selected, obj)
	}

	for _, obj := range selected {
		flows, nodeMetadata, err := s.readFlowLogs(ctx, p, obj.key)
		if err != nil {
			if ctx.Err() != nil {
				return processedObjects, processedFlows, lastProcessed, ctx.Err()
			}
			// Only failures that repeat on every retry count toward the
			// budget. Authorization, server, and network errors do not, so an
			// outage never drops data.
			if permanentReadError(err) {
				if attempts := s.recordReadFailure(obj.key); attempts >= maxObjectReadAttempts {
					log.Printf("Warning: giving up on object %s after %d failed reads; recording it with no flows: %v", obj.key, attempts, err)
					if err := p.store.CommitObjectIngest(ctx, p.tailnetIDOrDefault(), database.ObjectIngestResult{
						Key:          obj.key,
						LastModified: obj.lastModified,
						Size:         obj.size,
					}); err != nil {
						return processedObjects, processedFlows, lastProcessed, err
					}
					s.clearReadFailure(obj.key)
					if obj.logTime.After(lastProcessed) {
						lastProcessed = obj.logTime
					}
					continue
				}
			}
			if firstReadErr == nil {
				firstReadErr = fmt.Errorf("failed to read %s: %w", obj.key, err)
			}
			if blockedAt.IsZero() || obj.logTime.Before(blockedAt) {
				blockedAt = obj.logTime
			}
			log.Printf("Warning: skipping unreadable object %s; later objects will still be attempted: %v", obj.key, err)
			continue
		}
		s.clearReadFailure(obj.key)
		if len(nodeMetadata) > 0 {
			p.deviceCache.UpsertNodeMetadata(nodeMetadata)
		}
		nodePairs, totalBandwidth, nodeBandwidth, trafficStats := p.aggregate(flows)
		pollEnd := obj.logTime
		if pollEnd.IsZero() {
			pollEnd = end
		}
		if err := p.store.CommitObjectIngest(ctx, p.tailnetIDOrDefault(), database.ObjectIngestResult{
			Key:           obj.key,
			LastModified:  obj.lastModified,
			Size:          obj.size,
			FlowCount:     len(flows),
			NodeMetadata:  nodeMetadata,
			NodePairs:     nodePairs,
			Bandwidth:     totalBandwidth,
			NodeBandwidth: nodeBandwidth,
			TrafficStats:  trafficStats,
		}); err != nil {
			return processedObjects, processedFlows, lastProcessed, err
		}

		p.rollingCache.Update(nodePairs, totalBandwidth, nodeBandwidth, trafficStats)
		processedObjects++
		processedFlows += len(flows)
		if pollEnd.After(lastProcessed) {
			lastProcessed = pollEnd
		}
	}
	// Keep the cursor at the earliest unreadable object so a transient or
	// repaired source object is retried on the next poll. Objects after it may
	// still be ingested, but a failure must not silently advance past the gap.
	if !blockedAt.IsZero() && blockedAt.Before(lastProcessed) {
		lastProcessed = blockedAt
	}

	return processedObjects, processedFlows, lastProcessed, firstReadErr
}

func (s *ObjectStoreSource) recordReadFailure(key string) int {
	s.failuresMu.Lock()
	defer s.failuresMu.Unlock()
	if s.readFailures == nil {
		s.readFailures = make(map[string]int)
	}
	s.readFailures[key]++
	return s.readFailures[key]
}

func (s *ObjectStoreSource) clearReadFailure(key string) {
	s.failuresMu.Lock()
	defer s.failuresMu.Unlock()
	delete(s.readFailures, key)
}

// hydrateMissingMetadata repairs historical node identities independently of
// the flow cursor and S3 lookback. This matters after a partial metadata-table
// loss or when a database created before metadata hydration is upgraded.
func (s *ObjectStoreSource) hydrateMissingMetadata(ctx context.Context, p *Poller) error {
	keys, err := p.store.GetObjectsNeedingMetadata(ctx, p.tailnetIDOrDefault(), s.cfg.MaxObjects)
	if err != nil {
		return err
	}
	var firstErr error
	for _, key := range keys {
		_, nodeMetadata, err := s.readFlowLogs(ctx, p, key)
		if err != nil {
			if firstErr == nil {
				firstErr = fmt.Errorf("failed to hydrate metadata from %s: %w", key, err)
			}
			continue
		}
		if len(nodeMetadata) > 0 {
			p.deviceCache.UpsertNodeMetadata(nodeMetadata)
			if err := p.store.UpsertNodeMetadata(ctx, p.tailnetIDOrDefault(), nodeMetadata); err != nil {
				if firstErr == nil {
					firstErr = fmt.Errorf("failed to persist metadata from %s: %w", key, err)
				}
				continue
			}
		}
		if err := p.store.MarkObjectMetadataHydrated(ctx, p.tailnetIDOrDefault(), key, objectMetadataIDs(nodeMetadata)); err != nil {
			if firstErr == nil {
				firstErr = fmt.Errorf("failed to mark metadata hydrated for %s: %w", key, err)
			}
		}
	}
	return firstErr
}

func objectMetadataIDs(nodes []database.NodeMetadata) []string {
	ids := make([]string, 0, len(nodes))
	for _, node := range nodes {
		if node.NodeID != "" {
			ids = append(ids, node.NodeID)
		}
	}
	return ids
}

func minInt(left, right int) int {
	if left < right {
		return left
	}
	return right
}

func (s *ObjectStoreSource) listObjects(ctx context.Context, start, end time.Time) ([]flowObject, error) {
	prefixes := dayPrefixes(s.cfg.Prefix, start, end)
	var objects []flowObject
	for _, prefix := range prefixes {
		listed, err := s.blobs.List(ctx, prefix)
		if err != nil {
			return nil, fmt.Errorf("failed to list %s: %w", prefix, err)
		}
		for _, item := range listed {
			logTime, ok := objectTime(item.Key)
			if !ok || logTime.Before(start) || !logTime.Before(end) {
				continue
			}
			objects = append(objects, flowObject{
				key:          item.Key,
				lastModified: item.LastModified,
				size:         item.Size,
				logTime:      logTime,
			})
		}
	}
	sort.Slice(objects, func(i, j int) bool {
		if objects[i].logTime.Equal(objects[j].logTime) {
			return objects[i].key < objects[j].key
		}
		return objects[i].logTime.Before(objects[j].logTime)
	})
	return objects, nil
}

func (s *ObjectStoreSource) readFlowLogs(ctx context.Context, p *Poller, key string) ([]database.FlowLog, []database.NodeMetadata, error) {
	body, err := s.blobs.Open(ctx, key)
	if err != nil {
		return nil, nil, err
	}
	defer body.Close()

	var reader io.Reader = body
	var zstdReader *zstd.Decoder
	var gzipReader *gzip.Reader
	switch {
	case strings.HasSuffix(key, ".zst") || strings.HasSuffix(key, ".zstd"):
		zstdReader, err = zstd.NewReader(body)
		if err != nil {
			return nil, nil, objectContentError{fmt.Errorf("failed to create zstd reader: %w", err)}
		}
		defer zstdReader.Close()
		reader = zstdReader
	case strings.HasSuffix(key, ".gz") || strings.HasSuffix(key, ".gzip"):
		gzipReader, err = gzip.NewReader(body)
		if err != nil {
			if ctx.Err() != nil {
				return nil, nil, ctx.Err()
			}
			return nil, nil, objectContentError{fmt.Errorf("failed to create gzip reader: %w", err)}
		}
		defer gzipReader.Close()
		reader = gzipReader
	}

	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 0, 256*1024), 32*1024*1024)

	var flows []database.FlowLog
	nodeMetadata := make(map[string]database.NodeMetadata)
	lineNo := 0
	for scanner.Scan() {
		lineNo++
		line := scanner.Bytes()
		if len(strings.TrimSpace(string(line))) == 0 {
			continue
		}
		var logMap map[string]any
		if err := json.Unmarshal(line, &logMap); err != nil {
			return nil, nil, objectContentError{fmt.Errorf("line %d: %w", lineNo, err)}
		}
		p.deviceCache.UpsertFromFlowLogMetadata(logMap)
		for _, node := range extractNodeMetadata(logMap) {
			nodeMetadata[node.NodeID] = node
		}
		flows = append(flows, p.convertMapLog(logMap)...)
	}
	if err := scanner.Err(); err != nil {
		if ctx.Err() != nil {
			return nil, nil, ctx.Err()
		}
		return nil, nil, objectContentError{err}
	}
	log.Printf("Read %d flow rows from %s", len(flows), key)
	nodes := make([]database.NodeMetadata, 0, len(nodeMetadata))
	for _, node := range nodeMetadata {
		nodes = append(nodes, node)
	}
	return flows, nodes, nil
}

func extractNodeMetadata(logMap map[string]any) []database.NodeMetadata {
	var nodes []database.NodeMetadata
	if src, ok := logMap["srcNode"].(map[string]any); ok {
		if node := parseNodeMetadata(src); node.NodeID != "" {
			nodes = append(nodes, node)
		}
	}
	if dstNodes, ok := logMap["dstNodes"].([]any); ok {
		for _, item := range dstNodes {
			if rawNode, ok := item.(map[string]any); ok {
				if node := parseNodeMetadata(rawNode); node.NodeID != "" {
					nodes = append(nodes, node)
				}
			}
		}
	}
	return nodes
}

func parseNodeMetadata(raw map[string]any) database.NodeMetadata {
	id, _ := raw["nodeId"].(string)
	name, _ := raw["name"].(string)
	hostname, _ := raw["hostname"].(string)
	if hostname == "" {
		hostname = name
		if dot := strings.Index(hostname, "."); dot > 0 {
			hostname = hostname[:dot]
		}
	}
	owner, _ := raw["user"].(string)
	if owner == "" {
		owner, _ = raw["owner"].(string)
	}

	return database.NodeMetadata{
		NodeID:   id,
		Name:     name,
		Hostname: hostname,
		Owner:    owner,
		IPs:      stringSlice(raw["addresses"]),
		Tags:     stringSlice(raw["tags"]),
	}
}

func stringSlice(raw any) []string {
	values, ok := raw.([]any)
	if !ok {
		return nil
	}
	result := make([]string, 0, len(values))
	for _, value := range values {
		if str, ok := value.(string); ok && str != "" {
			result = append(result, str)
		}
	}
	return result
}

func dayPrefixes(basePrefix string, start, end time.Time) []string {
	basePrefix = strings.TrimPrefix(basePrefix, "/")
	basePrefix = strings.TrimSuffix(basePrefix, "/")
	day := time.Date(start.UTC().Year(), start.UTC().Month(), start.UTC().Day(), 0, 0, 0, 0, time.UTC)
	lastDay := time.Date(end.UTC().Year(), end.UTC().Month(), end.UTC().Day(), 0, 0, 0, 0, time.UTC)

	var prefixes []string
	for !day.After(lastDay) {
		prefixes = append(prefixes, path.Join(basePrefix, day.Format("2006/01/02"))+"/")
		day = day.AddDate(0, 0, 1)
	}
	return prefixes
}

func objectTime(key string) (time.Time, bool) {
	name := path.Base(key)
	name = strings.TrimSuffix(name, ".zst")
	name = strings.TrimSuffix(name, ".zstd")
	name = strings.TrimSuffix(name, ".gz")
	name = strings.TrimSuffix(name, ".gzip")
	name = strings.TrimSuffix(name, ".ndjson")
	t, err := time.ParseInLocation("2006-01-02-15-04-05", name, time.UTC)
	if err != nil {
		return time.Time{}, false
	}
	return t, true
}
