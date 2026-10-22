package cli

import (
	"flag"
	"fmt"
	"io"
	"path/filepath"
	"strings"

	"github.com/threadforge-ai/threadforge/internal/appdata"
	"github.com/threadforge-ai/threadforge/internal/forwarding"
)

func runEndpoint(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		_, _ = fmt.Fprintln(stderr, "usage: threadforge endpoint <s3|cloudwatch|gcs|splunk-hec|falcon-hec|elasticsearch|datadog|sumo|rapid7|wazuh|sentinel> <print-config|install-pack> [flags]")
		return 2
	}
	switch args[0] {
	case "s3":
		return runEndpointS3(args[1:], stdout, stderr)
	case "cloudwatch":
		return runEndpointCloudWatch(args[1:], stdout, stderr)
	case "gcs":
		return runEndpointGCS(args[1:], stdout, stderr)
	case "splunk-hec":
		return runEndpointSplunkHEC(args[1:], stdout, stderr)
	case "falcon-hec":
		return runEndpointFalconHEC(args[1:], stdout, stderr)
	case "elasticsearch":
		return runEndpointElasticsearch(args[1:], stdout, stderr)
	case "datadog":
		return runEndpointDatadog(args[1:], stdout, stderr)
	case "sumo":
		return runEndpointSumo(args[1:], stdout, stderr)
	case "rapid7":
		return runEndpointRapid7(args[1:], stdout, stderr)
	case "wazuh":
		return runEndpointWazuh(args[1:], stdout, stderr)
	case "sentinel":
		return runEndpointSentinel(args[1:], stdout, stderr)
	default:
		_, _ = fmt.Fprintln(stderr, "usage: threadforge endpoint <s3|cloudwatch|gcs|splunk-hec|falcon-hec|elasticsearch|datadog|sumo|rapid7|wazuh|sentinel> <print-config|install-pack> [flags]")
		return 2
	}
}

func runEndpointSentinel(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 || (args[0] != "print-config" && args[0] != "install-pack") {
		_, _ = fmt.Fprintln(stderr, "usage: threadforge endpoint sentinel <print-config|install-pack> --path <vector|ama|both> --allow-remote-metadata-forwarding [Azure Logs Ingestion flags] [--metadata-log-path <absolute-path>] [--log-path <events.jsonl>] [--output <new-dir>]")
		return 2
	}
	operation := args[0]
	fs := flag.NewFlagSet("endpoint sentinel "+operation, flag.ContinueOnError)
	fs.SetOutput(stderr)
	pathMode := fs.String("path", "vector", "Sentinel export path: vector, ama, or both")
	endpoint := fs.String("endpoint", "", "Azure Monitor DCE or DCR ingestion hostname")
	dcrID := fs.String("dcr-immutable-id", "", "immutable ID returned by the deployed Direct DCR")
	tenantID := fs.String("tenant-id", "", "Microsoft Entra tenant UUID (not a credential)")
	clientID := fs.String("client-id", "", "Microsoft Entra application/client UUID (not a credential)")
	logPath := fs.String("log-path", "", "local Threadforge JSONL path (defaults to the configured trace path)")
	metadataLogPath := fs.String("metadata-log-path", "", "absolute path for sanitized JSONL consumed by Azure Monitor Agent (required for ama/both)")
	secretsDir := fs.String("secrets-dir", "", "Vector directory secrets backend path (defaults to the user config directory)")
	outputDir := fs.String("output", "threadforge-sentinel-pack", "new output directory for install-pack")
	allowMetadataForwarding := fs.Bool("allow-remote-metadata-forwarding", false, "acknowledge that activation sends the strict metadata projection to Microsoft Sentinel")
	if err := fs.Parse(args[1:]); err != nil {
		return 2
	}
	if fs.NArg() != 0 {
		_, _ = fmt.Fprintln(stderr, "endpoint sentinel does not accept positional arguments")
		return 2
	}
	if !*allowMetadataForwarding {
		_, _ = fmt.Fprintln(stderr, "endpoint sentinel requires --allow-remote-metadata-forwarding; pack generation is local-only, activation sends timestamp and allowlisted severity metadata to Azure")
		return 2
	}
	selectedLogPath := strings.TrimSpace(*logPath)
	if selectedLogPath == "" {
		paths, err := appdata.Resolve("", "")
		if err != nil {
			_, _ = fmt.Fprintln(stderr, err)
			return 1
		}
		selectedLogPath = paths.Traces
	}
	if strings.TrimSpace(*metadataLogPath) != "" && !filepath.IsAbs(strings.TrimSpace(*metadataLogPath)) {
		_, _ = fmt.Fprintln(stderr, "endpoint sentinel --metadata-log-path must be absolute")
		return 2
	}
	absoluteLogPath, err := filepath.Abs(selectedLogPath)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "resolve local Threadforge log path:", err)
		return 2
	}
	options := forwarding.SentinelForwardingOptions{
		Path: *pathMode, LogPath: absoluteLogPath, MetadataLogPath: *metadataLogPath,
		Endpoint: *endpoint, DCRImmutableID: *dcrID, TenantID: *tenantID,
		ClientID: *clientID, SecretsDir: *secretsDir,
	}
	if operation == "print-config" {
		var config string
		if strings.EqualFold(strings.TrimSpace(*pathMode), "ama") {
			config, err = forwarding.RenderSentinelLocalVectorConfig(options)
		} else if strings.EqualFold(strings.TrimSpace(*pathMode), "both") {
			_, _ = fmt.Fprintln(stderr, "endpoint sentinel print-config does not support --path both; use install-pack to write both Vector configs")
			return 2
		} else {
			config, err = forwarding.RenderSentinelVectorConfig(options)
		}
		if err != nil {
			_, _ = fmt.Fprintln(stderr, err)
			return 2
		}
		_, _ = fmt.Fprintln(stdout, config)
		return 0
	}
	installed, err := forwarding.InstallSentinelPack(*outputDir, options)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, err)
		return 1
	}
	_, _ = fmt.Fprintf(stdout, "Sentinel metadata-only pack written locally to %s; Vector was not installed or started and no network request was made.\n", installed)
	return 0
}

