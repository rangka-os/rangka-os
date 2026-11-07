// The module path is the only place the GitHub org appears outside import lines.
// To rename the org run: make rename-module MODULE=github.com/<org>/rangka-os/kernel
module github.com/rangka-os/rangka-os/kernel

go 1.24.0

require github.com/xuri/excelize/v2 v2.9.1

require (
	github.com/jackc/pgx/v5 v5.8.0
	golang.org/x/crypto v0.43.0
	rsc.io/qr v0.2.0
)

require (
	github.com/jackc/pgpassfile v1.0.0 // indirect
	github.com/jackc/pgservicefile v0.0.0-20240606120523-5a60cdf6a761 // indirect
	github.com/jackc/puddle/v2 v2.2.2 // indirect
	github.com/richardlehane/mscfb v1.0.4 // indirect
	github.com/richardlehane/msoleps v1.0.4 // indirect
	github.com/tiendc/go-deepcopy v1.6.0 // indirect
	github.com/xuri/efp v0.0.1 // indirect
	github.com/xuri/nfp v0.0.1 // indirect
	golang.org/x/net v0.45.0 // indirect
	golang.org/x/sync v0.17.0 // indirect
	golang.org/x/text v0.30.0 // indirect
)
