// main_test.go
package main

import (
	"bytes"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"

	"github.com/gobwas/glob"
	ignore "github.com/sabhiram/go-gitignore"
)

func TestIsTextFile(t *testing.T) {
	// Helper to create temp files
	createTextFile := func(t *testing.T, content string) string {
		t.Helper()
		f, err := os.CreateTemp(t.TempDir(), "test_text_*.txt")
		if err != nil {
			t.Fatalf("Failed to create temp text file: %v", err)
		}
		_, err = f.WriteString(content)
		if err != nil {
			f.Close() // Close before failing
			t.Fatalf("Failed to write to temp text file: %v", err)
		}
		f.Close()
		return f.Name()
	}

	createBinaryFile := func(t *testing.T, content []byte) string {
		t.Helper()
		f, err := os.CreateTemp(t.TempDir(), "test_binary_*.bin")
		if err != nil {
			t.Fatalf("Failed to create temp binary file: %v", err)
		}
		_, err = f.Write(content)
		if err != nil {
			f.Close()
			t.Fatalf("Failed to write to temp binary file: %v", err)
		}
		f.Close()
		return f.Name()
	}

	// Test cases using t.Run for better organization
	t.Run("Valid UTF-8 Text", func(t *testing.T) {
		path := createTextFile(t, "This is a valid text file.\nWith multiple lines.")
		if !isTextFile(path) {
			t.Errorf("Expected isTextFile(%q) to be true, got false", path)
		}
	})

	t.Run("File with Null Bytes", func(t *testing.T) {
		path := createBinaryFile(t, []byte("This contains a null byte \x00 here."))
		if isTextFile(path) {
			t.Errorf("Expected isTextFile(%q) to be false (null byte), got true", path)
		}
	})

	t.Run("Non UTF-8 Bytes", func(t *testing.T) {
		// Example invalid UTF-8 sequence
		path := createBinaryFile(t, []byte{0x68, 0x65, 0x6c, 0x6c, 0x6f, 0x80}) // Invalid continuation byte
		if isTextFile(path) {
			t.Errorf("Expected isTextFile(%q) to be false (invalid UTF-8), got true", path)
		}
	})

	t.Run("Empty File", func(t *testing.T) {
		path := createTextFile(t, "")
		if !isTextFile(path) {
			t.Errorf("Expected isTextFile(%q) for empty file to be true, got false", path)
		}
	})

	t.Run("Non-existent File", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "non_existent_file.txt")
		if isTextFile(path) {
			t.Errorf("Expected isTextFile(%q) for non-existent file to be false, got true", path)
		}
	})
}

func TestFormatOutput(t *testing.T) {
	testCases := []struct {
		name     string
		output   Item
		format   string
		tag      string
		expected string
	}{
		{
			name:     "XML format",
			output:   Item{path: "src/main.go", content: "package main\nfunc main() {}\n"},
			format:   "xml",
			tag:      "file",
			expected: "<file path='src/main.go'>\npackage main\nfunc main() {}\n</file>\n",
		},
		{
			name:     "Markdown format",
			output:   Item{path: "src/main.go", content: "package main\nfunc main() {}\n"},
			format:   "md",
			tag:      "file", // Tag is ignored for md format
			expected: "```src/main.go\npackage main\nfunc main() {}\n```\n",
		},
		{
			name:     "XML with custom tag",
			output:   Item{path: "test.txt", content: "hello world\n"},
			format:   "xml",
			tag:      "source",
			expected: "<source path='test.txt'>\nhello world\n</source>\n",
		},
		{
			name:     "URL with XML format",
			output:   Item{path: "https://example.com", content: "web content\n"},
			format:   "xml",
			tag:      "file",
			expected: "<web url='https://example.com'>\nweb content\n</web>\n",
		},
		{
			name:     "URL with Markdown format",
			output:   Item{path: "https://example.com", content: "web content\n"},
			format:   "md",
			tag:      "file",
			expected: "```https://example.com\nweb content\n```\n",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			result := formatItem(tc.output, tc.format, tc.tag)
			if result != tc.expected {
				t.Errorf("formatOutput() = %q, expected %q", result, tc.expected)
			}
		})
	}
}