func runEndpointWazuh(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 || (args[0] != "print-config" && args[0] != "install-pack" && args[0] != "validate") {
		_, _ = fmt.Fprintln(stderr, "usage: threadforge endpoint wazuh <print-config|install-pack|validate> --allow-remote-metadata-forwarding [--metadata-log-path <absolute-path>] [--log-path <events.jsonl>] [--output <new-dir>]")
		return 2
	}
	operation := args[0]
	fs := flag.NewFlagSet("endpoint wazuh "+operation, flag.ContinueOnError)
	fs.SetOutput(stderr)
	logPath := fs.String("log-path", "", "local Threadforge JSONL path (defaults to the configured trace path)")
	metadataLogPath := fs.String("metadata-log-path", "", "absolute path for a separate sanitized JSONL file consumed by Wazuh")
	outputDir := fs.String("output", "threadforge-wazuh-pack", "new output directory for install-pack")
	allowMetadataForwarding := fs.Bool("allow-remote-metadata-forwarding", false, "acknowledge that downstream Wazuh may forward the exported metadata off-device")
	if err := fs.Parse(args[1:]); err != nil {
		return 2
	}
	if fs.NArg() != 0 {
		_, _ = fmt.Fprintln(stderr, "endpoint wazuh does not accept positional arguments")
		return 2
	}
	if !*allowMetadataForwarding {
		_, _ = fmt.Fprintln(stderr, "endpoint wazuh requires --allow-remote-metadata-forwarding because Wazuh may forward ingested metadata to a remote manager")
		return 2
	}
	selectedLogPath := strings.TrimSpace(*logPath)
	if selectedLogPath == "" {
		paths, err := appdata.Resolve("", "")
		if err != nil {
			_, _ = fmt.Fprintln(stderr, err)
			return 1
		}
		selectedLogPath = paths.Traces
	}
	if operation == "validate" {
		tracePath, err := appendWazuhValidationEvent(selectedLogPath)
		if err != nil {
			_, _ = fmt.Fprintln(stderr, err)
			return 1
		}
		_, _ = fmt.Fprintf(stdout, "Synthetic Wazuh validation event appended to local Threadforge trace %s; no network request was made. Vector exports only timestamp and allowlisted severity metadata, but Wazuh may forward it to a remote manager. Expected informational rule: 119510.\n", tracePath)
		return 0
	}
	if strings.TrimSpace(*metadataLogPath) == "" {
		_, _ = fmt.Fprintln(stderr, "endpoint wazuh requires --metadata-log-path pointing to a separate absolute output file")
		return 2
	}
	if !filepath.IsAbs(strings.TrimSpace(*metadataLogPath)) {
		_, _ = fmt.Fprintln(stderr, "endpoint wazuh --metadata-log-path must be absolute")
		return 2
	}
	absoluteLogPath, err := filepath.Abs(selectedLogPath)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "resolve local Threadforge log path:", err)
		return 2
	}
	absoluteMetadataLogPath, err := filepath.Abs(strings.TrimSpace(*metadataLogPath))
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "resolve Wazuh metadata log path:", err)
		return 2
	}
	options := forwarding.WazuhForwardingOptions{LogPath: absoluteLogPath, MetadataLogPath: absoluteMetadataLogPath}
	config, err := forwarding.RenderWazuhVectorConfig(options)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, err)
		return 2
	}
	if operation == "print-config" {
		_, _ = fmt.Fprintln(stdout, config)
		return 0
	}
	installed, err := forwarding.InstallWazuhMetadataPack(*outputDir, options)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, err)
		return 1
	}
	_, _ = fmt.Fprintf(stdout, "Wazuh metadata-only pack written locally to %s; Vector and Wazuh were not installed or started and no network request was made.\n", installed)
	return 0
}

