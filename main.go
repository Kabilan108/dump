package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/gobwas/glob"
	"github.com/sabhiram/go-gitignore"
)

var (
	dirs         arrayFlags
	patterns     arrayFlags
	filterRgx    string
	ignoreValues arrayFlags
	urls         arrayFlags
	outfmt       string
	xmlTag       string
	listOnly     bool
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

type fileOutput struct {
	path    string
	content string
}

// Exa API structures
type ExaRequest struct {
	URLs      []string `json:"urls"`
	Text      bool     `json:"text"`
	Livecrawl string   `json:"livecrawl"`
}

type ExaResult struct {
	URL   string `json:"url"`
	Title string `json:"title"`
	Text  string `json:"text"`
}

type ExaResponse struct {
	Results []ExaResult `json:"results"`
	Context string      `json:"context"`
}

func formatOutput(output fileOutput, format string, tag string) string {
	switch format {
	case "md":
		return fmt.Sprintf("```%s\n%s```\n", output.path, output.content)
	default:
		if strings.HasPrefix(output.path, "http://") || strings.HasPrefix(output.path, "https://") {
			return fmt.Sprintf("<%s url='%s'>\n%s</%s>\n", tag, output.path, output.content, tag)
		}
		return fmt.Sprintf("<%s path='%s'>\n%s</%s>\n", tag, output.path, output.content, tag)
	}
}

func dumpFile(path, displayPath string, filter *regexp.Regexp) (*fileOutput, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	var buf bytes.Buffer
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
		return nil, errors.New("error reading file content")
	}

	return &fileOutput{
		path:    displayPath,
		content: buf.String(),
	}, nil
}

func processDirectory(
	baseDir string, globs []glob.Glob, gitIgnore *ignore.GitIgnore,
	filter *regexp.Regexp, wg *sync.WaitGroup, mu *sync.Mutex,
	outputs *[]*fileOutput, pathList *[]string,
) error {
	defer wg.Done()

	parentDir := filepath.Base(baseDir)
	return filepath.WalkDir(baseDir, func(path string, d fs.DirEntry, err error) error {
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

		if d.IsDir() || !isTextFile(path) {
			return nil
		}

		// check if file matches patterns
		if len(globs) > 0 && !matchesAny(relPath, globs) {
			return nil
		}

		displayPath := filepath.Join(parentDir, relPath)

		if listOnly {
			mu.Lock()
			*pathList = append(*pathList, displayPath)
			mu.Unlock()
		} else {
			output, err := dumpFile(path, displayPath, filter)
			if err == nil {
				mu.Lock()
				*outputs = append(*outputs, output)
				mu.Unlock()
			}
		}

		return nil
	})
}

func fetchURLContent(targetURL string, apiKey string) (*fileOutput, error) {
	client := &http.Client{
		Timeout: 30 * time.Second,
	}

	reqBody := ExaRequest{
		URLs:      []string{targetURL},
		Text:      true,
		Livecrawl: "always",
	}

	jsonData, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	req, err := http.NewRequest("POST", "https://api.exa.ai/contents", bytes.NewBuffer(jsonData))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-api-key", apiKey)

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to make request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("API request failed with status: %s", resp.Status)
	}

	var exaResp ExaResponse
	if err := json.NewDecoder(resp.Body).Decode(&exaResp); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	if exaResp.Context == "" {
		return nil, fmt.Errorf("no context field in response")
	}

	return &fileOutput{
		path:    targetURL,
		content: exaResp.Context,
	}, nil
}

func writeContents(w io.Writer, contents []string) error {
	for _, c := range contents {
		// treat snippet as raw text NOT a format string (Fprintf)
		if _, err := io.WriteString(w, c); err != nil {
			return err
		}
	}
	return nil
}

