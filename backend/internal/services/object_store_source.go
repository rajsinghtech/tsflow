package services

import (
	"bufio"
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/url"
	"path"
	"sort"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/klauspost/compress/zstd"
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
}

type ObjectStoreSource struct {
	cfg    ObjectStoreConfig
	client *s3.Client
}

type flowObject struct {
	key          string
	lastModified time.Time
	size         int64
	logTime      time.Time
}

func NewObjectStoreSource(ctx context.Context, cfg ObjectStoreConfig) (*ObjectStoreSource, error) {
	if cfg.Bucket == "" {
		return nil, fmt.Errorf("object-store bucket is required")
	}
	if cfg.Endpoint != "" {
		endpoint, err := url.Parse(cfg.Endpoint)
		if err != nil || endpoint.Scheme == "" || endpoint.Host == "" {
			return nil, fmt.Errorf("object-store endpoint must be a valid absolute URL")
		}
		if endpoint.Scheme != "http" && endpoint.Scheme != "https" {
			return nil, fmt.Errorf("object-store endpoint must use http or https")
		}
	}
	if cfg.Prefix == "" {
		cfg.Prefix = "network/"
	}
	if cfg.Region == "" {
		cfg.Region = "garage"
	}
	if cfg.Lookback <= 0 {
		cfg.Lookback = 15 * time.Minute
	}
	if cfg.MaxObjects <= 0 {
		cfg.MaxObjects = 500
	}

	loadOptions := []func(*config.LoadOptions) error{
		config.WithRegion(cfg.Region),
	}
	if cfg.AccessKey != "" || cfg.SecretKey != "" {
		loadOptions = append(loadOptions, config.WithCredentialsProvider(
			credentials.NewStaticCredentialsProvider(cfg.AccessKey, cfg.SecretKey, ""),
		))
	}
	awsCfg, err := config.LoadDefaultConfig(ctx, loadOptions...)
	if err != nil {
		return nil, fmt.Errorf("failed to load object-store config: %w", err)
	}

	client := s3.NewFromConfig(awsCfg, func(o *s3.Options) {
		o.UsePathStyle = cfg.UsePathStyle
		if cfg.Endpoint != "" {
			o.BaseEndpoint = aws.String(cfg.Endpoint)
		}
	})

	return &ObjectStoreSource{cfg: cfg, client: client}, nil
}

func (s *ObjectStoreSource) Poll(ctx context.Context, p *Poller, start, end time.Time) (int, int, time.Time, error) {
	if p == nil || p.store == nil {
		return 0, 0, start, fmt.Errorf("poller store is required")
	}
	if err := s.hydrateMissingMetadata(ctx, p); err != nil {
		return 0, 0, start, err
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
	// Select un-ingested objects after checking the ingestion guard. This keeps
	// a timestamp-only cursor progressing even when more than MaxObjects share
	// the same timestamp and the first page has already been seen.
	selected := make([]flowObject, 0, minInt(len(objects), s.cfg.MaxObjects))
	for _, obj := range objects {
		seen, err := p.store.IsObjectIngested(ctx, obj.key)
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
			return processedObjects, processedFlows, lastProcessed, fmt.Errorf("failed to read %s: %w", obj.key, err)
		}
		if len(nodeMetadata) > 0 {
			p.deviceCache.UpsertNodeMetadata(nodeMetadata)
		}
		nodePairs, totalBandwidth, nodeBandwidth, trafficStats := p.aggregate(flows)
		pollEnd := obj.logTime
		if pollEnd.IsZero() {
			pollEnd = end
		}
		if err := p.store.CommitObjectIngest(ctx, database.ObjectIngestResult{
			Key:           obj.key,
			LastModified:  obj.lastModified,
			Size:          obj.size,
			FlowCount:     len(flows),
			NodeMetadata:  nodeMetadata,
			NodePairs:     nodePairs,
			Bandwidth:     totalBandwidth,
			NodeBandwidth: nodeBandwidth,
			TrafficStats:  trafficStats,
			PollEnd:       pollEnd,
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

	return processedObjects, processedFlows, lastProcessed, nil
}

// hydrateMissingMetadata repairs historical node identities independently of
// the flow cursor and S3 lookback. This matters after a partial metadata-table
// loss or when a database created before metadata hydration is upgraded.
func (s *ObjectStoreSource) hydrateMissingMetadata(ctx context.Context, p *Poller) error {
	keys, err := p.store.GetObjectsNeedingMetadata(ctx, s.cfg.MaxObjects)
	if err != nil {
		return err
	}
	for _, key := range keys {
		_, nodeMetadata, err := s.readFlowLogs(ctx, p, key)
		if err != nil {
			return fmt.Errorf("failed to hydrate metadata from %s: %w", key, err)
		}
		if len(nodeMetadata) > 0 {
			p.deviceCache.UpsertNodeMetadata(nodeMetadata)
			if err := p.store.UpsertNodeMetadata(ctx, nodeMetadata); err != nil {
				return fmt.Errorf("failed to persist metadata from %s: %w", key, err)
			}
		}
		if err := p.store.MarkObjectMetadataHydrated(ctx, key, objectMetadataIDs(nodeMetadata)); err != nil {
			return fmt.Errorf("failed to mark metadata hydrated for %s: %w", key, err)
		}
	}
	return nil
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
		paginator := s3.NewListObjectsV2Paginator(s.client, &s3.ListObjectsV2Input{
			Bucket: aws.String(s.cfg.Bucket),
			Prefix: aws.String(prefix),
		})
		for paginator.HasMorePages() {
			page, err := paginator.NextPage(ctx)
			if err != nil {
				return nil, fmt.Errorf("failed to list %s: %w", prefix, err)
			}
			for _, item := range page.Contents {
				key := aws.ToString(item.Key)
				logTime, ok := objectTime(key)
				if !ok || logTime.Before(start) || logTime.After(end) {
					continue
				}
				lastModified := time.Time{}
				if item.LastModified != nil {
					lastModified = *item.LastModified
				}
				objects = append(objects, flowObject{
					key:          key,
					lastModified: lastModified,
					size:         aws.ToInt64(item.Size),
					logTime:      logTime,
				})
			}
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
	out, err := s.client.GetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(s.cfg.Bucket),
		Key:    aws.String(key),
	})
	if err != nil {
		return nil, nil, err
	}
	defer out.Body.Close()

	var reader io.Reader = out.Body
	var zstdReader *zstd.Decoder
	var gzipReader *gzip.Reader
	switch {
	case strings.HasSuffix(key, ".zst") || strings.HasSuffix(key, ".zstd"):
		zstdReader, err = zstd.NewReader(out.Body)
		if err != nil {
			return nil, nil, fmt.Errorf("failed to create zstd reader: %w", err)
		}
		defer zstdReader.Close()
		reader = zstdReader
	case strings.HasSuffix(key, ".gz") || strings.HasSuffix(key, ".gzip"):
		gzipReader, err = gzip.NewReader(out.Body)
		if err != nil {
			return nil, nil, fmt.Errorf("failed to create gzip reader: %w", err)
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
			return nil, nil, fmt.Errorf("line %d: %w", lineNo, err)
		}
		p.deviceCache.UpsertFromFlowLogMetadata(logMap)
		for _, node := range extractNodeMetadata(logMap) {
			nodeMetadata[node.NodeID] = node
		}
		flows = append(flows, p.convertMapLog(logMap)...)
	}
	if err := scanner.Err(); err != nil {
		return nil, nil, err
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