func runEndpointRapid7(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 || (args[0] != "print-config" && args[0] != "install-pack") {
		_, _ = fmt.Fprintln(stderr, "usage: threadforge endpoint rapid7 <print-config|install-pack> --allow-remote-metadata-forwarding --allow-vector-env-interpolation [--log-path <events.jsonl>] [--output <new-dir>]")
		return 2
	}
	operation := args[0]
	fs := flag.NewFlagSet("endpoint rapid7 "+operation, flag.ContinueOnError)
	fs.SetOutput(stderr)
	logPath := fs.String("log-path", "", "local Threadforge JSONL path (defaults to the configured trace path)")
	outputDir := fs.String("output", "threadforge-rapid7-pack", "new output directory for install-pack")
	allowMetadataForwarding := fs.Bool("allow-remote-metadata-forwarding", false, "acknowledge that activating Vector forwards timestamp and allowlisted severity metadata off-device")
	allowVectorEnvInterpolation := fs.Bool("allow-vector-env-interpolation", false, "acknowledge that Vector must interpolate the externally supplied Rapid7 bearer URL from its process environment")
	if err := fs.Parse(args[1:]); err != nil {
		return 2
	}
	if fs.NArg() != 0 {
		_, _ = fmt.Fprintln(stderr, "endpoint rapid7 does not accept positional arguments")
		return 2
	}
	if !*allowMetadataForwarding {
		_, _ = fmt.Fprintln(stderr, "endpoint rapid7 requires --allow-remote-metadata-forwarding; creating config is local-only, but activating it sends timestamp and allowlisted severity metadata off-device")
		return 2
	}
	if !*allowVectorEnvInterpolation {
		_, _ = fmt.Fprintln(stderr, "endpoint rapid7 requires --allow-vector-env-interpolation because Vector resolves the bearer webhook URL from its process environment")
		return 2
	}
	selectedLogPath := strings.TrimSpace(*logPath)
	if selectedLogPath == "" {
		paths, err := appdata.Resolve("", "")
		if err != nil {
			_, _ = fmt.Fprintln(stderr, err)
			return 1
		}
		selectedLogPath = paths.Traces
	}
	absoluteLogPath, err := filepath.Abs(selectedLogPath)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "resolve local Threadforge log path:", err)
		return 2
	}
	options := forwarding.Rapid7ForwardingOptions{LogPath: absoluteLogPath}
	config, err := forwarding.RenderRapid7VectorConfig(options)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, err)
		return 2
	}
	if operation == "print-config" {
		_, _ = fmt.Fprintln(stdout, config)
		return 0
	}
	installed, err := forwarding.InstallRapid7ForwardingPack(*outputDir, options)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, err)
		return 1
	}
	_, _ = fmt.Fprintf(stdout, "Rapid7 forwarding pack written locally to %s; Vector was not installed or started and no network request was made.\n", installed)
	return 0
}

func runEndpointSumo(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 || (args[0] != "print-config" && args[0] != "install-pack") {
		_, _ = fmt.Fprintln(stderr, "usage: threadforge endpoint sumo <print-config|install-pack> --endpoint <https://.../receiver/v1/http> --auth-header --allow-remote-metadata-forwarding [--secrets-dir <dir>] [--log-path <events.jsonl>] [--output <new-dir>]")
		return 2
	}
	operation := args[0]
	fs := flag.NewFlagSet("endpoint sumo "+operation, flag.ContinueOnError)
	fs.SetOutput(stderr)
	endpoint := fs.String("endpoint", "", "non-secret Sumo Auth Header URL ending in /receiver/v1/http")
	logPath := fs.String("log-path", "", "local Threadforge JSONL path (defaults to the configured trace path)")
	secretsDir := fs.String("secrets-dir", "", "Vector directory secrets backend path (defaults to the user config directory)")
	useAuthHeader := fs.Bool("auth-header", false, "confirm this is a Sumo Auth Header source; presigned bearer URLs are unsupported")
	outputDir := fs.String("output", "threadforge-sumo-pack", "new output directory for install-pack")
	allowMetadataForwarding := fs.Bool("allow-remote-metadata-forwarding", false, "acknowledge that activating Vector forwards timestamp and allowlisted severity metadata off-device")
	if err := fs.Parse(args[1:]); err != nil {
		return 2
	}
	if fs.NArg() != 0 {
		_, _ = fmt.Fprintln(stderr, "endpoint sumo does not accept positional arguments")
		return 2
	}
	if !*allowMetadataForwarding {
		_, _ = fmt.Fprintln(stderr, "endpoint sumo requires --allow-remote-metadata-forwarding; creating config is local-only, but activating it sends timestamp and allowlisted severity metadata off-device")
		return 2
	}
	if !*useAuthHeader {
		_, _ = fmt.Fprintln(stderr, "endpoint sumo requires --auth-header; presigned URLs are bearer credentials and are not accepted")
		return 2
	}
	selectedLogPath := strings.TrimSpace(*logPath)
	if selectedLogPath == "" {
		paths, err := appdata.Resolve("", "")
		if err != nil {
			_, _ = fmt.Fprintln(stderr, err)
			return 1
		}
		selectedLogPath = paths.Traces
	}
	absoluteLogPath, err := filepath.Abs(selectedLogPath)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "resolve local Threadforge log path:", err)
		return 2
	}
	options := forwarding.SumoForwardingOptions{LogPath: absoluteLogPath, Endpoint: *endpoint, SecretsDir: *secretsDir}
	config, err := forwarding.RenderSumoVectorConfig(options)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, err)
		return 2
	}
	if operation == "print-config" {
		_, _ = fmt.Fprintln(stdout, config)
		return 0
	}
	installed, err := forwarding.InstallSumoForwardingPack(*outputDir, options)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, err)
		return 1
	}
	_, _ = fmt.Fprintf(stdout, "Sumo Logic forwarding pack written locally to %s; Vector was not installed or started and no network request was made.\n", installed)
	return 0
}

