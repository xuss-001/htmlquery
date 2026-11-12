# Native environment

Validated with Go 1.24.9, GCC 15.2.0, Git 2.53.0 on Ubuntu 26.04 LTS
(WSL2 Linux amd64). The upstream Go language requirement is unchanged.
Dependencies are pinned by go.mod and verified using go.sum.

Run `go test ./...` and `go test -race ./...`.
Tests use local HTTP listeners and do not require a public application service.
Permit local networking when using a sandbox.

This machine uses persistent caches outside the repository:
```
export PATH=/home/xuss-/.local/bin:/usr/local/bin:/usr/bin:/bin
export GOCACHE=/home/xuss-/userfeedback/cache/task005/go-build
export GOMODCACHE=/home/xuss-/userfeedback/cache/task005/go-mod
export GOTOOLCHAIN=local
export GOPROXY=off
export GOSUMDB=off
```
The caches have been populated for the versions in go.mod. On another machine,
populate the cache from a trusted Go module proxy first; normal module checksum
validation remains enabled via go.sum. GOSUMDB=off is for this offline execution.
No credentials or machine configuration belong in the repository.
