module trpc.group/trpc-go/trpc-agent-go/knowledge/graphstore/age

go 1.21

replace (
	trpc.group/trpc-go/trpc-agent-go => ../../../
	trpc.group/trpc-go/trpc-agent-go/storage/postgres => ../../../storage/postgres
)

require (
	github.com/DATA-DOG/go-sqlmock v1.5.2
	trpc.group/trpc-go/trpc-agent-go v0.2.0
	trpc.group/trpc-go/trpc-agent-go/storage/postgres v0.0.0-20251030021201-13c22db836ca
)

require (
	github.com/jackc/pgpassfile v1.0.0 // indirect
	github.com/jackc/pgservicefile v0.0.0-20240606120523-5a60cdf6a761 // indirect
	github.com/jackc/pgx/v5 v5.7.1 // indirect
	github.com/jackc/puddle/v2 v2.2.2 // indirect
	golang.org/x/crypto v0.32.0 // indirect
	golang.org/x/sync v0.10.0 // indirect
	golang.org/x/text v0.21.0 // indirect
)
