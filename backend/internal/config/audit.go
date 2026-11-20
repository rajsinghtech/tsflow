package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// Audit backends. off (or empty) leaves a tailnet without audit ingest.
const (
	AuditBackendOff = "off"
	AuditBackendGCS = "gcs"
	AuditBackendS3  = "s3"
	AuditBackendAPI = "api"
)

// Audit defaults.
const (
	DefaultAuditRetention    = 400 * 24 * time.Hour
	DefaultAuditPollInterval = 2 * time.Minute
	DefaultAuditBackfill     = 90 * 24 * time.Hour
	DefaultAuditMaxObjects   = 500
	DefaultAuditPrefix       = "configuration/"
)

// AuditSource is where one tailnet's configuration audit logs come from.
// Empty fields inherit the process values (TSFLOW_AUDIT_*).
type AuditSource struct {
	Backend              string
	Bucket               string
	Prefix               string
	Region               string
	Endpoint             string
	Auth                 string
	RoleARN              string
	WebIdentityTokenFile string
}

// Audit holds the configuration audit log settings. Audit support is off
// unless TSFLOW_AUDIT_ENABLED is true.
type Audit struct {
	Enabled      bool
	DBPath       string
	Retention    time.Duration
	PollInterval time.Duration
	Backfill     time.Duration
	MaxObjects   int
	// Viewers are logins and group names allowed to see audit data when
	// access control is on. An empty list allows nobody.
	Viewers []string
	// Default is the process-wide source from TSFLOW_AUDIT_BACKEND and
	// friends.
	Default AuditSource
}

// LoadAudit reads the TSFLOW_AUDIT_* settings. flowDBPath places audit.db
// next to the flow database when TSFLOW_AUDIT_DB_PATH is not set.
func LoadAudit(flowDBPath string) (Audit, error) {
	a := Audit{
		Retention:    DefaultAuditRetention,
		PollInterval: DefaultAuditPollInterval,
		Backfill:     DefaultAuditBackfill,
		MaxObjects:   DefaultAuditMaxObjects,
	}
	if raw := strings.TrimSpace(os.Getenv("TSFLOW_AUDIT_ENABLED")); raw != "" {
		enabled, err := strconv.ParseBool(raw)
		if err != nil {
			return Audit{}, fmt.Errorf("TSFLOW_AUDIT_ENABLED: %w", err)
		}
		a.Enabled = enabled
	}
	a.DBPath = strings.TrimSpace(os.Getenv("TSFLOW_AUDIT_DB_PATH"))
	if a.DBPath == "" {
		dir := "."
		if flowDBPath != "" {
			dir = filepath.Dir(flowDBPath)
		}
		a.DBPath = filepath.Join(dir, "audit.db")
	}
	var err error
	if a.Retention, err = durationEnv("TSFLOW_AUDIT_RETENTION", a.Retention); err != nil {
		return Audit{}, err
	}
	if a.PollInterval, err = durationEnv("TSFLOW_AUDIT_POLL_INTERVAL", a.PollInterval); err != nil {
		return Audit{}, err
	}
	if a.PollInterval < 10*time.Second {
		return Audit{}, fmt.Errorf("TSFLOW_AUDIT_POLL_INTERVAL must be at least 10s")
	}
	if a.Backfill, err = durationEnv("TSFLOW_AUDIT_BACKFILL", a.Backfill); err != nil {
		return Audit{}, err
	}
	if raw := strings.TrimSpace(os.Getenv("TSFLOW_AUDIT_MAX_OBJECTS_PER_POLL")); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n <= 0 {
			return Audit{}, fmt.Errorf("TSFLOW_AUDIT_MAX_OBJECTS_PER_POLL must be a positive integer")
		}
		a.MaxObjects = n
	}
	a.Viewers = splitList(os.Getenv("TSFLOW_AUDIT_VIEWERS"))
	a.Default = AuditSource{
		Backend:              strings.ToLower(strings.TrimSpace(os.Getenv("TSFLOW_AUDIT_BACKEND"))),
		Bucket:               strings.TrimSpace(os.Getenv("TSFLOW_AUDIT_BUCKET")),
		Prefix:               strings.TrimSpace(os.Getenv("TSFLOW_AUDIT_PREFIX")),
		Region:               strings.TrimSpace(os.Getenv("TSFLOW_AUDIT_REGION")),
		Endpoint:             strings.TrimSpace(os.Getenv("TSFLOW_AUDIT_ENDPOINT")),
		Auth:                 strings.ToLower(strings.TrimSpace(os.Getenv("TSFLOW_AUDIT_AUTH"))),
		RoleARN:              strings.TrimSpace(os.Getenv("TSFLOW_AUDIT_ROLE_ARN")),
		WebIdentityTokenFile: strings.TrimSpace(os.Getenv("TSFLOW_AUDIT_WEB_IDENTITY_TOKEN_FILE")),
	}
	if err := validateAuditBackend("TSFLOW_AUDIT_BACKEND", a.Default.Backend); err != nil {
		return Audit{}, err
	}
	return a, nil
}

