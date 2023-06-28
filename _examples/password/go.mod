module example_password

go 1.20

replace github.com/kataras/pg => ../../

require github.com/kataras/pg v0.0.0-00010101000000-000000000000

require (
	github.com/jackc/pgpassfile v1.0.0 // indirect
	github.com/jackc/pgservicefile v0.0.0-20221227161230-091c0ba34f0a // indirect
	github.com/jackc/pgx/v5 v5.3.1 // indirect
	github.com/jackc/puddle/v2 v2.2.0 // indirect
	golang.org/x/crypto v0.6.0 // indirect
	golang.org/x/sync v0.1.0 // indirect
	golang.org/x/text v0.7.0 // indirect
)