func TestFormatTmuxItem(t *testing.T) {
    t.Run("XML format", func(t *testing.T) {
        ti := TmuxPaneItem{
            id: "%1", session: "s", window: "0", pane: "1", content: "line1\nline2\n",
        }
        got := formatTmuxItem(ti, "xml")
        expected := "<tmux_pane id='%1' session='s' window='0' pane='1'>\nline1\nline2\n</tmux_pane>\n"
        if got != expected {
            t.Errorf("formatTmuxItem(xml) = %q, expected %q", got, expected)
        }
    })

    t.Run("Markdown format", func(t *testing.T) {
        ti := TmuxPaneItem{
            id: "%2", session: "dev", window: "1", pane: "0", content: "echo hi\n",
        }
        got := formatTmuxItem(ti, "md")
        expected := "```shell\n# tmux-pane: id='%2' session='dev' window='1' pane='0'\n\n" +
            "echo hi\n```\n"
        if got != expected {
            t.Errorf("formatTmuxItem(md) = %q, expected %q", got, expected)
        }
    })
}

func TestWriteContents(t *testing.T) {
	testCases := []struct {
		name     string
		contents []string
		expected string
	}{
		{
			name:     "Plain percent",
			contents: []string{"100% sure\n"},
			expected: "100% sure\n",
		},
		{
			name:     "Format verb",
			contents: []string{"placeholder %s should stay literal\n"},
			expected: "placeholder %s should stay literal\n",
		},
		{
			name:     "Multiple contents",
			contents: []string{"first\n", "second\n", "third\n"},
			expected: "first\nsecond\nthird\n",
		},
		{
			name:     "Empty contents",
			contents: []string{},
			expected: "",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			var buf bytes.Buffer
			if err := writeContents(&buf, tc.contents); err != nil {
				t.Fatalf("writeContents: %v", err)
			}
			if got := buf.String(); got != tc.expected {
				t.Fatalf("got %q, want %q", got, tc.expected)
			}
		})
	}
}

func TestBuildIgnoreList(t *testing.T) {
	baseDir := t.TempDir()
	gitignorePath := filepath.Join(baseDir, ".gitignore")
	gitignoreContent := `
*.log
temp/
`
	if err := os.WriteFile(gitignorePath, []byte(gitignoreContent), 0o644); err != nil {
		t.Fatalf("Failed to write test .gitignore: %v", err)
	}

	extraPatterns := []string{"*.tmp", "build/"}

	gitIgnore, err := buildIgnoreList(baseDir, extraPatterns)
	if err != nil {
		t.Fatalf("buildIgnoreList failed: %v", err)
	}

	if gitIgnore == nil {
		t.Fatal("buildIgnoreList returned nil ignore object")
	}

	testCases := []struct {
		path     string
		expected bool // true if should be ignored
	}{
		{"myfile.log", true},        // Ignored by .gitignore
		{"src/myfile.log", true},    // Ignored by .gitignore (nested)
		{"temp/file.txt", true},     // Ignored by .gitignore
		{"another.tmp", true},       // Ignored by extra pattern
		{"build/output", true},      // Ignored by extra pattern
		{"src/main.go", false},      // Not ignored
		{"README.md", false},        // Not ignored
		{".git/HEAD", true},         // Implicitly ignored
		{".gitignore", true},        // Implicitly ignored
		{"other/nested/.git", true}, // Implicitly ignored
	}

	for _, tc := range testCases {
		if gitIgnore.MatchesPath(tc.path) != tc.expected {
			t.Errorf("gitIgnore.MatchesPath(%q): expected %v, got %v", tc.path, tc.expected, !tc.expected)
		}
	}

	// Test case where .gitignore doesn't exist
	emptyDir := t.TempDir()
	gitIgnoreNoFile, err := buildIgnoreList(emptyDir, extraPatterns)
	if err != nil {
		t.Fatalf("buildIgnoreList failed without .gitignore: %v", err)
	}
	if gitIgnoreNoFile == nil {
		t.Fatal("buildIgnoreList returned nil ignore object without .gitignore")
	}
	if !gitIgnoreNoFile.MatchesPath("some.tmp") {
		t.Errorf("Expected ignore object without .gitignore to match extra pattern 'some.tmp'")
	}
	if gitIgnoreNoFile.MatchesPath("some.txt") {
		t.Errorf("Expected ignore object without .gitignore to NOT match 'some.txt'")
	}
}

