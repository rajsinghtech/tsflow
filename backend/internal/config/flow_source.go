package config

import (
	"fmt"
	"net/url"
	"os"
	"strings"
)

// FlowSource is the flow-log backend and object store for one tailnet after
// process defaults and per-entry overrides are combined.
type FlowSource struct {
	Backend              string
	Bucket               string
	Prefix               string
	Region               string
	Endpoint             string
	Auth                 string
	RoleARN              string
	WebIdentityTokenFile string
	UsePathStyle         bool
}

func (c *Config) processFlowSource() (FlowSource, error) {
	if c == nil {
		return FlowSource{}, fmt.Errorf("config is nil")
	}
	backend, err := c.EffectiveFlowBackend()
	if err != nil {
		return FlowSource{}, err
	}
	auth := c.objectStoreAuth()
	if backend == FlowBackendGCS && auth == "" {
		auth = ObjectStoreAuthGCSADC
	}
	return FlowSource{
		Backend:              backend,
		Bucket:               strings.TrimSpace(c.FlowObjectStoreBucket),
		Prefix:               c.FlowObjectStorePrefix,
		Region:               strings.TrimSpace(c.FlowObjectStoreRegion),
		Endpoint:             strings.TrimSpace(c.FlowObjectStoreEndpoint),
		Auth:                 auth,
		RoleARN:              strings.TrimSpace(c.FlowObjectStoreRoleARN),
		WebIdentityTokenFile: strings.TrimSpace(c.FlowObjectStoreWebIdentityTokenFile),
		UsePathStyle:         c.FlowObjectStorePathStyle,
	}, nil
}

// ResolveFlow overlays this tailnet's flow fields onto the process source.
// Empty fields keep the process value. prefix-only entries therefore match
// a process that has no per-tailnet flow configuration.
func (s TailnetSpec) ResolveFlow(base FlowSource) FlowSource {
	out := base
	if s.FlowBackend != "" {
		out.Backend = s.FlowBackend
	}
	if s.S3Prefix != "" {
		out.Prefix = s.S3Prefix
	}
	if s.Bucket != "" {
		out.Bucket = s.Bucket
	}
	if s.Region != "" {
		out.Region = s.Region
	}
	if s.Endpoint != "" {
		out.Endpoint = s.Endpoint
	}
	if s.ObjectStoreAuth != "" {
		out.Auth = s.ObjectStoreAuth
	}
	if s.RoleARN != "" {
		out.RoleARN = s.RoleARN
	}
	if s.WebIdentityTokenFile != "" {
		out.WebIdentityTokenFile = s.WebIdentityTokenFile
	}
	if out.Backend == FlowBackendS3 && out.Auth == "" {
		out.Auth = ObjectStoreAuthStatic
	}
	if out.Backend == FlowBackendGCS && out.Auth == "" {
		out.Auth = ObjectStoreAuthGCSADC
	}
	// Path style follows the auth mode only when this entry changes the
	// backend or the auth mode. An explicit TSFLOW_S3_PATH_STYLE wins.
	if !objectStorePathStyleSet() && flowChangesSource(s, base) {
		switch out.Auth {
		case ObjectStoreAuthAWSDefault:
			out.UsePathStyle = false
		case ObjectStoreAuthGCSADC:
		default:
			if out.Backend == FlowBackendS3 {
				out.UsePathStyle = true
			}
		}
	}
	return out
}

func flowChangesSource(spec TailnetSpec, base FlowSource) bool {
	if spec.ObjectStoreAuth != "" {
		return true
	}
	return spec.FlowBackend != "" && spec.FlowBackend != base.Backend
}

func (s TailnetSpec) hasFlowOverride() bool {
	return s.FlowBackend != "" || s.Bucket != "" || s.Region != "" || s.Endpoint != "" ||
		s.ObjectStoreAuth != "" || s.RoleARN != "" || s.WebIdentityTokenFile != ""
}

func (s TailnetSpec) setsObjectStoreFields() bool {
	return s.Bucket != "" || s.Region != "" || s.Endpoint != "" ||
		s.ObjectStoreAuth != "" || s.RoleARN != "" || s.WebIdentityTokenFile != ""
}

