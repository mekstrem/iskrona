#!/bin/sh
set -eu
cd "$(dirname "$0")"
go build ./cmd/iskrona