func init() {
	flag.Var(&dirs, "d", "directory to scan (can be repeated)")
	flag.Var(&dirs, "dir", "directory to scan (can be repeated)")
	flag.Var(&patterns, "g", "glob pattern to match (can be repeated)")
	flag.Var(&patterns, "glob", "glob pattern to match (can be repeated)")
	flag.Var(&ignoreValues, "i", "glob pattern to ignore (can be repeated)")
	flag.Var(&ignoreValues, "ignore", "glob pattern to ignore (can be repeated)")
	flag.Var(&urls, "u", "URL to fetch content from via Exa API (can be repeated)")
	flag.Var(&urls, "url", "URL to fetch content from via Exa API (can be repeated)")
	flag.StringVar(&filterRgx, "f", "", "skip lines matching this regex")
	flag.StringVar(&filterRgx, "filter", "", "skip lines matching this regex")
	flag.StringVar(&outfmt, "o", "xml", "output format: xml or md")
	flag.StringVar(&outfmt, "out-fmt", "xml", "output format: xml or md")
	flag.StringVar(&xmlTag, "xml-tag", "file", "custom XML tag name (only for xml output)")
	flag.BoolVar(&listOnly, "l", false, "list file paths only")
	flag.BoolVar(&listOnly, "list", false, "list file paths only")
	flag.BoolVar(&helpFlag, "h", false, "display help message")
	flag.BoolVar(&helpFlag, "help", false, "display help message")

	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, `usage: dump [options] [directories...]

  recursively dumps text files from specified directories,
  respecting .gitignore and custom ignore rules.
  can also fetch content from URLs via Exa API.

options:
  -d|--dir <value>       directory to scan (can be repeated)
  -g|--glob <value>      glob pattern to match (can be repeated)
  -f|--filter <string>   skip lines matching this regex
  -h|--help              display help message
  -i|--ignore <value>    glob pattern to ignore (can be repeated)
  -o|--out-fmt <string>  xml or md (default "xml")
  -l|--list              list file paths only
  -u|--url <value>       URL to fetch content from via Exa API (can be repeated)
  --xml-tag <string>     custom XML tag name (only for xml output) (default "file")

environment variables:
  EXA_API_KEY            required for URL fetching via Exa API
`)
	}
}

func main() {
	flag.Parse()

	args := flag.Args()
	if len(args) > 0 {
		for _, a := range args {
			if strings.HasPrefix(a, "-") {
				fmt.Fprintf(os.Stderr, "unexpected flag after directories\n\n")
				flag.Usage()
				os.Exit(1)
			}
		}
	}

	if helpFlag {
		flag.Usage()
		os.Exit(0)
	}

	if outfmt != "xml" && outfmt != "md" {
		fmt.Fprintf(os.Stderr, "invalid output format %q (must be xml or md)\n", outfmt)
		os.Exit(1)
	}

	// collect dirs
	allDirs := append([]string{}, dirs...)
	allDirs = append(allDirs, flag.Args()...)

	if len(allDirs) == 0 {
		allDirs = []string{"."}
	}

	filter := (*regexp.Regexp)(nil)
	if filterRgx != "" {
		r, err := regexp.Compile(filterRgx)
		if err != nil {
			fmt.Fprintf(os.Stderr, "failed to compile regex filter: %v\n", err)
			os.Exit(1)
		}
		filter = r
	}

	globs, err := compilePatterns(patterns)
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to compile glob patterns: %v\n", err)
		os.Exit(1)
	}

	var wg sync.WaitGroup
	var mu sync.Mutex
	var outputs []*fileOutput
	var pathList []string

	for _, dir := range allDirs {
		absDir, err := filepath.Abs(dir)
		if err != nil {
			fmt.Fprintf(os.Stderr, "failed to resolve directory %q: %v\n", dir, err)
			continue
		}

		gitIgnore, err := buildIgnoreList(absDir, ignoreValues)
		if err != nil {
			fmt.Fprintf(os.Stderr, "failed to build ignore list for %q: %v\n", dir, err)
			continue
		}

		wg.Add(1)
		go processDirectory(absDir, globs, gitIgnore, filter, &wg, &mu, &outputs, &pathList)
	}

	wg.Wait()

	// Process URLs after local files (if any URLs provided)
	if len(urls) > 0 && !listOnly {
		apiKey := os.Getenv("EXA_API_KEY")
		if apiKey == "" {
			fmt.Fprintf(os.Stderr, "error: EXA_API_KEY environment variable is required for URL fetching\n")
		} else {
			for _, url := range urls {
				urlOutput, err := fetchURLContent(url, apiKey)
				if err != nil {
					fmt.Fprintf(os.Stderr, "error fetching URL %s: %v\n", url, err)
					continue
				}
				outputs = append(outputs, urlOutput)
			}
		}
	}

	if listOnly {
		for _, path := range pathList {
			fmt.Println(path)
		}
	} else {
		for _, output := range outputs {
			fmt.Print(formatOutput(*output, outfmt, xmlTag))
		}
	}
}