func runEndpointS3(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 || (args[0] != "print-config" && args[0] != "install-pack") {
		_, _ = fmt.Fprintln(stderr, "usage: threadforge endpoint s3 <print-config|install-pack> --bucket <name> --allow-remote-metadata-forwarding [--region us-east-1] [--prefix threadforge] [--log-path <events.jsonl>] [--output <new-dir>]")
		return 2
	}
	operation := args[0]
	fs := flag.NewFlagSet("endpoint s3 "+operation, flag.ContinueOnError)
	fs.SetOutput(stderr)
	bucket := fs.String("bucket", "", "AWS S3 bucket name")
	region := fs.String("region", "us-east-1", "AWS region")
	prefix := fs.String("prefix", "threadforge", "S3 object key prefix")
	logPath := fs.String("log-path", "", "local Threadforge JSONL path (defaults to the configured trace path)")
	outputDir := fs.String("output", "threadforge-s3-pack", "new output directory for install-pack")
	allowMetadataForwarding := fs.Bool("allow-remote-metadata-forwarding", false, "acknowledge that activating Vector forwards timestamp and allowlisted severity metadata off-device")
	if err := fs.Parse(args[1:]); err != nil {
		return 2
	}
	if fs.NArg() != 0 {
		_, _ = fmt.Fprintln(stderr, "endpoint s3 does not accept positional arguments")
		return 2
	}
	if !*allowMetadataForwarding {
		_, _ = fmt.Fprintln(stderr, "endpoint s3 requires --allow-remote-metadata-forwarding; creating config is local-only, but activating it sends timestamp and allowlisted severity metadata off-device")
		return 2
	}
	if strings.TrimSpace(*bucket) == "" {
		_, _ = fmt.Fprintln(stderr, "endpoint s3 requires --bucket")
		return 2
	}
	selectedLogPath := strings.TrimSpace(*logPath)
	if selectedLogPath == "" {
		paths, err := appdata.Resolve("", "")
		if err != nil {
			_, _ = fmt.Fprintln(stderr, err)
			return 1
		}
		selectedLogPath = paths.Traces
	}
	absoluteLogPath, err := filepath.Abs(selectedLogPath)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "resolve local Threadforge log path:", err)
		return 2
	}
	options := forwarding.S3ForwardingOptions{
		LogPath: absoluteLogPath,
		Bucket:  *bucket,
		Region:  *region,
		Prefix:  *prefix,
	}
	config, err := forwarding.RenderS3VectorConfig(options)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, err)
		return 2
	}
	if operation == "print-config" {
		_, _ = fmt.Fprintln(stdout, config)
		return 0
	}
	installed, err := forwarding.InstallS3ForwardingPack(*outputDir, options)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, err)
		return 1
	}
	_, _ = fmt.Fprintf(stdout, "S3 forwarding pack written locally to %s; Vector was not installed or started and no network request was made.\n", installed)
	return 0
}

func runEndpointCloudWatch(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 || (args[0] != "print-config" && args[0] != "install-pack") {
		_, _ = fmt.Fprintln(stderr, "usage: threadforge endpoint cloudwatch <print-config|install-pack> --log-group <name> --allow-remote-metadata-forwarding [--region us-east-1] [--log-path <events.jsonl>] [--output <new-dir>]")
		return 2
	}
	operation := args[0]
	fs := flag.NewFlagSet("endpoint cloudwatch "+operation, flag.ContinueOnError)
	fs.SetOutput(stderr)
	logGroup := fs.String("log-group", "", "pre-existing AWS CloudWatch Logs group")
	region := fs.String("region", "us-east-1", "AWS region")
	logPath := fs.String("log-path", "", "local Threadforge JSONL path (defaults to the configured trace path)")
	outputDir := fs.String("output", "threadforge-cloudwatch-pack", "new output directory for install-pack")
	allowMetadataForwarding := fs.Bool("allow-remote-metadata-forwarding", false, "acknowledge that activating Vector forwards timestamp and allowlisted severity metadata off-device")
	if err := fs.Parse(args[1:]); err != nil {
		return 2
	}
	if fs.NArg() != 0 {
		_, _ = fmt.Fprintln(stderr, "endpoint cloudwatch does not accept positional arguments")
		return 2
	}
	if !*allowMetadataForwarding {
		_, _ = fmt.Fprintln(stderr, "endpoint cloudwatch requires --allow-remote-metadata-forwarding; creating config is local-only, but activating it sends timestamp and allowlisted severity metadata off-device")
		return 2
	}
	if strings.TrimSpace(*logGroup) == "" {
		_, _ = fmt.Fprintln(stderr, "endpoint cloudwatch requires --log-group")
		return 2
	}
	selectedLogPath := strings.TrimSpace(*logPath)
	if selectedLogPath == "" {
		paths, err := appdata.Resolve("", "")
		if err != nil {
			_, _ = fmt.Fprintln(stderr, err)
			return 1
		}
		selectedLogPath = paths.Traces
	}
	absoluteLogPath, err := filepath.Abs(selectedLogPath)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "resolve local Threadforge log path:", err)
		return 2
	}
	options := forwarding.CloudWatchForwardingOptions{
		LogPath:  absoluteLogPath,
		LogGroup: *logGroup,
		Region:   *region,
	}
	config, err := forwarding.RenderCloudWatchVectorConfig(options)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, err)
		return 2
	}
	if operation == "print-config" {
		_, _ = fmt.Fprintln(stdout, config)
		return 0
	}
	installed, err := forwarding.InstallCloudWatchForwardingPack(*outputDir, options)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, err)
		return 1
	}
	_, _ = fmt.Fprintf(stdout, "CloudWatch forwarding pack written locally to %s; Vector was not installed or started and no network request was made.\n", installed)
	return 0
}

