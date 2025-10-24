module trpc.group/trpc-go/trpc-agent-go/session/database

go 1.22

replace (
	trpc.group/trpc-go/trpc-agent-go => ../../
	trpc.group/trpc-go/trpc-agent-go/storage/database => ../../storage/database
)

require (
	github.com/google/uuid v1.6.0
	github.com/spaolacci/murmur3 v1.1.0
	gorm.io/gorm v1.25.12
	trpc.group/trpc-go/trpc-agent-go v0.0.0
	trpc.group/trpc-go/trpc-agent-go/storage/database v0.0.0
)

require (
	github.com/go-sql-driver/mysql v1.7.0 // indirect
	github.com/jinzhu/inflection v1.0.0 // indirect
	github.com/jinzhu/now v1.1.5 // indirect
	go.uber.org/multierr v1.10.0 // indirect
	go.uber.org/zap v1.27.0 // indirect
	golang.org/x/text v0.21.0 // indirect
	gorm.io/driver/mysql v1.5.7 // indirect
	trpc.group/trpc-go/trpc-a2a-go v0.2.5-0.20251020094851-6ab922c9dab1 // indirect
)
