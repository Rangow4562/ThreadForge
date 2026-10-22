package forwarding

import (
	"bytes"
	"encoding/xml"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

const wazuhRulesXML = `<?xml version="1.0" encoding="UTF-8"?>
<group name="threadforge,metadata_export,">
  <rule id="119500" level="0" noalert="1">
    <decoded_as>json</decoded_as>
    <field name="threadforge_export.schema_version">threadforge.metadata-export.v1</field>
    <field name="threadforge_export.raw_content">false</field>
    <description>Threadforge metadata-only telemetry export</description>
  </rule>
  <rule id="119510" level="3">
    <if_sid>119500</if_sid>
    <field name="severity">info</field>
    <description>Threadforge exported metadata severity: info</description>
  </rule>
  <rule id="119520" level="5">
    <if_sid>119500</if_sid>
    <field name="severity">low</field>
    <description>Threadforge exported metadata severity: low</description>
  </rule>
  <rule id="119530" level="7">
    <if_sid>119500</if_sid>
    <field name="severity">medium</field>
    <description>Threadforge exported metadata severity: medium</description>
  </rule>
  <rule id="119540" level="9">
    <if_sid>119500</if_sid>
    <field name="severity">high</field>
    <description>Threadforge exported metadata severity: high</description>
  </rule>
  <rule id="119550" level="11">
    <if_sid>119500</if_sid>
    <field name="severity">critical</field>
    <description>Threadforge exported metadata severity: critical</description>
  </rule>
  <rule id="119560" level="0" noalert="1">
    <if_sid>119500</if_sid>
    <field name="severity">unknown</field>
    <description>Threadforge exported metadata severity: unknown</description>
  </rule>
</group>
`

const wazuhDashboardColumnsScript = `#!/bin/sh
set -eu

: "${WAZUH_DASHBOARD_URL:?set WAZUH_DASHBOARD_URL to the HTTPS Wazuh Dashboard origin}"
: "${WAZUH_DASHBOARD_NETRC:?set WAZUH_DASHBOARD_NETRC to a protected netrc file for this dashboard}"

case "$WAZUH_DASHBOARD_URL" in
  https://?*) ;;
  *) echo "WAZUH_DASHBOARD_URL must use HTTPS" >&2; exit 2 ;;
esac

dashboard_origin=${WAZUH_DASHBOARD_URL%/}
dashboard_authority=${dashboard_origin#https://}
case "$dashboard_authority" in
  ''|*/*|*@*|*\?*|*#*) echo "WAZUH_DASHBOARD_URL must be an HTTPS origin without credentials, path, query, or fragment" >&2; exit 2 ;;
esac

[ -f "$WAZUH_DASHBOARD_NETRC" ] && [ ! -L "$WAZUH_DASHBOARD_NETRC" ] && [ -r "$WAZUH_DASHBOARD_NETRC" ] || { echo "WAZUH_DASHBOARD_NETRC must be a readable regular non-symlink file" >&2; exit 2; }
netrc_mode=$(stat -c '%a' "$WAZUH_DASHBOARD_NETRC" 2>/dev/null || stat -f '%Lp' "$WAZUH_DASHBOARD_NETRC" 2>/dev/null) || { echo "could not inspect WAZUH_DASHBOARD_NETRC permissions" >&2; exit 2; }
[ "$netrc_mode" = "600" ] || { echo "WAZUH_DASHBOARD_NETRC must have mode 0600" >&2; exit 2; }

curl --fail --silent --show-error --proto '=https' --max-time 15 --netrc-file "$WAZUH_DASHBOARD_NETRC" --request POST --header 'osd-xsrf: threadforge' --header 'Content-Type: application/json' --data-binary '{"value":["timestamp","severity","threadforge_export.schema_version","threadforge_export.raw_content"]}' --output /dev/null "$dashboard_origin/api/opensearch-dashboards/settings/defaultColumns"

printf '\nThreadforge metadata-only dashboard columns applied to %s\n' "$dashboard_origin"
`

// WazuhForwardingOptions configures a local metadata-only handoff to Wazuh.
// Vector tails the runtime log, writes a newly constructed metadata JSONL file,
// and Wazuh is configured to read only that output file.
type WazuhForwardingOptions struct {
	LogPath         string
	MetadataLogPath string
}

// RenderWazuhVectorConfig creates an inert local-only Vector transform/file sink.
func RenderWazuhVectorConfig(options WazuhForwardingOptions) (string, error) {
	normalized, err := normalizeWazuhOptions(options)
	if err != nil {
		return "", err
	}
	return renderLocalMetadataVectorConfig(normalized.LogPath, normalized.MetadataLogPath, "threadforge_wazuh_metadata", metadataProjectionVRL), nil
}

// RenderWazuhLocalfileConfig points Wazuh at the sanitized output, never at the
// source runtime log.
func RenderWazuhLocalfileConfig(metadataLogPath string) (string, error) {
	metadataLogPath, err := normalizeWazuhMetadataLogPath(metadataLogPath)
	if err != nil {
		return "", err
	}
	var escaped bytes.Buffer
	if err := xml.EscapeText(&escaped, []byte(metadataLogPath)); err != nil {
		return "", fmt.Errorf("escape Wazuh localfile path: %w", err)
	}
	return fmt.Sprintf(`<localfile>
  <location>%s</location>
  <log_format>json</log_format>
</localfile>
`, escaped.String()), nil
}

// RenderSyntheticWazuhSample returns one synthetic, content-free metadata event.
func RenderSyntheticWazuhSample() ([]byte, error) {
	data, err := renderSyntheticMetadataExport()
	if err != nil {
		return nil, fmt.Errorf("encode synthetic Wazuh metadata sample: %w", err)
	}
	return data, nil
}

// InstallWazuhMetadataPack writes an inert local pack without modifying Wazuh,
// starting Vector, or contacting any service.
func InstallWazuhMetadataPack(outputDir string, options WazuhForwardingOptions) (string, error) {
	normalized, err := normalizeWazuhOptions(options)
	if err != nil {
		return "", err
	}
	vectorConfig, err := RenderWazuhVectorConfig(normalized)
	if err != nil {
		return "", err
	}
	localfileConfig, err := RenderWazuhLocalfileConfig(normalized.MetadataLogPath)
	if err != nil {
		return "", err
	}
	sample, err := RenderSyntheticWazuhSample()
	if err != nil {
		return "", err
	}
	readme := renderWazuhReadme(normalized)
	return writePrivatePack(outputDir, []privatePackFile{
		{name: "README.md", content: []byte(readme)},
		{name: "sample-event.jsonl", content: sample},
		{name: "threadforge-rules.xml", content: []byte(wazuhRulesXML)},
		{name: "ossec-localfile.xml", content: []byte(localfileConfig)},
		{name: "vector.toml", content: []byte(vectorConfig)},
		{name: "apply-dashboard-default-columns.sh", content: []byte(wazuhDashboardColumnsScript)},
	})
}

func normalizeWazuhOptions(options WazuhForwardingOptions) (WazuhForwardingOptions, error) {
	logPath, err := normalizeRuntimeLogPath(options.LogPath)
	if err != nil {
		return WazuhForwardingOptions{}, err
	}
	metadataPath, err := normalizeWazuhMetadataLogPath(options.MetadataLogPath)
	if err != nil {
		return WazuhForwardingOptions{}, err
	}
	if sameFilesystemPath(logPath, metadataPath) {
		return WazuhForwardingOptions{}, errors.New("Wazuh metadata output path must differ from the raw Threadforge runtime log path")
	}
	if err := validateRealDirectoryChain(filepath.Dir(metadataPath)); err != nil {
		return WazuhForwardingOptions{}, fmt.Errorf("validate Wazuh metadata output directory: %w", err)
	}
	options.LogPath = logPath
	options.MetadataLogPath = metadataPath
	return options, nil
}

func normalizeWazuhMetadataLogPath(path string) (string, error) {
	path = strings.TrimSpace(path)
	if path == "" || len(path) > 4096 || strings.ContainsAny(path, "\x00\r\n") {
		return "", errors.New("Wazuh metadata log path must be nonempty, bounded, and contain no control separators")
	}
	if !filepath.IsAbs(path) {
		return "", errors.New("Wazuh metadata log path must be absolute")
	}
	return filepath.Clean(path), nil
}

func sameFilesystemPath(left, right string) bool {
	left, right = filepath.Clean(left), filepath.Clean(right)
	if runtime.GOOS == "windows" {
		if strings.EqualFold(left, right) {
			return true
		}
	} else if left == right {
		return true
	}
	leftInfo, leftErr := os.Stat(left)
	rightInfo, rightErr := os.Stat(right)
	return leftErr == nil && rightErr == nil && os.SameFile(leftInfo, rightInfo)
}

func renderWazuhReadme(options WazuhForwardingOptions) string {
	return fmt.Sprintf(`# Threadforge metadata-only Wazuh pack

This pack provides a local Vector transform/file sink, a Wazuh localfile
snippet, custom rules, a synthetic sample, and an optional dashboard-column
helper. Pack generation does not install or start Vector or Wazuh, edit host
configuration, read credentials, or make network requests.

Vector tails new Threadforge runtime JSONL from end-of-file, constructs a new
record containing only timestamp, allowlisted severity, and the fixed
Threadforge schema/privacy marker, then writes it to:

  %s

Wazuh's ossec-localfile.xml points only at that sanitized output. Do not change
it to point at the source runtime log: that log can contain content excluded by
this pack. The metadata file is local, but Wazuh may forward events to a remote
manager; review that downstream path before activation. Set filesystem access
so Vector can write the metadata file and the Wazuh agent can read it, without
granting broader access to the raw Threadforge trace.

Install threadforge-rules.xml in the Wazuh manager's custom rules directory
and add ossec-localfile.xml to the Wazuh agent configuration on the same host
that runs Vector. Review rule IDs 119500-119560 against your existing local
rules before activation. The levels reflect only the exported severity label;
they are not independent threat detections or authorization decisions. Restart
or reload Wazuh after applying configuration. Keep Vector's data directory
persistent so source checkpoints survive restarts.

The sample-event.jsonl is synthetic. The Vector input starts at EOF and does
not backfill the existing raw runtime log. The destination file path is
configured at pack creation time; keep vector.toml and this pack private
because they contain local filesystem paths.

apply-dashboard-default-columns.sh is inert until you explicitly run it.
It requires WAZUH_DASHBOARD_URL to be an HTTPS origin and
WAZUH_DASHBOARD_NETRC to point to a user-managed netrc file with POSIX mode
0600. It makes one authenticated HTTPS request without following redirects or
printing the response body. The four selected columns are limited to timestamp,
severity, the fixed export schema version, and the constant raw-content=false
marker. No dashboard credentials or prompt, command, path, session, or other
raw trace fields are included in the pack or displayed by the helper.

Runtime log path: %s
`, options.MetadataLogPath, options.LogPath)
}
