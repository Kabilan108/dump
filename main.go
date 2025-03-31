package main

import (
	"bufio"
	"bytes"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"unicode/utf8"

	"github.com/gobwas/glob"
	"github.com/sabhiram/go-gitignore"
)

var (
	dir          string
	filterRegex  string
	ignoreValues arrayFlags
	helpFlag     bool
	outputMutex  sync.Mutex
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

func compilePatterns(patterns []string) ([]glob.Glob, error) {
	var globs []glob.Glob
	for _, p := range patterns {
		g, err := glob.Compile(p, '/')
		if err != nil {
			return nil, fmt.Errorf("invalid pattern %q: %w", p, err)
		}
		globs = append(globs, g)
	}
	return globs, nil
}

func matchesAny(path string, globs []glob.Glob) bool {
	for _, g := range globs {
		if g.Match(path) {
			return true
		}
	}
	return false
}

func dumpFile(contents *[]string, path, relPath string, filter *regexp.Regexp) error {
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()

	var buf bytes.Buffer
	buf.WriteString(fmt.Sprintf("<file path='%s'>\n", relPath))

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := scanner.Text()
		if filter != nil && filter.MatchString(line) {
			continue
		}
		buf.WriteString(line)
		buf.WriteByte('\n')
	}

	if err := scanner.Err(); err != nil {
		return errors.New("error reading file content")
	}

	buf.WriteString("</file>\n")

	outputMutex.Lock()
	defer outputMutex.Unlock()
	*contents = append(*contents, buf.String())

	return nil
}

func main() {
	patterns := parseNonFlagArgs()
	flag.Parse()

	if helpFlag {
		usage()
		os.Exit(0)
	}

	filter := (*regexp.Regexp)(nil)
	if filterRegex != "" {
		r, err := regexp.Compile(filterRegex)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error compiling filter regex: %v\n", err)
			os.Exit(1)
		}
		filter = r
	}

	baseDir, err := filepath.Abs(dir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to resolve directory: %v\n", err)
		os.Exit(1)
	}

	globs, err := compilePatterns(patterns)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error compiling glob patterns: %v\n", err)
		os.Exit(1)
	}

	gitIgnore, err := buildIgnoreList(baseDir, ignoreValues)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error building ignore list: %v\n", err)
		os.Exit(1)
	}

	var wg sync.WaitGroup
	jobs := make(chan string, 100)
	contents := []string{}

	go func() {
		for path := range jobs {
			wg.Add(1)
			go func(p string) {
				defer wg.Done()
				relPath, err := filepath.Rel(baseDir, p)
				if err != nil {
					return
				}
				_ = dumpFile(&contents, p, relPath, filter)
			}(path)
		}
	}()

	err = filepath.WalkDir(baseDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		relPath, err := filepath.Rel(baseDir, path)
		if err != nil {
			return nil
		}
		if gitIgnore.MatchesPath(relPath) {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if len(globs) == 0 || matchesAny(relPath, globs) {
			if isTextFile(path) && !d.IsDir() {
				jobs <- path
			}
		}
		return nil
	})
	close(jobs)
	wg.Wait()
	for _, c := range contents {
		fmt.Fprintf(os.Stdout, c)
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "error walking directory: %v\n", err)
		os.Exit(1)
	}
}
