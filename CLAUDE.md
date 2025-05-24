# Dump - Development Guide

## Build Commands
- `make build` - Build the dump binary to `build/dump`
- `make run` - Build and run the application
- `make install` - Install dump globally using `go install`
- `make deps` - Update Go module dependencies with `go mod tidy`
- `make clean` - Remove the build directory

## Alternative Build / Lint / Test Commands
- Build: `go build .` or `CGO_ENABLED=0 go build -ldflags="-s -w" -o dump .`
- Cross-compile (static): `GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -ldflags="-s -w" -o dump .`
- Run: `go run main.go`
- Test: `go test ./...`
- Test single file: `go test -v path/to/test_file.go`

## Code Style Guidelines
- **Formatting**: Use `gofmt` for consistent code style
- **Imports**: Group standard library imports first, then 3rd party packages
- **Error Handling**: Always check error returns, use descriptive error messages
- **Naming**: Use camelCase for variables, PascalCase for exported functions/types
- **Comments**: Document all exported functions, use complete sentences
- **Types**: Prefer strong typing, use custom types for domain-specific values
- **Functions**: Keep functions small and focused on a single task
- **Error Flow**: Handle errors early, avoid deeply nested error handling
- **Console Output**: User messages to stdout, errors to stderr
- **File Structure**: Organize related functionality into packages when scaling

## Dependencies
- Keep external dependencies minimal
- Currently using: github.com/sabhiram/go-gitignore