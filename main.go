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
	dir          string
	filterRegex  string
	ignoreValues arrayFlags
	helpFlag     bool
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
	flag.StringVar(&dir, "d", ".", "base directory to scan (default: current directory)")
	flag.StringVar(&dir, "dir", ".", "base directory to scan (default: current directory)")
	flag.Var(&ignoreValues, "i", "glob pattern to ignore (can be repeated)")
	flag.Var(&ignoreValues, "ignore", "glob pattern to ignore (can be repeated)")
	flag.StringVar(&filterRegex, "f", "", "skip lines matching this regex")
	flag.StringVar(&filterRegex, "filter", "", "skip lines matching this regex")
	flag.BoolVar(&helpFlag, "h", false, "display help message")
	flag.BoolVar(&helpFlag, "help", false, "display help message")
}

func usage() {
	fmt.Printf(`usage: dump [options] [patterns]

description:
  recursively dumps text files under the current directory (or --dir),
  respecting .gitignore and custom ignore rules. positional patterns
  are treated as path match filters (e.g., "*.kt", "**/Foo.kt").

options:
  -d, --dir <path>      base directory to scan (default: current directory)
  -i, --ignore <pat>    glob pattern to ignore (can be repeated)
  -f, --filter <regex>  skip lines matching this regex
  -h, --help            display this help message
`)
}

func isTextFile(path string) bool {
	file, err := os.Open(path)
	if err != nil {
		return false
	}
	defer file.Close()

	// read up to 512 bytes
	const s = 512
	buf := make([]byte, s)
	n, err := file.Read(buf)
	if err != nil && err != io.EOF {
		return false
	}

	buf = buf[:n]
	if !utf8.Valid(buf) || strings.ContainsRune(string(buf), '\x00') {
		return false
	}
	return true
}

func buildIgnoreList(baseDir string, extraPatterns []string) (*ignore.GitIgnore, error) {
	ignorePath := filepath.Join(baseDir, ".gitignore")
	extraPatterns = append(extraPatterns, ".git", ".gitignore")
	if _, err := os.Stat(ignorePath); err == nil {
		return ignore.CompileIgnoreFileAndLines(ignorePath, extraPatterns...)
	}
	return ignore.CompileIgnoreLines(extraPatterns...), nil
}

func processFile(
	path, baseDir string, gitIgnore *ignore.GitIgnore, filter *regexp.Regexp,
) {
	relPath, err := filepath.Rel(baseDir, path)
	if err != nil {
		relPath = path
	}
	if gitIgnore.MatchesPath(relPath) {
		return
	}
	if !isTextFile(path) {
		return
	}
	err = dumpFile(path, relPath, filter)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error dumping file '%s': %v\n", relPath, err)
	}
}

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
		if filter != nil && filter.MatchString(line) {
			continue
		}
		fmt.Println(line)
	}
	if err := scanner.Err(); err != nil {
		return errors.New("error reading file content")
	}

	fmt.Println("</file>\n")
	return nil
}

// flag parsing to handle flags appearing after positional arguments
func parseNonFlagArgs() []string {
	args := os.Args[1:]

	var globPatterns []string
	var remainingArgs []string

	for i := 0; i < len(args); i++ {
		arg := args[i]
		if strings.HasPrefix(arg, "-") {
			// This is a flag, skip it and its value if it takes one
			remainingArgs = append(remainingArgs, arg)
			// Check if this flag needs a value
			if (arg == "-f" || arg == "--filter" || arg == "-i" || arg == "--ignore" || arg == "-d" || arg == "--dir") &&
				i+1 < len(args) &&
				!strings.HasPrefix(args[i+1], "-") {
				remainingArgs = append(remainingArgs, args[i+1])
				i++ // skip next value
			}
		} else {
			globPatterns = append(globPatterns, arg)
		}
	}

	tempArgs := append([]string{os.Args[0]}, remainingArgs...)
	os.Args = tempArgs
	return globPatterns
}

func main() {
	globPatterns := parseNonFlagArgs()
	flag.Parse()

	if helpFlag {
		usage()
		os.Exit(0)
	}

	var filter *regexp.Regexp
	var err error
	if filterRegex != "" {
		filter, err = regexp.Compile(filterRegex)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error compiling filter regex: %v\n", err)
			os.Exit(1)
		}
	}

	baseDir, err := filepath.Abs(dir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to resolve directory: %v\n", err)
		os.Exit(1)
	}

	gitIgnore, err := buildIgnoreList(baseDir, ignoreValues)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error building ignore list: %v\n", err)
		os.Exit(1)
	}

	// Use the collected glob patterns or default
	if len(globPatterns) == 0 {
		// Default to all files in current directory
		globPatterns = []string{"**"}
	}

	// Process each glob pattern
	for _, pattern := range globPatterns {
		// Handle absolute path patterns
		searchDir := baseDir
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
					processFile(filePath, baseDir, gitIgnore, filter)
					return nil
				})
				if err != nil {
					fmt.Fprintf(os.Stderr, "Error walking directory '%s': %v\n", path, err)
				}
			} else {
				// Process individual file
				processFile(path, baseDir, gitIgnore, filter)
			}
		}
	}
}
