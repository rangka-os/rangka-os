// The module path is the only place the GitHub org appears outside import lines.
// To rename the org run: make rename-module MODULE=github.com/<org>/rangka-os/kernel
module github.com/rangka-os/rangka-os/kernel

go 1.24.0

require github.com/jackc/pgx/v5 v5.8.0

require (
	github.com/jackc/pgpassfile v1.0.0 // indirect
	github.com/jackc/pgservicefile v0.0.0-20240606120523-5a60cdf6a761 // indirect
	github.com/jackc/puddle/v2 v2.2.2 // indirect
	golang.org/x/sync v0.17.0 // indirect
	golang.org/x/text v0.29.0 // indirect
)