func runEndpointGCS(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 || (args[0] != "print-config" && args[0] != "install-pack") {
		_, _ = fmt.Fprintln(stderr, "usage: threadforge endpoint gcs <print-config|install-pack> --bucket <name> --allow-remote-metadata-forwarding [--prefix threadforge] [--log-path <events.jsonl>] [--output <new-dir>]")
		return 2
	}
	operation := args[0]
	fs := flag.NewFlagSet("endpoint gcs "+operation, flag.ContinueOnError)
	fs.SetOutput(stderr)
	bucket := fs.String("bucket", "", "Google Cloud Storage bucket name")
	prefix := fs.String("prefix", "threadforge", "GCS object key prefix")
	logPath := fs.String("log-path", "", "local Threadforge JSONL path (defaults to the configured trace path)")
	outputDir := fs.String("output", "threadforge-gcs-pack", "new output directory for install-pack")
	allowMetadataForwarding := fs.Bool("allow-remote-metadata-forwarding", false, "acknowledge that activating Vector forwards timestamp and allowlisted severity metadata off-device")
	if err := fs.Parse(args[1:]); err != nil {
		return 2
	}
	if fs.NArg() != 0 {
		_, _ = fmt.Fprintln(stderr, "endpoint gcs does not accept positional arguments")
		return 2
	}
	if !*allowMetadataForwarding {
		_, _ = fmt.Fprintln(stderr, "endpoint gcs requires --allow-remote-metadata-forwarding; creating config is local-only, but activating it sends timestamp and allowlisted severity metadata off-device")
		return 2
	}
	if strings.TrimSpace(*bucket) == "" {
		_, _ = fmt.Fprintln(stderr, "endpoint gcs requires --bucket")
		return 2
	}
	selectedLogPath := strings.TrimSpace(*logPath)
	if selectedLogPath == "" {
		paths, err := appdata.Resolve("", "")
		if err != nil {
			_, _ = fmt.Fprintln(stderr, err)
			return 1
		}
		selectedLogPath = paths.Traces
	}
	absoluteLogPath, err := filepath.Abs(selectedLogPath)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "resolve local Threadforge log path:", err)
		return 2
	}
	options := forwarding.GCSForwardingOptions{
		LogPath: absoluteLogPath,
		Bucket:  *bucket,
		Prefix:  *prefix,
	}
	config, err := forwarding.RenderGCSVectorConfig(options)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, err)
		return 2
	}
	if operation == "print-config" {
		_, _ = fmt.Fprintln(stdout, config)
		return 0
	}
	installed, err := forwarding.InstallGCSForwardingPack(*outputDir, options)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, err)
		return 1
	}
	_, _ = fmt.Fprintf(stdout, "GCS forwarding pack written locally to %s; Vector was not installed or started and no network request was made.\n", installed)
	return 0
}

func runEndpointSplunkHEC(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 || (args[0] != "print-config" && args[0] != "install-pack") {
		_, _ = fmt.Fprintln(stderr, "usage: threadforge endpoint splunk-hec <print-config|install-pack> --endpoint <https-origin> --allow-remote-metadata-forwarding [--index threadforge] [--secrets-dir <dir>] [--log-path <events.jsonl>] [--output <new-dir>]")
		return 2
	}
	operation := args[0]
	fs := flag.NewFlagSet("endpoint splunk-hec "+operation, flag.ContinueOnError)
	fs.SetOutput(stderr)
	endpoint := fs.String("endpoint", "", "HTTPS origin for Splunk HEC (no path or query)")
	index := fs.String("index", "threadforge", "pre-existing Splunk index")
	logPath := fs.String("log-path", "", "local Threadforge JSONL path (defaults to the configured trace path)")
	secretsDir := fs.String("secrets-dir", "", "Vector directory secrets backend path (defaults to the user config directory)")
	outputDir := fs.String("output", "threadforge-splunk-hec-pack", "new output directory for install-pack")
	allowMetadataForwarding := fs.Bool("allow-remote-metadata-forwarding", false, "acknowledge that activating Vector forwards timestamp and allowlisted severity metadata off-device")
	if err := fs.Parse(args[1:]); err != nil {
		return 2
	}
	if fs.NArg() != 0 {
		_, _ = fmt.Fprintln(stderr, "endpoint splunk-hec does not accept positional arguments")
		return 2
	}
	if !*allowMetadataForwarding {
		_, _ = fmt.Fprintln(stderr, "endpoint splunk-hec requires --allow-remote-metadata-forwarding; creating config is local-only, but activating it sends timestamp and allowlisted severity metadata off-device")
		return 2
	}
	if strings.TrimSpace(*endpoint) == "" {
		_, _ = fmt.Fprintln(stderr, "endpoint splunk-hec requires --endpoint")
		return 2
	}
	selectedLogPath := strings.TrimSpace(*logPath)
	if selectedLogPath == "" {
		paths, err := appdata.Resolve("", "")
		if err != nil {
			_, _ = fmt.Fprintln(stderr, err)
			return 1
		}
		selectedLogPath = paths.Traces
	}
	absoluteLogPath, err := filepath.Abs(selectedLogPath)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "resolve local Threadforge log path:", err)
		return 2
	}
	options := forwarding.SplunkForwardingOptions{
		LogPath:    absoluteLogPath,
		Endpoint:   *endpoint,
		Index:      *index,
		SecretsDir: *secretsDir,
	}
	config, err := forwarding.RenderSplunkVectorConfig(options)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, err)
		return 2
	}
	if operation == "print-config" {
		_, _ = fmt.Fprintln(stdout, config)
		return 0
	}
	installed, err := forwarding.InstallSplunkForwardingPack(*outputDir, options)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, err)
		return 1
	}
	_, _ = fmt.Fprintf(stdout, "Splunk HEC forwarding pack written locally to %s; Vector was not installed or started and no network request was made.\n", installed)
	return 0
}

