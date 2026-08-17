# ISKrona

ISKrona is a focused Go market assistant for EVE Online. It watches exported market data, turns the current order book into practical buy and sell recommendations, estimates margin, and can copy a recommendation to the clipboard.

## Install

Download the archive for your operating system and CPU from [Releases](../../releases), extract it, and place the `iskrona` executable somewhere on your `PATH`.

Alternatively, build from source with Go 1.24 or later:

```text
git clone https://github.com/mekstrem/iskrona.git
cd iskrona
go build .
```

Prebuilt releases are available for Windows, macOS, and Linux on amd64 and arm64.

## Quick start

1. In EVE Online, open a market item and use the market-export action to create a `.txt` export in the EVE `Marketlogs` folder.
2. Run `iskrona setup`. The command creates a configuration file and automatically selects the standard EVE Marketlogs folder when it exists.
3. If the folder was not detected, set `market.marketlogs` in the printed config path, or pass `--path DIR` when running `watch`.
4. Start watching for new exports:

```text
iskrona watch --copy sell
```

`watch` ignores existing files when it starts and prints a snapshot for each new, stable `.txt` export. Press `Ctrl+C` to stop it.

## Commands

```text
iskrona setup
iskrona watch [--path DIR] [--jumps N] [--copy buy|sell] [--no-color]
iskrona inspect --path export.txt
iskrona version
```

- `setup` creates or displays the configuration location.
- `watch` monitors a Marketlogs directory and calculates buy, sell, margin, and profit examples.
- `inspect` validates and counts the orders in one export without starting the watcher.
- Run `iskrona --help` or `iskrona COMMAND --help` for the generated command reference.

ISKrona reads the `price`, `bid`, and `jumps` columns, so extra columns and column ordering in an EVE export do not matter.

## Configuration

The configuration is stored in your platform-specific user config directory as `iskrona/config.toml`:

```toml
[market]
marketlogs = ""
max_jumps = 0

[fees]
broker = 0.02
sales_tax = 0.01

[display]
copy_mode = "sell"
color = true
```

`max_jumps` limits the market distance considered. Fee values are decimal fractions: `0.02` means 2%. Per-run options override the corresponding saved settings.

## Clipboard and troubleshooting

With `--copy buy` or `--copy sell`, ISKrona copies the selected recommendation when it processes an export. If clipboard access is unavailable, analysis continues and a warning is printed.

- **`market.marketlogs is empty`**: run `iskrona setup` and set the Marketlogs path, or use `iskrona watch --path DIR`.
- **No output for an export**: start `watch` before creating the new `.txt` export; files already in the directory are intentionally ignored.
- **Invalid export error**: verify the export has `price`, `bid`, and `jumps` columns.

## Contributing and support

Bug reports and feature requests belong in [GitHub Issues](../../issues). Please read [CONTRIBUTING.md](CONTRIBUTING.md), [SECURITY.md](SECURITY.md), and the [Code of Conduct](CODE_OF_CONDUCT.md) before participating.

## Development

```text
go test ./...
go vet ./...
go build .
```

## License and disclaimer

ISKrona is released under the [MIT License](LICENSE). It is an independent project and is not affiliated with, endorsed by, or sponsored by CCP Games. EVE Online and related marks are the property of CCP Games.
