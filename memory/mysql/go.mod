module trpc.group/trpc-go/trpc-agent-go/memory/mysql

go 1.21.0

replace (
	trpc.group/trpc-go/trpc-agent-go => ../..
	trpc.group/trpc-go/trpc-agent-go/storage/mysql => ../../storage/mysql
)

require (
	github.com/DATA-DOG/go-sqlmock v1.5.2
	github.com/go-sql-driver/mysql v1.9.3
	github.com/stretchr/testify v1.11.1
	trpc.group/trpc-go/trpc-agent-go v0.0.0-20251126064502-c8c2594d2519
	trpc.group/trpc-go/trpc-agent-go/storage/mysql v0.0.0-20251126064502-c8c2594d2519
)

require (
	filippo.io/edwards25519 v1.1.0 // indirect
	github.com/davecgh/go-spew v1.1.1 // indirect
	github.com/google/uuid v1.6.0 // indirect
	github.com/pmezard/go-difflib v1.0.0 // indirect
	github.com/spaolacci/murmur3 v1.1.0 // indirect
	go.uber.org/multierr v1.10.0 // indirect
	go.uber.org/zap v1.27.0 // indirect
	gopkg.in/yaml.v3 v3.0.1 // indirect
	trpc.group/trpc-go/trpc-a2a-go v0.2.5 // indirect
)