func runEndpointFalconHEC(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 || (args[0] != "print-config" && args[0] != "install-pack") {
		_, _ = fmt.Fprintln(stderr, "usage: threadforge endpoint falcon-hec <print-config|install-pack> --endpoint <https-hec-url> --allow-remote-metadata-forwarding [--repository <name>] [--secrets-dir <dir>] [--log-path <events.jsonl>] [--output <new-dir>]")
		return 2
	}
	operation := args[0]
	fs := flag.NewFlagSet("endpoint falcon-hec "+operation, flag.ContinueOnError)
	fs.SetOutput(stderr)
	endpoint := fs.String("endpoint", "", "HTTPS CrowdStrike HEC connector URL")
	repository := fs.String("repository", "", "optional Falcon LogScale repository/index")
	logPath := fs.String("log-path", "", "local Threadforge JSONL path (defaults to the configured trace path)")
	secretsDir := fs.String("secrets-dir", "", "Vector directory secrets backend path (defaults to the user config directory)")
	outputDir := fs.String("output", "threadforge-falcon-hec-pack", "new output directory for install-pack")
	allowMetadataForwarding := fs.Bool("allow-remote-metadata-forwarding", false, "acknowledge that activating Vector forwards timestamp and allowlisted severity metadata off-device")
	if err := fs.Parse(args[1:]); err != nil {
		return 2
	}
	if fs.NArg() != 0 {
		_, _ = fmt.Fprintln(stderr, "endpoint falcon-hec does not accept positional arguments")
		return 2
	}
	if !*allowMetadataForwarding {
		_, _ = fmt.Fprintln(stderr, "endpoint falcon-hec requires --allow-remote-metadata-forwarding; creating config is local-only, but activating it sends timestamp and allowlisted severity metadata off-device")
		return 2
	}
	if strings.TrimSpace(*endpoint) == "" {
		_, _ = fmt.Fprintln(stderr, "endpoint falcon-hec requires --endpoint")
		return 2
	}
	selectedLogPath := strings.TrimSpace(*logPath)
	if selectedLogPath == "" {
		paths, err := appdata.Resolve("", "")
		if err != nil {
			_, _ = fmt.Fprintln(stderr, err)
			return 1
		}
		selectedLogPath = paths.Traces
	}
	absoluteLogPath, err := filepath.Abs(selectedLogPath)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "resolve local Threadforge log path:", err)
		return 2
	}
	options := forwarding.FalconHECOptions{
		LogPath:    absoluteLogPath,
		Endpoint:   *endpoint,
		Repository: *repository,
		SecretsDir: *secretsDir,
	}
	config, err := forwarding.RenderFalconHECVectorConfig(options)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, err)
		return 2
	}
	if operation == "print-config" {
		_, _ = fmt.Fprintln(stdout, config)
		return 0
	}
	installed, err := forwarding.InstallFalconHECForwardingPack(*outputDir, options)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, err)
		return 1
	}
	_, _ = fmt.Fprintf(stdout, "Falcon HEC forwarding pack written locally to %s; Vector was not installed or started and no network request was made.\n", installed)
	return 0
}

