module example_logging

go 1.20

replace github.com/kataras/pg => ../../

require (
	github.com/jackc/pgx/v5 v5.4.1
	github.com/kataras/pg v0.0.0-00010101000000-000000000000
	github.com/kataras/golog v0.1.9
	github.com/kataras/pgx-golog v0.0.0-20230624202157-16677d51b141
)

require (
	github.com/gertd/go-pluralize v0.2.1 // indirect
	github.com/jackc/pgpassfile v1.0.0 // indirect
	github.com/jackc/pgservicefile v0.0.0-20221227161230-091c0ba34f0a // indirect
	github.com/jackc/puddle/v2 v2.2.0 // indirect
	github.com/kataras/pio v0.0.12 // indirect
	golang.org/x/crypto v0.9.0 // indirect
	golang.org/x/sync v0.1.0 // indirect
	golang.org/x/sys v0.9.0 // indirect
	golang.org/x/text v0.9.0 // indirect
)
