module github.com/threadforge-ai/threadforge

go 1.27.4

require (
	github.com/asymptote-labs/agent-beacon/cli/beacon v0.0.0
	github.com/asymptote-labs/agent-beacon/pkg/asymptoteobserve v0.0.0
	github.com/pelletier/go-toml/v2 v2.4.3
	go.opentelemetry.io/proto/otlp v1.11.1
	golang.org/x/net v0.62.0
	google.golang.org/genproto/googleapis/rpc v0.0.0-20260928230214-8a89bd6388cc
	google.golang.org/grpc v1.85.0
	google.golang.org/protobuf v1.36.12
	gopkg.in/yaml.v3 v3.0.1
	modernc.org/sqlite v1.62.0
)

replace (
	github.com/asymptote-labs/agent-beacon/cli/beacon => ./third_party/beacon-sessionreaders
	github.com/asymptote-labs/agent-beacon/pkg/asymptoteobserve => ./third_party/agent-beacon/pkg/asymptoteobserve
)

require (
	cel.dev/expr v0.25.3 // indirect
	github.com/antlr4-go/antlr/v4 v4.13.1 // indirect
	github.com/dustin/go-humanize v1.1.0 // indirect
	github.com/google/cel-go v0.30.0 // indirect
	github.com/google/uuid v1.6.0 // indirect
	github.com/grpc-ecosystem/grpc-gateway/v2 v2.31.0 // indirect
	github.com/klauspost/compress v1.20.3 // indirect
	github.com/mattn/go-isatty v0.0.24 // indirect
	github.com/ncruces/go-strftime v1.1.0 // indirect
	github.com/remyoudompheng/bigfft v0.0.0-20230129092748-24d4a6f8daec // indirect
	go.yaml.in/yaml/v3 v3.0.5 // indirect
	golang.org/x/exp v0.0.0-20260908205506-85c1c2202aba // indirect
	golang.org/x/sys v0.49.0 // indirect
	golang.org/x/text v0.43.0 // indirect
	google.golang.org/genproto/googleapis/api v0.0.0-20260928230214-8a89bd6388cc // indirect
	modernc.org/libc v1.80.1 // indirect
	modernc.org/mathutil v1.7.1 // indirect
	modernc.org/memory v1.12.1 // indirect
)
