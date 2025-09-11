# AGENTS.md

This file provides guidance to AI agents when working with code in this repository.

## Project Overview

This is a CLI tool called `dump` that recursively walks through directories and outputs text files in a structured format (XML or Markdown) for easy consumption by LLMs. The tool respects `.gitignore` files, provides flexible filtering options (glob and extension filters), supports directory tree visualization, and can also fetch content from URLs via the Exa API.

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
- `arrayFlags`: Custom flag type for handling repeated CLI arguments (e.g., multiple `-i`, `-u`, `-e`)
- `Item`: Represents a file or URL with its path/URL and content for output formatting
- `TmuxPaneItem`: Represents a captured tmux pane and its content
- `TreeNode` / `DirectoryOutput`: Structures for building and rendering a directory tree and collected items
- `ExaRequest` / `ExaResponse`: Structures for Exa API requests and responses

### Key Functions
- `isTextFile()`: Determines if a file is text-based by checking UTF-8 validity and absence of null bytes
- `buildIgnoreList()`: Creates gitignore matcher from `.gitignore` file and additional patterns
- `compilePatterns()`: Compiles glob patterns for file matching
- `processDirectory()`: Directory walker that respects ignore patterns and applies filters (glob and extension)
- `dumpFile()`: Reads file content and applies line-level filtering via regex
- `fetchURLContent()`: Fetches web content from URLs using Exa API with proper error handling
- `formatItem()`: Formats file or URL content as XML or Markdown
- `formatTreeOutput()`: Renders directory tree structure
- `resolveTmuxSelectors()`, `capturePaneContent()`, `fetchTmuxConcurrently()`: tmux helpers for capturing terminal panes

### Concurrency Model
The tool uses goroutines with `sync.WaitGroup` for concurrent directory processing. Each directory is processed in its own goroutine and results are funneled through channels. URL fetching uses a bounded worker pool with gentle staggering between requests to respect API rate limits. tmux pane capture also uses a small worker pool.

### CLI Interface
Uses Go's `flag` package with custom `arrayFlags` type to support repeated arguments. Supports both short (`-i`, `-u`, `-t`, `-l`, `-e`) and long (`--ignore`, `--url`, `--tree`, `--list`, `--ext`) flag formats. Features include:
- Directory tree visualization (`-t/--tree`)
- List-only mode (`-l/--list`) for file paths without content
- Extension filters (`-e/--ext`) with case-insensitive matching; can be repeated. Combines with glob filters using OR semantics.
- Custom XML tag names (`--file-tag`)
- Configurable timeout for URL fetching (`--timeout`)
- Live crawl option for fresh URL content (`--live`)
- URL functionality requires the `EXA_API_KEY` environment variable

Notes on filtering semantics:
- If neither globs nor extensions are provided, all text files (respecting ignore rules) are included.
- If globs and/or extensions are provided, a file is included if it matches any glob OR any listed extension.

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

## URL Dumping Feature

The tool supports fetching content from URLs via the Exa API in addition to processing local files.

### Usage
```bash
# Fetch content from URLs alongside local files
dump -d src/ -u https://docs.example.com/api -u https://github.com/user/repo

# URL-only dumping
dump -u https://example.com/documentation

# Mix with other options
dump -g "*.go" -u https://golang.org/doc/ -o md

# List files in directory without content
dump -l -d src/

# Custom timeout and live crawl
dump -u https://example.com --timeout 30 --live
```

### Requirements
- `EXA_API_KEY` environment variable must be set
- URLs are processed after all local file processing completes
- API calls have a configurable timeout (default 15 seconds)
- Live crawl option forces fresh content retrieval

### Output Format
- **XML**: `<file url='https://example.com'>content</file>`
- **Markdown**: ````https://example.com\ncontent\n````

### Error Handling
- Missing API key: Error logged to stderr, local files still processed
- Individual URL failures: Error logged to stderr, other URLs still processed
- Network issues: Graceful handling with descriptive error messages

### API Integration
Uses Exa's `/contents` endpoint with:
- `text: true` for text extraction
- `livecrawl: "fallback"` by default, or `"always"` when `--live` flag is used
- Returns the `context` field containing combined text content
- Configurable timeout via `--timeout` flag (default 15 seconds)
