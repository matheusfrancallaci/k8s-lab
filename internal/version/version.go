// Package version carrega a versão do build, injetada via -ldflags no Dockerfile
// (git short SHA). Fica "dev" em build local sem stamping. Uma única fonte da
// verdade para exibir no site, no /healthz e no /metrics.
package version

// Version é o git short SHA do commit buildado (ou "dev"). Injetado com
//
//	go build -ldflags "-X estudo-app/internal/version.Version=<sha>"
var Version = "dev"

// Short devolve a versão para exibição.
func Short() string {
	if Version == "" {
		return "dev"
	}
	return Version
}
