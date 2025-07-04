# dump 📄

[![Run Go Tests](https://github.com/Kabilan108/dump/actions/workflows/test.yml/badge.svg)](https://github.com/Kabilan108/dump/actions/workflows/test.yml)

<img src="./assets/dump.png" width="250" height="250" alt="dumpy">

A simple CLI tool that dumps text files from directories into a format that's easy for LLMs to understand. Features include directory tree visualization, URL content fetching, and flexible filtering options.

## Why?

When working with LLMs, you often need to provide multiple files as context. This tool makes it super easy by:
- Walking through directories recursively
- Filtering out binary files and respecting `.gitignore`
- Outputting text files in structured XML or Markdown formats
- Visualizing directory structure with tree view
- Fetching content from URLs via Exa API
- Providing flexible filtering and pattern matching

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

### Option 3: Using Nix Flake

```bash
# Install directly
nix profile install github:kabilan108/dump

# Or run without installing
nix run github:kabilan108/dump

# For development
nix develop
```

## Usage

```bash
# Dump all text files from current directory
dump

# Dump from specific directories
dump src/ tests/ docs/

# Dump with directory flags
dump -d src/ -d tests/

# Include specific files using glob patterns
dump -g "*.go" -g "*.md"

# Add ignore patterns (can use multiple times)
dump -i "*.log" -i "node_modules"

# Filter out lines matching a regex pattern
dump -f "TODO|FIXME"

# Include directory tree structure in output
dump -t

# List file paths only (no content)
dump -l

# Markdown output format instead of XML
dump -o md

# Custom XML tag name
dump --xml-tag source

# Fetch content from URLs (requires EXA_API_KEY)
dump -u https://docs.example.com/api

# Mix local files and URLs
dump -d src/ -u https://github.com/user/repo

# Custom timeout and live crawl for URLs
dump -u https://example.com --timeout 30 --live

# Combine options
dump src/ tests/ -g "*.go" -i "vendor" -f "^//.*" -t

# Get help
dump -h
```

### Flag Reference

| Flag | Long Flag | Description |
|------|-----------|-------------|
| `-d` | `--dir` | Directory to scan (can be repeated) |
| `-g` | `--glob` | Glob pattern to match files (can be repeated) |
| `-f` | `--filter` | Skip lines matching this regex |
| `-h` | `--help` | Display help message |
| `-i` | `--ignore` | Glob pattern to ignore files/dirs (can be repeated) |
| `-l` | `--list` | List file paths only (no content) |
| `-o` | `--out-fmt` | Output format: xml or md (default "xml") |
| `-t` | `--tree` | Show directory tree structure |
| `-u` | `--url` | URL to fetch content from via Exa API (can be repeated) |
| | `--xml-tag` | Custom XML tag name (only for xml output) |
| | `--timeout` | Timeout in seconds for URL fetching (default 15) |
| | `--live` | Force fresh content from URLs (livecrawl=always) |

## Output Format

### XML Format (Default)

Files are output in this format:
```xml
<file path="relative/path/to/file">
content of the file goes here
multiple lines are preserved
</file>
```

URLs are output as:
```xml
<file url="https://example.com">
fetched content goes here
</file>
```

### Markdown Format

When using `-o md`, files are output as code blocks:
````markdown
```relative/path/to/file
content of the file goes here
multiple lines are preserved
```
````

URLs are output as:
````markdown
```https://example.com
fetched content goes here
```
````

### Tree Mode

When using `-t`, shows directory structure:
```
.
├── src/
│   ├── main.go
│   └── utils/
│       └── helper.go
└── README.md
```

### List Mode

When using `-l`, shows only file paths:
```
src/main.go
src/utils/helper.go
README.md
```

## URL Fetching

The tool can fetch content from URLs using the Exa API:

```bash
# Set your Exa API key
export EXA_API_KEY="your-api-key"

# Fetch content from URLs
dump -u https://docs.example.com/api -u https://github.com/user/repo

# Combine with local files
dump -d src/ -u https://example.com/docs

# Use live crawl for fresh content
dump -u https://example.com --live

# Custom timeout
dump -u https://example.com --timeout 30
```

### URL Requirements
- `EXA_API_KEY` environment variable must be set
- URLs are processed after local file processing
- Default timeout is 15 seconds (configurable with `--timeout`)
- Use `--live` flag to force fresh content retrieval

## Examples

### Basic Usage
```bash
# Dump current directory
dump

# Dump specific directories with tree view
dump -t -d src/ -d docs/

# Only Go files, ignore vendor, show tree
dump -g "*.go" -i "vendor" -t
```

### Advanced Filtering
```bash
# Skip comment lines and show only file paths
dump -f "^\s*//" -l

# Markdown output with custom patterns
dump -o md -g "*.md" -g "*.txt" -i "node_modules"
```

### Fetching URLs
```bash
# Mix local and remote content
dump -d src/ -u https://raw.githubusercontent.com/user/repo/main/README.md

# Multiple URLs with custom timeout
dump -u https://example.com/api -u https://docs.example.com --timeout 30
```

## License

MIT