func runEndpointElasticsearch(args []string, stdout, stderr io.Writer) int {
	if len(args) > 0 && (args[0] == "up" || args[0] == "down" || args[0] == "status") {
		return runEndpointElasticsearchStack(args, stdout, stderr)
	}
	if len(args) == 0 || (args[0] != "print-config" && args[0] != "install-pack") {
		_, _ = fmt.Fprintln(stderr, "usage: threadforge endpoint elasticsearch <print-config|install-pack|up|down|status> [--path vector|filebeat|elastic-agent] --endpoint <https-origin> --allow-remote-metadata-forwarding [local stack flags]")
		return 2
	}
	operation := args[0]
	fs := flag.NewFlagSet("endpoint elasticsearch "+operation, flag.ContinueOnError)
	fs.SetOutput(stderr)
	path := fs.String("path", "vector", "forwarding path: vector, filebeat, or elastic-agent")
	endpoint := fs.String("endpoint", "", "HTTPS Elasticsearch origin (no path or query)")
	index := fs.String("index", "threadforge-metadata", "Vector Elasticsearch index (Vector path only)")
	logPath := fs.String("log-path", "", "local Threadforge JSONL path (defaults to the configured trace path)")
	metadataLogPath := fs.String("metadata-log-path", "", "separate absolute sanitized JSONL path required for --path filebeat or elastic-agent")
	secretsDir := fs.String("secrets-dir", "", "Vector directory secrets backend path (defaults to the user config directory)")
	outputDir := fs.String("output", "threadforge-elasticsearch-pack", "new output directory for install-pack")
	allowMetadataForwarding := fs.Bool("allow-remote-metadata-forwarding", false, "acknowledge that activating Vector forwards timestamp and allowlisted severity metadata off-device")
	if err := fs.Parse(args[1:]); err != nil {
		return 2
	}
	if fs.NArg() != 0 {
		_, _ = fmt.Fprintln(stderr, "endpoint elasticsearch does not accept positional arguments")
		return 2
	}
	indexWasProvided := false
	fs.Visit(func(parsed *flag.Flag) {
		if parsed.Name == "index" {
			indexWasProvided = true
		}
	})
	if !*allowMetadataForwarding {
		_, _ = fmt.Fprintln(stderr, "endpoint elasticsearch requires --allow-remote-metadata-forwarding; creating config is local-only, but activating it may forward sanitized Threadforge metadata off-device")
		return 2
	}
	if *path != "vector" && *path != "filebeat" && *path != "elastic-agent" {
		_, _ = fmt.Fprintln(stderr, "endpoint elasticsearch --path must be vector, filebeat, or elastic-agent")
		return 2
	}
	if strings.TrimSpace(*endpoint) == "" {
		_, _ = fmt.Fprintln(stderr, "endpoint elasticsearch requires --endpoint")
		return 2
	}
	if *path == "vector" {
		if strings.TrimSpace(*metadataLogPath) != "" {
			_, _ = fmt.Fprintln(stderr, "endpoint elasticsearch --metadata-log-path is used only with --path filebeat or elastic-agent")
			return 2
		}
	} else {
		if indexWasProvided {
			_, _ = fmt.Fprintln(stderr, "endpoint elasticsearch --index applies only to --path vector; Filebeat and Elastic Agent use the Threadforge metadata data stream")
			return 2
		}
		if strings.TrimSpace(*metadataLogPath) == "" {
			_, _ = fmt.Fprintln(stderr, "endpoint elasticsearch --path filebeat or elastic-agent requires --metadata-log-path")
			return 2
		}
		if strings.TrimSpace(*secretsDir) != "" {
			_, _ = fmt.Fprintln(stderr, "endpoint elasticsearch --path filebeat or elastic-agent does not accept --secrets-dir; configure credentials through the shipper's external secure mechanism")
			return 2
		}
	}
	selectedLogPath := strings.TrimSpace(*logPath)
	if selectedLogPath == "" {
		paths, err := appdata.Resolve("", "")
		if err != nil {
			_, _ = fmt.Fprintln(stderr, err)
			return 1
		}
		selectedLogPath = paths.Traces
	}
	absoluteLogPath, err := filepath.Abs(selectedLogPath)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "resolve local Threadforge log path:", err)
		return 2
	}
	if *path == "vector" {
		options := forwarding.ElasticsearchForwardingOptions{
			LogPath: absoluteLogPath, Endpoint: *endpoint, Index: *index, SecretsDir: *secretsDir,
		}
		config, err := forwarding.RenderElasticsearchVectorConfig(options)
		if err != nil {
			_, _ = fmt.Fprintln(stderr, err)
			return 2
		}
		if operation == "print-config" {
			_, _ = fmt.Fprintln(stdout, config)
			return 0
		}
		installed, err := forwarding.InstallElasticsearchForwardingPack(*outputDir, options)
		if err != nil {
			_, _ = fmt.Fprintln(stderr, err)
			return 1
		}
		_, _ = fmt.Fprintf(stdout, "Elasticsearch Vector forwarding pack written locally to %s; Vector was not installed or started and no network request was made.\n", installed)
		return 0
	}
	agentOptions := forwarding.ElasticsearchAgentForwardingOptions{
		LogPath: absoluteLogPath, MetadataLogPath: strings.TrimSpace(*metadataLogPath), Endpoint: *endpoint,
	}
	if operation == "print-config" {
		var config string
		if *path == "filebeat" {
			config, err = forwarding.RenderElasticsearchFilebeatConfig(agentOptions)
		} else {
			config, err = forwarding.RenderElasticsearchStandaloneAgentConfig(agentOptions)
		}
		if err != nil {
			_, _ = fmt.Fprintln(stderr, err)
			return 2
		}
		_, _ = fmt.Fprintln(stdout, config)
		return 0
	}
	installed, err := forwarding.InstallElasticsearchAgentForwardingPack(*outputDir, *path, agentOptions)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, err)
		return 1
	}
	_, _ = fmt.Fprintf(stdout, "Elasticsearch %s metadata-only pack written locally to %s; Vector and the shipper were not installed or started and no network request was made.\n", *path, installed)
	return 0
}

