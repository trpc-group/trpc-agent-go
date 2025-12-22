module trpc.group/trpc-go/trpc-agent-go/tool/arxivsearch

go 1.24.1

replace (
	trpc.group/trpc-go/trpc-agent-go => ../..
	trpc.group/trpc-go/trpc-agent-go/knowledge/document/reader/pdf => ../../knowledge/document/reader/pdf
)

require (
	github.com/go-pdf/fpdf v0.9.0
	github.com/stretchr/testify v1.11.1
	trpc.group/trpc-go/trpc-agent-go v0.2.0
	trpc.group/trpc-go/trpc-agent-go/knowledge/document/reader/pdf v0.0.0-20251126064502-c8c2594d2519
)

require (
	github.com/clipperhouse/uax29/v2 v2.2.0 // indirect
	github.com/davecgh/go-spew v1.1.1 // indirect
	github.com/hhrutter/lzw v1.0.0 // indirect
	github.com/hhrutter/pkcs7 v0.2.0 // indirect
	github.com/hhrutter/tiff v1.0.2 // indirect
	github.com/ledongthuc/pdf v0.0.0-20250511090121-5959a4027728 // indirect
	github.com/mattn/go-runewidth v0.0.19 // indirect
	github.com/pdfcpu/pdfcpu v0.11.1 // indirect
	github.com/pkg/errors v0.9.1 // indirect
	github.com/pmezard/go-difflib v1.0.0 // indirect
	github.com/yuin/goldmark v1.4.13 // indirect
	go.uber.org/multierr v1.10.0 // indirect
	go.uber.org/zap v1.27.0 // indirect
	golang.org/x/crypto v0.43.0 // indirect
	golang.org/x/image v0.32.0 // indirect
	golang.org/x/text v0.30.0 // indirect
	gopkg.in/yaml.v2 v2.4.0 // indirect
	gopkg.in/yaml.v3 v3.0.1 // indirect
	trpc.group/trpc-go/trpc-a2a-go v0.2.5 // indirect
)
