package main

import (
	"bufio"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/sabhiram/go-gitignore"
)

var (
	helpFlag     bool
	ignoreValues arrayFlags // Custom type to accumulate multiple -i/--ignore patterns
	filterRegex  string
)

// arrayFlags is a helper type for collecting multiple -i/--ignore patterns.
type arrayFlags []string

func (af *arrayFlags) String() string {
	return strings.Join(*af, ",")
}

func (af *arrayFlags) Set(value string) error {
	*af = append(*af, value)
	return nil
}

func init() {
	flag.BoolVar(&helpFlag, "h", false, "display help message")
	flag.BoolVar(&helpFlag, "help", false, "display help message")
	flag.Var(&ignoreValues, "i", "glob pattern to ignore (can be repeated)")
	flag.Var(&ignoreValues, "ignore", "glob pattern to ignore (can be repeated)")
	flag.StringVar(&filterRegex, "f", "", "regex pattern to filter out lines")
	flag.StringVar(&filterRegex, "filter", "", "regex pattern to filter out lines")
}

func usage() {
	fmt.Printf(`usage: dump [options] [patterns]

description:
  the 'dump' tool recursively scans the current directory (or specified glob patterns),
  respects .gitignore (if present), applies any additional ignore patterns
  provided, and prints all text files to stdout.

options:
  -h, --help            display this help message and exit.
  -i, --ignore <pat>    add a glob pattern to ignore (may be repeated).
  -f, --filter <regex>  filter out lines matching the regular expression.

arguments:
  patterns              optional glob patterns to specify files to include.
                        if no patterns are provided, all files in current
                        directory will be searched.
`)
}

// isTextFile attempts to determine if a file is text by reading its first
// 512 bytes and checking for usual (binary) control sequences.
func isTextFile(path string) bool {
	file, err := os.Open(path)
	if err != nil {
		// assume its binary if the file can't be opened
		return false
	}
	defer file.Close()

	// read up to 512 bytes
	const ssize = 512 // sample size
	buf := make([]byte, ssize)
	n, err := file.Read(buf)
	if err != nil && err != io.EOF {
		return false
	}
	buf = buf[:n]

	// heuristic: validate utf-8 -> failure ~probably binary
	if !utf8.Valid(buf) {
		return false
	}

	// check for null bytes
	if strings.ContainsRune(string(buf), '\x00') {
		return false
	}

	return true
}

// buildIgnoreList reads the .gitignore file (if present) and merges those patterns
// with any -i/--ignore patterns passed in via the cli. it returns a single ignore
// instance that can be used to test if a file should be ignored.
func buildIgnoreList(baseDir string, extraPatterns []string) (*ignore.GitIgnore, error) {
	ignorePath := filepath.Join(baseDir, ".gitignore")

	// ignore git files
	extraPatterns = append(extraPatterns, ".git")
	extraPatterns = append(extraPatterns, ".gitignore")

	var gitIgnore *ignore.GitIgnore
	if _, err := os.Stat(ignorePath); err == nil {
		gitIgnore, err = ignore.CompileIgnoreFileAndLines(ignorePath, extraPatterns...)
		if err != nil {
			return nil, err
		}
	} else {
		// .gitignore might not exist -> compile only extra patterns
		gitIgnore = ignore.CompileIgnoreLines(extraPatterns...)
	}
	return gitIgnore, nil
}

func main() {
	flag.Parse()

	// if help is requested, show usage and exit
	if helpFlag {
		usage()
		os.Exit(0)
	}

	// Compile filter regex if provided
	var filter *regexp.Regexp
	var err error
	if filterRegex != "" {
		filter, err = regexp.Compile(filterRegex)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error compiling filter regex: %v\n", err)
			os.Exit(1)
		}
	}

	// Get current working directory
	cwd, err := os.Getwd()
	if err != nil {
		fmt.Fprintf(os.Stderr, "error getting current directory: %v\n", err)
		os.Exit(1)
	}

	// build the ignore list from .gitignore + extra patterns
	gitIgnore, err := buildIgnoreList(cwd, ignoreValues)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error building ignore list: %v\n", err)
		os.Exit(1)
	}

	// Get glob patterns from args
	args := flag.Args()
	var globPatterns []string
	if len(args) > 0 {
		globPatterns = args
	} else {
		// Default to all files in current directory
		globPatterns = []string{"**"}
	}

	// Process each glob pattern
	for _, pattern := range globPatterns {
		// Handle absolute path patterns
		searchDir := cwd
		if filepath.IsAbs(pattern) {
			searchDir = filepath.Dir(pattern)
			pattern = filepath.Base(pattern)
		}

		matches, err := filepath.Glob(filepath.Join(searchDir, pattern))
		if err != nil {
			fmt.Fprintf(os.Stderr, "error processing glob pattern '%s': %v\n", pattern, err)
			continue
		}

		// No matches for this pattern
		if len(matches) == 0 {
			continue
		}

		// Process each match
		for _, path := range matches {
			info, err := os.Stat(path)
			if err != nil {
				continue
			}

			// If it's a directory, walk it
			if info.IsDir() {
				err = filepath.Walk(path, func(filePath string, fileInfo fs.FileInfo, err error) error {
					if err != nil || fileInfo.IsDir() {
						return nil
					}

					// Process this file
					processFile(filePath, cwd, gitIgnore, filter)
					return nil
				})
				if err != nil {
					fmt.Fprintf(os.Stderr, "Error walking directory '%s': %v\n", path, err)
				}
			} else {
				// Process individual file
				processFile(path, cwd, gitIgnore, filter)
			}
		}
	}
}

// processFile checks if a file should be included in the dump and processes it
func processFile(path string, baseDir string, gitIgnore *ignore.GitIgnore, filter *regexp.Regexp) {
	// Convert path to relative path
	relPath, err := filepath.Rel(baseDir, path)
	if err != nil {
		// Fallback to absolute path
		relPath = path
	}

	// Check if file is ignored
	if gitIgnore.MatchesPath(relPath) {
		return
	}

	// Skip if not a text file
	if !isTextFile(path) {
		return
	}

	// Dump file contents
	err = dumpFile(path, relPath, filter)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error dumping file '%s': %v\n", relPath, err)
	}
}

// print file contents as:
// <file path="{file_path}">
// {file_contents}
// </file>
func dumpFile(absolutePath, relativePath string, filter *regexp.Regexp) error {
	file, err := os.Open(absolutePath)
	if err != nil {
		return err
	}
	defer file.Close()

	fmt.Printf("<file path=\"%s\">\n", relativePath)

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := scanner.Text()
		// Skip lines that match the filter regex
		if filter != nil && filter.MatchString(line) {
			continue
		}
		fmt.Println(line)
	}
	if err := scanner.Err(); err != nil {
		return errors.New("error reading file content")
	}

	fmt.Println("</file>")
	fmt.Println()
	return nil
}
