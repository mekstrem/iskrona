# Repository guidance

- This is one Go module; the CLI, TUI, market processing, and colocated tests are all in `cmd/iskrona`. `main.go` wires the Cobra commands; bare `iskrona` and `tui` both start Market Relay, while `watch` is a deprecated alias.
- Use Go 1.26 or newer as required by `go.mod` and CI; the README and release workflow currently specify older versions.
- Required checks: `go test ./...`, `go vet ./...`, then `go build ./cmd/iskrona` (CI builds `./...`). The command-specific build writes `iskrona` in the current directory. Run one test with `go test ./cmd/iskrona -run '^TestName$'`.
- The watcher baselines existing exports at startup and processes new or changed `.txt` files only after stable scans. Its polling loop accepts injected scans and ticks for deterministic tests.
- Normal startup can create or migrate user configuration; `doctor` is read-only. EVE login uses the OS credential store and a browser callback on localhost port 8765.