// ResolveAudit returns the source for one tailnet. A tailnet with an audit
// block inherits empty fields from def. A tailnet without one uses def only
// when it is the only tailnet, so a shared default never points several
// tailnets at the same prefix.
func (s TailnetSpec) ResolveAudit(def AuditSource, onlyTailnet bool) AuditSource {
	if s.Audit == nil {
		if !onlyTailnet {
			return AuditSource{Backend: AuditBackendOff}
		}
		return finishAuditSource(def)
	}
	out := *s.Audit
	if out.Backend == "" {
		out.Backend = def.Backend
	}
	if out.Bucket == "" {
		out.Bucket = def.Bucket
	}
	if out.Prefix == "" {
		out.Prefix = def.Prefix
	}
	if out.Region == "" {
		out.Region = def.Region
	}
	if out.Endpoint == "" {
		out.Endpoint = def.Endpoint
	}
	if out.Auth == "" {
		out.Auth = def.Auth
	}
	if out.RoleARN == "" {
		out.RoleARN = def.RoleARN
	}
	if out.WebIdentityTokenFile == "" {
		out.WebIdentityTokenFile = def.WebIdentityTokenFile
	}
	return finishAuditSource(out)
}

func finishAuditSource(src AuditSource) AuditSource {
	if src.Backend == "" {
		src.Backend = AuditBackendOff
	}
	if (src.Backend == AuditBackendGCS || src.Backend == AuditBackendS3) && src.Prefix == "" {
		src.Prefix = DefaultAuditPrefix
	}
	if src.Backend == AuditBackendGCS && src.Auth == "" {
		src.Auth = ObjectStoreAuthGCSADC
	}
	return src
}

func validateAuditBackend(where, backend string) error {
	switch backend {
	case "", AuditBackendOff, AuditBackendGCS, AuditBackendS3, AuditBackendAPI:
		return nil
	}
	return fmt.Errorf("%s: audit backend %q must be gcs, s3, api or off", where, backend)
}

// durationEnv parses a Go duration, or a whole number of days such as 400d.
func durationEnv(key string, def time.Duration) (time.Duration, error) {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return def, nil
	}
	d, err := ParseLongDuration(raw)
	if err != nil {
		return 0, fmt.Errorf("%s: %w", key, err)
	}
	return d, nil
}

// FormatLongDuration writes whole days as 400d and anything else as a Go
// duration, the reverse of ParseLongDuration.
func FormatLongDuration(d time.Duration) string {
	if d > 0 && d%(24*time.Hour) == 0 {
		return fmt.Sprintf("%dd", d/(24*time.Hour))
	}
	return d.String()
}

// ParseLongDuration accepts Go durations (72h) and day counts (400d).
func ParseLongDuration(raw string) (time.Duration, error) {
	raw = strings.TrimSpace(raw)
	if days, ok := strings.CutSuffix(raw, "d"); ok {
		n, err := strconv.Atoi(days)
		if err != nil || n < 0 {
			return 0, fmt.Errorf("invalid duration %q", raw)
		}
		return time.Duration(n) * 24 * time.Hour, nil
	}
	d, err := time.ParseDuration(raw)
	if err != nil || d < 0 {
		return 0, fmt.Errorf("invalid duration %q", raw)
	}
	return d, nil
}

var tailnetAuditFields = map[string]struct{}{
	"backend":                 {},
	"bucket":                  {},
	"prefix":                  {},
	"region":                  {},
	"endpoint":                {},
	"s3_auth":                 {},
	"role_arn":                {},
	"web_identity_token_file": {},
}

func parseAuditBlock(raw any, where string) (*AuditSource, error) {
	fields, ok := raw.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("%s audit must be an object", where)
	}
	for key := range fields {
		if _, known := tailnetAuditFields[key]; !known {
			return nil, fmt.Errorf("%s audit: unknown field %q", where, key)
		}
	}
	where += " audit"
	var src AuditSource
	var err error
	for _, f := range []struct {
		key string
		dst *string
	}{
		{"backend", &src.Backend},
		{"bucket", &src.Bucket},
		{"prefix", &src.Prefix},
		{"region", &src.Region},
		{"endpoint", &src.Endpoint},
		{"s3_auth", &src.Auth},
		{"role_arn", &src.RoleARN},
		{"web_identity_token_file", &src.WebIdentityTokenFile},
	} {
		if *f.dst, err = optionalString(fields, f.key, where); err != nil {
			return nil, err
		}
		*f.dst = strings.TrimSpace(*f.dst)
	}
	src.Backend = strings.ToLower(src.Backend)
	src.Auth = strings.ToLower(src.Auth)
	if err := validateAuditBackend(where, src.Backend); err != nil {
		return nil, err
	}
	return &src, nil
}