func validateTailnetFlow(where string, spec TailnetSpec, global *Config) error {
	switch spec.FlowBackend {
	case "", FlowBackendAPI, FlowBackendS3, FlowBackendGCS:
	default:
		return fmt.Errorf("%s flow_backend must be api, s3, or gcs", where)
	}
	switch spec.ObjectStoreAuth {
	case "", ObjectStoreAuthStatic, ObjectStoreAuthAWSDefault, ObjectStoreAuthGCSADC:
	default:
		return fmt.Errorf("%s s3_auth must be static, aws_default, or gcs_adc", where)
	}
	if !spec.hasFlowOverride() {
		return nil
	}
	if spec.FlowBackend == FlowBackendAPI {
		if spec.setsObjectStoreFields() {
			return fmt.Errorf("%s flow_backend api does not use bucket, region, endpoint, s3_auth, role_arn, or web_identity_token_file", where)
		}
		return nil
	}
	if global == nil {
		return fmt.Errorf("%s flow configuration requires process config", where)
	}
	base, err := global.processFlowSource()
	if err != nil {
		return err
	}
	flow := spec.ResolveFlow(base)
	switch flow.Backend {
	case FlowBackendAPI:
		return fmt.Errorf("%s object store settings require flow_backend s3 or gcs", where)
	case FlowBackendGCS:
		return validateGCSFlow(where, flow)
	case FlowBackendS3:
		return validateS3Flow(where, flow, global)
	default:
		return fmt.Errorf("%s flow_backend must be api, s3, or gcs", where)
	}
}

func validateGCSFlow(where string, flow FlowSource) error {
	if flow.Auth != ObjectStoreAuthGCSADC {
		return fmt.Errorf("%s flow_backend gcs requires s3_auth gcs_adc or an empty auth mode", where)
	}
	if flow.Bucket == "" {
		return fmt.Errorf("%s flow_backend gcs requires a bucket", where)
	}
	if flow.Endpoint != "" {
		return fmt.Errorf("%s flow_backend gcs does not use an endpoint", where)
	}
	if flow.RoleARN != "" || flow.WebIdentityTokenFile != "" {
		return fmt.Errorf("%s flow_backend gcs does not use role_arn or web_identity_token_file", where)
	}
	if staticObjectStoreEnvSet() {
		return fmt.Errorf("%s flow_backend gcs does not use TSFLOW_S3_ACCESS_KEY_ID or TSFLOW_S3_SECRET_ACCESS_KEY", where)
	}
	return nil
}

func validateS3Flow(where string, flow FlowSource, global *Config) error {
	switch flow.Auth {
	case ObjectStoreAuthGCSADC:
		return fmt.Errorf("%s s3_auth gcs_adc requires flow_backend gcs", where)
	case ObjectStoreAuthAWSDefault:
		return validateAWSDefaultFlow(where, flow)
	case ObjectStoreAuthStatic, "":
		if flow.RoleARN != "" {
			return fmt.Errorf("%s role_arn requires s3_auth aws_default", where)
		}
		if flow.WebIdentityTokenFile != "" {
			return fmt.Errorf("%s web_identity_token_file requires s3_auth aws_default", where)
		}
		if !hasStaticObjectStore(global, flow) {
			return fmt.Errorf("%s s3 flow backend requires a bucket, an endpoint, and static object-store keys", where)
		}
		return validateFlowEndpoint(where, flow.Endpoint, true)
	default:
		return fmt.Errorf("%s s3_auth must be static, aws_default, or gcs_adc", where)
	}
}

func validateAWSDefaultFlow(where string, flow FlowSource) error {
	if flow.Bucket == "" {
		return fmt.Errorf("%s s3_auth aws_default requires a bucket", where)
	}
	if flow.Region == "" {
		return fmt.Errorf("%s s3_auth aws_default requires a region", where)
	}
	if staticObjectStoreEnvSet() {
		return fmt.Errorf("%s s3_auth aws_default does not use TSFLOW_S3_ACCESS_KEY_ID or TSFLOW_S3_SECRET_ACCESS_KEY", where)
	}
	if err := validateFlowEndpoint(where, flow.Endpoint, false); err != nil {
		return err
	}
	if flow.WebIdentityTokenFile == "" {
		return nil
	}
	if flow.RoleARN == "" {
		return fmt.Errorf("%s web_identity_token_file requires role_arn", where)
	}
	return readNonEmptyFile(where+" web_identity_token_file", flow.WebIdentityTokenFile)
}

func hasStaticObjectStore(global *Config, flow FlowSource) bool {
	if global == nil {
		return false
	}
	return flow.Bucket != "" && flow.Endpoint != "" &&
		strings.TrimSpace(global.FlowObjectStoreAccessKey) != "" &&
		strings.TrimSpace(global.FlowObjectStoreSecretKey) != ""
}

func validateFlowEndpoint(where, endpoint string, required bool) error {
	endpoint = strings.TrimSpace(endpoint)
	if endpoint == "" {
		if required {
			return fmt.Errorf("%s endpoint must be an absolute http or https URL", where)
		}
		return nil
	}
	parsed, err := url.Parse(endpoint)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return fmt.Errorf("%s endpoint must be an absolute http or https URL", where)
	}
	return nil
}

func readNonEmptyFile(where, path string) error {
	body, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("%s: %w", where, err)
	}
	if strings.TrimSpace(string(body)) == "" {
		return fmt.Errorf("%s %s is empty", where, path)
	}
	return nil
}

func objectStorePathStyleSet() bool {
	_, ok := lookupNonEmpty("TSFLOW_S3_PATH_STYLE")
	return ok
}