func TestDumpFile(t *testing.T) {
	// Create a temporary file with sample content
	content := `Line 1
Line 2 with secret
Line 3
Another secret line here
Line 5`
	tmpFile, err := os.CreateTemp(t.TempDir(), "dump_test_*.txt")
	if err != nil {
		t.Fatalf("Failed to create temp file: %v", err)
	}
	if _, err := tmpFile.WriteString(content); err != nil {
		tmpFile.Close()
		t.Fatalf("Failed to write to temp file: %v", err)
	}
	tmpFile.Close() // Close the file before reading

	absolutePath := tmpFile.Name()
	relativePath := "relative/path/to/file.txt"

	// Helper function to compare content with line-by-line accuracy
	compareContent := func(t *testing.T, got, expected string) {
		t.Helper()
		// Strip any trailing newlines from both strings before comparing
		got = strings.TrimRight(got, "\n")
		expected = strings.TrimRight(expected, "\n")

		// Compare line by line to handle different line ending situations
		gotLines := strings.Split(got, "\n")
		expectedLines := strings.Split(expected, "\n")

		if len(gotLines) != len(expectedLines) {
			t.Errorf("Line count mismatch: got %d lines, expected %d lines",
				len(gotLines), len(expectedLines))
			return
		}

		for i, line := range expectedLines {
			if gotLines[i] != line {
				t.Errorf("Line %d mismatch:\nExpected: %q\nGot:      %q",
					i+1, line, gotLines[i])
			}
		}
	}

	t.Run("No Filter", func(t *testing.T) {
		output, err := dumpFile(absolutePath, relativePath, nil) // No filter
		if err != nil {
			t.Errorf("dumpFile failed unexpectedly: %v", err)
			return
		}

		if output == nil {
			t.Fatal("dumpFile returned nil output")
		}

		if output.path != relativePath {
			t.Errorf("Expected path %q, got %q", relativePath, output.path)
		}

		// Compare content (remove trailing newline that dumpFile adds)
		expectedContent := content + "\n"
		compareContent(t, output.content, expectedContent)
	})

	t.Run("With Filter", func(t *testing.T) {
		filterRegex := regexp.MustCompile(`secret`)
		output, err := dumpFile(absolutePath, relativePath, filterRegex)
		if err != nil {
			t.Errorf("dumpFile with filter failed unexpectedly: %v", err)
			return
		}

		if output == nil {
			t.Fatal("dumpFile returned nil output")
		}

		if output.path != relativePath {
			t.Errorf("Expected path %q, got %q", relativePath, output.path)
		}

		expectedContent := `Line 1
Line 3
Line 5
`
		compareContent(t, output.content, expectedContent)
	})

	t.Run("Non-existent file", func(t *testing.T) {
		_, err := dumpFile(filepath.Join(t.TempDir(), "non_existent"), "rel/path", nil)
		if err == nil {
			t.Error("Expected dumpFile to return an error for non-existent file, but got nil")
		}
	})
}

// Helper function for tests that simulates file processing
func processFile(path string, baseDir string, gitIgnore *ignore.GitIgnore, filter *regexp.Regexp) {
	relPath, err := filepath.Rel(baseDir, path)
	if err != nil {
		return
	}

	// Skip ignored files
	if gitIgnore.MatchesPath(relPath) {
		return
	}

	// Only dump text files
	if !isTextFile(path) {
		return
	}

	// Dump file contents (without error checking for test simplicity)
	_, _ = dumpFile(path, relPath, filter)
}

func TestCompilePatterns(t *testing.T) {
	testCases := []struct {
		name     string
		patterns []string
		wantErr  bool
	}{
		{
			name:     "Valid patterns",
			patterns: []string{"*.go", "src/**/*.js", "!vendor"},
			wantErr:  false,
		},
		{
			name:     "Empty pattern list",
			patterns: []string{},
			wantErr:  false,
		},
		{
			name:     "Single pattern",
			patterns: []string{"*.txt"},
			wantErr:  false,
		},
		{
			name:     "Invalid pattern",
			patterns: []string{"[invalid"},
			wantErr:  true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			globs, err := compilePatterns(tc.patterns)

			if tc.wantErr {
				if err == nil {
					t.Error("Expected error but got nil")
				}
				return
			}

			if err != nil {
				t.Errorf("Expected no error but got: %v", err)
				return
			}

			if len(globs) != len(tc.patterns) {
				t.Errorf("Expected %d compiled patterns, got %d", len(tc.patterns), len(globs))
			}
		})
	}
}

