# dump

📄 [![Run Go Tests](https://github.com/Kabilan108/dump/actions/workflows/test.yml/badge.svg)](https://github.com/Kabilan108/dump/actions/workflows/test.yml)

<img src="./assets/dump.png" width="250" height="250" alt="dumpy">

A simple CLI tool that dumps text files from a directory into a format that's easy for LLMs to understand.

## Why?

When working with LLMs, you often need to provide multiple files as context. This tool makes it super easy by:
- Walking through a directory recursively
- Filtering out binary files and respecting `.gitignore`
- Outputting text files in a structured XML-like format that LLMs can parse

![demo](./assets/demo.gif)

## Installation

### Option 1: Download from GitHub Releases

```bash
# Download the latest release for your platform from:
# https://github.com/Kabilan108/dump/releases/latest

# Example for Linux:
curl -L https://github.com/Kabilan108/dump/releases/latest/download/dump_linux_amd64 -o dump
chmod +x dump
sudo mv dump /usr/local/bin/
```

### Option 2: Build from Source

```bash
# Using go install
go install github.com/kabilan108/dump@latest

# Or clone and build
git clone https://github.com/kabilan108/dump
cd dump
make build
```

## Usage

```bash
# Dump all text files from current directory
dump

# Include specific files using glob patterns
dump "*.go" "*.md"

# Add ignore patterns (can use multiple times)
dump -i "*.log" -i "node_modules"

# Filter out lines matching a regex pattern
dump -f "TODO|FIXME"

# Combine options
dump "*.go" -i "vendor" -f "^//.*"

# Get help
dump -h
```

## Output Format

Files are output in this format:
```xml
<file path="relative/path/to/file">
content of the file goes here
multiple lines are preserved
</file>
```

## License

MIT
