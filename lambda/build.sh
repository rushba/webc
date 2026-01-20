#!/bin/bash
# Build Lambda for ARM64 Linux

set -e

cd "$(dirname "$0")"

echo "Building Lambda..."
GOOS=linux GOARCH=arm64 go build -tags lambda.norpc -o bootstrap main.go

echo "Creating zip..."
zip bootstrap.zip bootstrap

echo "Done: bootstrap.zip"
ls -lh bootstrap.zip
