# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

This is a CLI tool called `dump` that recursively walks through directories and outputs text files in a structured format (XML or Markdown) for easy consumption by LLMs. The tool respects `.gitignore` files and provides flexible filtering options.

## Build and Development Commands

### Building
```bash
# Build for current platform
make build

# Build for Linux AMD64
make build/dump-linux-amd64

# Install to Go bin directory
make install
```

### Testing
```bash
# Run all tests with coverage
go test -v -cover ./...

# Run specific test
go test -v -run TestFunctionName
```

### Other Commands
```bash
# Update dependencies
make deps

# Clean build artifacts
make clean

# Run the built binary
make run
```

## Architecture

The codebase is a single-file Go application (`main.go`) with comprehensive test coverage (`main_test.go`). Key architectural components:

### Core Data Structures
- `arrayFlags`: Custom flag type for handling repeated CLI arguments (e.g., multiple `-i` patterns)
- `fileOutput`: Represents a file with its path and content for output formatting

### Key Functions
- `isTextFile()`: Determines if a file is text-based by checking UTF-8 validity and absence of null bytes
- `buildIgnoreList()`: Creates gitignore matcher from `.gitignore` file and additional patterns
- `compilePatterns()`: Compiles glob patterns for file matching
- `processDirectory()`: Concurrent directory walker that respects ignore patterns
- `dumpFile()`: Reads file content and applies line-level filtering via regex
- `formatOutput()`: Formats file content as XML or Markdown

### Concurrency Model
The tool uses goroutines with `sync.WaitGroup` for concurrent directory processing. Each directory is processed in its own goroutine with mutex-protected shared state for collecting results.

### CLI Interface
Uses Go's `flag` package with custom `arrayFlags` type to support repeated arguments. Supports both short (`-i`) and long (`--ignore`) flag formats.

## Dependencies

- `github.com/gobwas/glob`: Fast glob pattern matching
- `github.com/sabhiram/go-gitignore`: Gitignore pattern parsing and matching

## Testing Strategy

The test suite (`main_test.go`) provides comprehensive coverage including:
- Unit tests for core functions
- Integration tests for file processing workflows
- Concurrent processing validation
- Edge case handling (empty files, binary files, non-existent files)

All tests use temporary directories and files to avoid affecting the repository state.
