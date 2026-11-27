# Contributing to ISKrona

Thanks for helping improve ISKrona.

## Before you start

Use GitHub Issues for bug reports and feature requests. Search existing issues first, describe the problem or proposal clearly, and include the ISKrona version, operating system, and a minimal reproduction when relevant. Do not include personal information, market-log data you cannot share, or credentials.

For security-sensitive problems, follow [SECURITY.md](SECURITY.md).

## Build from source

Use Go 1.26 or later:

```text
git clone https://github.com/mekstrem/iskrona.git
cd iskrona
go build ./cmd/iskrona
```

For a stable build, check out the corresponding release tag before running `go build`. You can also run `./build.sh` from the repository to build locally.

## Development workflow

1. Fork the repository and create a focused branch.
2. Make one coherent change and add or update tests when behavior changes.
3. Run the required checks:

   ```text
   go test ./...
   go vet ./...
   go build ./cmd/iskrona
   ```

4. Open a pull request using the provided template. Explain what changed, why, and how you tested it.

Keep changes idiomatic Go, formatted with `gofmt`, and limited to the issue or improvement being addressed. Avoid unrelated refactors in the same pull request.

## Offline export simulation

Run `go run -tags dev ./cmd/iskrona` and press `F3` on the dashboard to simulate an export without EVE running. The dev build embeds the real Odysseus export from `The Forge-Odysseus-2026.10.30 065638.txt`; `?` shows the dev-only shortcut.

Each press creates a uniquely named `iskrona-dev-Odysseus-*.txt` in the active watched directory (`market.marketlogs`, or the `--path` override). Buy and sell prices vary independently by up to 5% to simulate changing margins; all other fixture columns are preserved. The simulator waits for the startup baseline and publishes only a complete file, without overwriting existing exports. The normal watcher parses it, updates recommendations, and performs the usual auto-copy. Creation errors appear in the dashboard. Simulation files remain on disk; delete only these generated files when finished. The folder must support hard links.

For an isolated session, create an empty folder and use `go run -tags dev ./cmd/iskrona --path /path/to/folder`. Test both variants with `go test ./cmd/iskrona` and `go test -tags dev ./cmd/iskrona`. Normal builds and release archives omit the fixture and simulation implementation; `F3` is inactive and absent from help.

## Contributor terms

There is no contributor license agreement or DCO requirement. By submitting a pull request, you agree that your contribution is licensed under the repository's [MIT License](LICENSE).