func runEndpointDatadog(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 || (args[0] != "print-config" && args[0] != "install-pack") {
		_, _ = fmt.Fprintln(stderr, "usage: threadforge endpoint datadog <print-config|install-pack> [--path vector|agent] --allow-remote-metadata-forwarding [--endpoint <https-origin>] [--secrets-dir <dir>] [--log-path <events.jsonl>] [--metadata-log-path <absolute-path>] [--output <new-dir>]")
		return 2
	}
	operation := args[0]
	fs := flag.NewFlagSet("endpoint datadog "+operation, flag.ContinueOnError)
	fs.SetOutput(stderr)
	path := fs.String("path", "vector", "forwarding path: vector or agent (Agent mode includes a local Vector sanitizer)")
	endpoint := fs.String("endpoint", "", "HTTPS Datadog Logs intake origin")
	logPath := fs.String("log-path", "", "local Threadforge JSONL path (defaults to the configured trace path)")
	metadataLogPath := fs.String("metadata-log-path", "", "separate absolute sanitized JSONL path required for --path agent")
	secretsDir := fs.String("secrets-dir", "", "Vector directory secrets backend path (defaults to the user config directory)")
	outputDir := fs.String("output", "", "new output directory for install-pack")
	allowMetadataForwarding := fs.Bool("allow-remote-metadata-forwarding", false, "acknowledge that activating the selected path forwards sanitized Threadforge metadata off-device")
	if err := fs.Parse(args[1:]); err != nil {
		return 2
	}
	if fs.NArg() != 0 {
		_, _ = fmt.Fprintln(stderr, "endpoint datadog does not accept positional arguments")
		return 2
	}
	if !*allowMetadataForwarding {
		_, _ = fmt.Fprintln(stderr, "endpoint datadog requires --allow-remote-metadata-forwarding; creating config is local-only, but activating it may forward sanitized Threadforge metadata and Agent host metadata off-device")
		return 2
	}
	if *path == "agent" {
		if strings.TrimSpace(*endpoint) != "" || strings.TrimSpace(*secretsDir) != "" {
			_, _ = fmt.Fprintln(stderr, "endpoint datadog --path agent does not accept --endpoint or --secrets-dir; the Datadog Agent uses its existing global site and credential configuration")
			return 2
		}
		selectedLogPath := strings.TrimSpace(*logPath)
		if selectedLogPath == "" {
			paths, err := appdata.Resolve("", "")
			if err != nil {
				_, _ = fmt.Fprintln(stderr, err)
				return 1
			}
			selectedLogPath = paths.Traces
		}
		absoluteLogPath, err := filepath.Abs(selectedLogPath)
		if err != nil {
			_, _ = fmt.Fprintln(stderr, "resolve local Threadforge log path:", err)
			return 2
		}
		options := forwarding.DatadogAgentForwardingOptions{LogPath: absoluteLogPath, MetadataLogPath: *metadataLogPath}
		if operation == "print-config" {
			config, err := forwarding.RenderDatadogAgentConfig(options)
			if err != nil {
				_, _ = fmt.Fprintln(stderr, err)
				return 2
			}
			_, _ = fmt.Fprintln(stdout, config)
			return 0
		}
		output := strings.TrimSpace(*outputDir)
		if output == "" {
			output = "threadforge-datadog-agent-pack"
		}
		installed, err := forwarding.InstallDatadogAgentForwardingPack(output, options)
		if err != nil {
			_, _ = fmt.Fprintln(stderr, err)
			return 1
		}
		_, _ = fmt.Fprintf(stdout, "Datadog Agent metadata-only pack written locally to %s; Vector and the Agent were not installed or started and no network request was made.\n", installed)
		return 0
	}
	if *path != "vector" {
		_, _ = fmt.Fprintln(stderr, "endpoint datadog --path must be vector or agent")
		return 2
	}
	if strings.TrimSpace(*metadataLogPath) != "" {
		_, _ = fmt.Fprintln(stderr, "endpoint datadog --metadata-log-path is used only with --path agent")
		return 2
	}
	if strings.TrimSpace(*endpoint) == "" {
		_, _ = fmt.Fprintln(stderr, "endpoint datadog requires --endpoint")
		return 2
	}
	selectedLogPath := strings.TrimSpace(*logPath)
	if selectedLogPath == "" {
		paths, err := appdata.Resolve("", "")
		if err != nil {
			_, _ = fmt.Fprintln(stderr, err)
			return 1
		}
		selectedLogPath = paths.Traces
	}
	absoluteLogPath, err := filepath.Abs(selectedLogPath)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "resolve local Threadforge log path:", err)
		return 2
	}
	options := forwarding.DatadogForwardingOptions{LogPath: absoluteLogPath, Endpoint: *endpoint, SecretsDir: *secretsDir}
	config, err := forwarding.RenderDatadogVectorConfig(options)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, err)
		return 2
	}
	if operation == "print-config" {
		_, _ = fmt.Fprintln(stdout, config)
		return 0
	}
	output := strings.TrimSpace(*outputDir)
	if output == "" {
		output = "threadforge-datadog-pack"
	}
	installed, err := forwarding.InstallDatadogForwardingPack(output, options)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, err)
		return 1
	}
	_, _ = fmt.Fprintf(stdout, "Datadog forwarding pack written locally to %s; Vector was not installed or started and no network request was made.\n", installed)
	return 0
}