func TestMatchesAny(t *testing.T) {
	// Helper to compile test patterns
	compileTestPatterns := func(t *testing.T, patterns []string) []glob.Glob {
		t.Helper()
		globs, err := compilePatterns(patterns)
		if err != nil {
			t.Fatalf("Failed to compile test patterns: %v", err)
		}
		return globs
	}

	testCases := []struct {
		name     string
		path     string
		patterns []string
		expected bool
	}{
		{
			name:     "Match single pattern",
			path:     "file.go",
			patterns: []string{"*.go"},
			expected: true,
		},
		{
			name:     "Match one of multiple patterns",
			path:     "src/main.js",
			patterns: []string{"*.go", "**/*.js"},
			expected: true,
		},
		{
			name:     "No match",
			path:     "docs/README.md",
			patterns: []string{"*.go", "*.js"},
			expected: false,
		},
		{
			name:     "Match complex pattern",
			path:     "src/components/Button.tsx",
			patterns: []string{"src/**/*.{ts,tsx}"},
			expected: true,
		},
		{
			name:     "Empty pattern list",
			path:     "anything.txt",
			patterns: []string{},
			expected: false,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			globs := compileTestPatterns(t, tc.patterns)
			result := matchesAny(tc.path, globs)

			if result != tc.expected {
				t.Errorf("matchesAny(%q, %v) = %v, expected %v",
					tc.path, tc.patterns, result, tc.expected)
			}
		})
	}
}

func TestConcurrentProcessing(t *testing.T) {
	// This test checks the core functionality of the concurrent file processing system
	// Create a simple file structure
	tempDir := t.TempDir()

	files := []struct {
		path    string
		content string
	}{
		{filepath.Join(tempDir, "file1.go"), "package main\nfunc main() {}\n"},
		{filepath.Join(tempDir, "file2.go"), "package utils\nfunc Helper() {}\n"},
		{filepath.Join(tempDir, "README.md"), "# Test Project\n"},
		{filepath.Join(tempDir, "data.txt"), "Some random text\n"},
	}

	// Create the test files
	for _, f := range files {
		err := os.WriteFile(f.path, []byte(f.content), 0o644)
		if err != nil {
			t.Fatalf("Failed to create test file %s: %v", f.path, err)
		}
	}

	// Test the pattern matching with simple patterns
	patterns := []string{"*.go", "*.md"}
	matchedFiles := make(map[string]bool)

	// Compile patterns
	globs, err := compilePatterns(patterns)
	if err != nil {
		t.Fatalf("Failed to compile patterns: %v", err)
	}

	// Simulate the job queue and worker for file processing
	var wg sync.WaitGroup
	processed := make(chan string, len(files))

	// Start the worker goroutine that marks files as processed
	go func() {
		for path := range processed {
			basename := filepath.Base(path)
			matchedFiles[basename] = true
			wg.Done()
		}
	}()

	// Only process files that match the patterns
	for _, f := range files {
		relPath, _ := filepath.Rel(tempDir, f.path)
		if matchesAny(relPath, globs) {
			wg.Add(1)
			processed <- f.path
		}
	}

	// Wait for all processing to complete
	wg.Wait()
	close(processed)

	// Verify expected matches
	expectedMatches := map[string]bool{
		"file1.go":  true,
		"file2.go":  true,
		"README.md": true,
	}

	for filename, expected := range expectedMatches {
		if matchedFiles[filename] != expected {
			t.Errorf("Expected %s to be matched=%v, got %v", filename, expected, matchedFiles[filename])
		}
	}

	// data.txt should not be matched
	if matchedFiles["data.txt"] {
		t.Errorf("data.txt should not have been matched, but was")
	}
}

func TestArrayFlags(t *testing.T) {
	t.Run("String method", func(t *testing.T) {
		af := arrayFlags{"*.go", "*.js", "test/**"}
		expected := "*.go,*.js,test/**"
		if af.String() != expected {
			t.Errorf("String() = %q, expected %q", af.String(), expected)
		}
	})

	t.Run("String method empty", func(t *testing.T) {
		af := arrayFlags{}
		expected := ""
		if af.String() != expected {
			t.Errorf("String() = %q, expected %q", af.String(), expected)
		}
	})

	t.Run("Set method", func(t *testing.T) {
		af := arrayFlags{}

		err := af.Set("*.go")
		if err != nil {
			t.Errorf("Set() returned unexpected error: %v", err)
		}

		err = af.Set("*.js")
		if err != nil {
			t.Errorf("Set() returned unexpected error: %v", err)
		}

		expected := arrayFlags{"*.go", "*.js"}
		if len(af) != len(expected) {
			t.Errorf("Set() length = %d, expected %d", len(af), len(expected))
		}

		for i, v := range expected {
			if af[i] != v {
				t.Errorf("Set() af[%d] = %q, expected %q", i, af[i], v)
			}
		}
	})
}
