# Contributing to ISKrona

Thanks for helping improve ISKrona.

## Before you start

Use GitHub Issues for bug reports and feature requests. Search existing issues first, describe the problem or proposal clearly, and include the ISKrona version, operating system, and a minimal reproduction when relevant. Do not include personal information, market-log data you cannot share, or credentials.

For security-sensitive problems, follow [SECURITY.md](SECURITY.md).

## Development workflow

1. Fork the repository and create a focused branch.
2. Make one coherent change and add or update tests when behavior changes.
3. Run the required checks:

   ```text
   go test ./...
   go vet ./...
   go build .
   ```

4. Open a pull request using the provided template. Explain what changed, why, and how you tested it.

Keep changes idiomatic Go, formatted with `gofmt`, and limited to the issue or improvement being addressed. Avoid unrelated refactors in the same pull request.

## Contributor terms

There is no contributor license agreement or DCO requirement. By submitting a pull request, you agree that your contribution is licensed under the repository's [MIT License](LICENSE).
