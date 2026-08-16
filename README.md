# ISKrona

ISKrona is a focused Go market assistant for EVE Online. It watches exported market data, turns the current order book into practical buy and sell recommendations, estimates margin, and optionally copies one recommendation to the clipboard.

## Workflow

```text
iskrona setup
iskrona watch --copy sell
iskrona inspect --path export.txt
```

`watch` monitors the configured folder and prints a readable market snapshot whenever a new export arrives. `inspect` validates and counts a single export without starting the watcher.

## Configuration

The first run creates a platform-specific `iskrona/config.toml`:

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

Use `iskrona watch --path DIR`, `--jumps N`, `--copy buy|sell`, or `--no-color` for per-run changes. ISKrona reads the `price`, `bid`, and `jumps` columns from EVE exports and applies its own four-significant-figure recommendation rules.

## Development

```text
go test ./...
go vet ./...
go build .
```

ISKrona is an independently implemented Go application for EVE market data. It is not affiliated with EVE Online or other market tools.
