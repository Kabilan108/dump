// main_test.go
package main

import (
	"bytes"
	"io"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

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

// Helper function to capture stdout
func captureOutput(t *testing.T, action func()) string {
	t.Helper()
	oldStdout := os.Stdout // Keep backup of the real stdout
	r, w, err := os.Pipe() // Create a pipe
	if err != nil {
		t.Fatalf("Failed to create pipe: %v", err)
	}
	os.Stdout = w // Set stdout to the write end of the pipe

	// Execute the action whose output we want to capture
	action()

	// Close the write end and restore stdout
	w.Close()
	os.Stdout = oldStdout

	// Read everything from the read end of the pipe
	var buf bytes.Buffer
	if _, err := io.Copy(&buf, r); err != nil {
		// Log instead of Fatalf to ensure stdout is restored
		log.Printf("Failed to read captured output: %v", err)
	}
	r.Close() // Close the read end

	return buf.String()
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

	t.Run("No Filter", func(t *testing.T) {
		output := captureOutput(t, func() {
			err := dumpFile(absolutePath, relativePath, nil) // No filter
			if err != nil {
				t.Errorf("dumpFile failed unexpectedly: %v", err)
			}
		})

		expectedStart := `<file path="relative/path/to/file.txt">` + "\n"
		expectedEnd := "\n" + `</file>` + "\n\n" // Includes trailing newline

		if !strings.HasPrefix(output, expectedStart) {
			t.Errorf("Output missing expected start:\nExpected prefix: %q\nGot: %q", expectedStart, output)
		}
		if !strings.HasSuffix(output, expectedEnd) {
			t.Errorf("Output missing expected end:\nExpected suffix: %q\nGot: %q", expectedEnd, output)
		}
		// Check content between tags
		contentInOutput := strings.TrimSuffix(strings.TrimPrefix(output, expectedStart), expectedEnd)
		if contentInOutput != content {
			t.Errorf("Output content mismatch:\nExpected:\n%s\nGot:\n%s", content, contentInOutput)
		}
	})

	t.Run("With Filter", func(t *testing.T) {
		filterRegex := regexp.MustCompile(`secret`)
		output := captureOutput(t, func() {
			err := dumpFile(absolutePath, relativePath, filterRegex)
			if err != nil {
				t.Errorf("dumpFile with filter failed unexpectedly: %v", err)
			}
		})

		expectedContent := `Line 1
Line 3
Line 5`
		expectedStart := `<file path="relative/path/to/file.txt">` + "\n"
		expectedEnd := "\n" + `</file>` + "\n\n" // Includes trailing newline

		if !strings.HasPrefix(output, expectedStart) {
			t.Errorf("Filtered output missing expected start:\nExpected prefix: %q\nGot: %q", expectedStart, output)
		}
		if !strings.HasSuffix(output, expectedEnd) {
			t.Errorf("Filtered output missing expected end:\nExpected suffix: %q\nGot: %q", expectedEnd, output)
		}
		contentInOutput := strings.TrimSuffix(strings.TrimPrefix(output, expectedStart), expectedEnd)
		if contentInOutput != expectedContent {
			t.Errorf("Filtered output content mismatch:\nExpected:\n%s\nGot:\n%s", expectedContent, contentInOutput)
		}
	})

	t.Run("Non-existent file", func(t *testing.T) {
		err := dumpFile(filepath.Join(t.TempDir(), "non_existent"), "rel/path", nil)
		if err == nil {
			t.Error("Expected dumpFile to return an error for non-existent file, but got nil")
		}
	})
}

func TestProcessFile(t *testing.T) {
	// Setup: Create a base directory and some files
	baseDir := t.TempDir()
	subDir := filepath.Join(baseDir, "src")
	if err := os.Mkdir(subDir, 0755); err != nil {
		t.Fatalf("Failed to create subDir: %v", err)
	}

	// Files
	textFileAbs := filepath.Join(subDir, "main.go")
	textFileRel := "src/main.go" // Relative to baseDir
	os.WriteFile(textFileAbs, []byte("package main\n\nfunc main() {}"), 0644)

	binaryFileAbs := filepath.Join(subDir, "app.bin")
	os.WriteFile(binaryFileAbs, []byte{0xDE, 0xAD, 0xBE, 0xEF}, 0644)

	ignoredFileAbs := filepath.Join(baseDir, "output.log")
	os.WriteFile(ignoredFileAbs, []byte("Log message"), 0644)

	// Ignore list
	gitIgnore := ignore.CompileIgnoreLines("*.log", ".git") // Simple ignore list for testing

	// Filter
	filterRegex := regexp.MustCompile(`main`) // Filter lines with "main"

	// --- Test Cases ---

	t.Run("Process Text File (No Filter)", func(t *testing.T) {
		output := captureOutput(t, func() {
			processFile(textFileAbs, baseDir, gitIgnore, nil) // No filter
		})
		expected := `<file path="` + textFileRel + `">` + "\n" +
			`package main` + "\n\n" +
			`func main() {}` + "\n" +
			`</file>` + "\n\n"
		if output != expected {
			t.Errorf("processFile output mismatch (no filter):\nExpected:\n%s\nGot:\n%s", expected, output)
		}
	})

	t.Run("Process Text File (With Filter)", func(t *testing.T) {
		output := captureOutput(t, func() {
			processFile(textFileAbs, baseDir, gitIgnore, filterRegex) // With filter
		})
		// Expecting empty content as both lines contain "main"
		expected := `<file path="` + textFileRel + `">` + "\n" +
			"\n" + // Empty content
			`</file>` + "\n\n"
		if output != expected {
			t.Errorf("processFile output mismatch (with filter):\nExpected:\n%s\nGot:\n%s", expected, output)
		}
	})

	t.Run("Process Binary File", func(t *testing.T) {
		output := captureOutput(t, func() {
			processFile(binaryFileAbs, baseDir, gitIgnore, nil)
		})
		if output != "" {
			t.Errorf("Expected no output for binary file, got: %q", output)
		}
	})

	t.Run("Process Ignored File", func(t *testing.T) {
		output := captureOutput(t, func() {
			processFile(ignoredFileAbs, baseDir, gitIgnore, nil)
		})
		if output != "" {
			t.Errorf("Expected no output for ignored file, got: %q", output)
		}
	})

	t.Run("Process Non-Existent File", func(t *testing.T) {
		// processFile currently doesn't explicitly handle non-existent files
		// itself, it relies on isTextFile/dumpFile errors.
		// isTextFile returns false, so it should produce no output.
		nonExistentAbs := filepath.Join(baseDir, "nosuchfile.txt")
		output := captureOutput(t, func() {
			processFile(nonExistentAbs, baseDir, gitIgnore, nil)
		})
		if output != "" {
			t.Errorf("Expected no output for non-existent file, got: %q", output)
		}
	})
}
