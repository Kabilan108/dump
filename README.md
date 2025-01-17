# dump 📄

A simple CLI tool that dumps text files from a directory into a format that's easy for LLMs to understand.

## Why?

When working with LLMs, you often need to provide multiple files as context. This tool makes it super easy by:
- Walking through a directory recursively
- Filtering out binary files and respecting `.gitignore`
- Outputting text files in a structured XML-like format that LLMs can parse

## Installation

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

# Specify a different directory
dump -d /path/to/dir

# Add ignore patterns (can use multiple times)
dump -i "*.log" -i "node_modules"

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
