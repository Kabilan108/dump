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
	"net/url"
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
	liveCrawl    bool
	timeoutSec   int
	outfmt       string
	xmlTag       string
	listOnly     bool
	treeFlag     bool
	helpFlag     bool
)

const exaBaseURL = "https://api.exa.ai/contents"

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

type Dumped struct {
	path    string
	content string
}

type treeNode struct {
	name     string
	path     string
	isDir    bool
	children []*treeNode
}

type directoryResult struct {
	tree     *treeNode
	outputs  []*Dumped
	pathList []string
	dirName  string
}

type ExaRequest struct {
	URLs      []string `json:"urls"`
	Text      bool     `json:"text"`
	Context   bool     `json:"context"`
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

func formatOutput(output Dumped, format string, tag string) string {
	switch format {
	case "md":
		return fmt.Sprintf("```%s\n%s```\n", output.path, output.content)
	default:
		if strings.HasPrefix(output.path, "http://") || strings.HasPrefix(output.path, "https://") {
			return fmt.Sprintf("<web url='%s'>\n%s</web>\n", output.path, output.content)
		}
		return fmt.Sprintf("<%s path='%s'>\n%s</%s>\n", tag, output.path, output.content, tag)
	}
}

func dumpFile(path, displayPath string, filter *regexp.Regexp) (*Dumped, error) {
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

	return &Dumped{
		path:    displayPath,
		content: buf.String(),
	}, nil
}

func processDirectory(
	baseDir string, globs []glob.Glob, gitIgnore *ignore.GitIgnore, filter *regexp.Regexp,
	outputs *[]*Dumped, pathList *[]string, treeRoot *treeNode,
) error {
	parentDir := filepath.Base(baseDir)

	var nodeMap map[string]*treeNode
	if treeRoot != nil {
		nodeMap = make(map[string]*treeNode)
		nodeMap[baseDir] = treeRoot
	}

	err := filepath.WalkDir(baseDir, func(path string, d fs.DirEntry, err error) error {
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

		// handle directory nodes for tree (if tree building is enabled)
		if d.IsDir() {
			if treeRoot != nil && path != baseDir {
				node := &treeNode{
					name:     d.Name(),
					path:     path,
					isDir:    true,
					children: []*treeNode{},
				}
				nodeMap[path] = node
			}
			return nil
		}

		if !isTextFile(path) {
			return nil
		}

		// check if file matches patterns
		if len(globs) > 0 && !matchesAny(relPath, globs) {
			return nil
		}

		displayPath := filepath.Join(parentDir, relPath)

		// add file node to tree (if tree building is enabled)
		if treeRoot != nil {
			fileNode := &treeNode{
				name:  d.Name(),
				path:  path,
				isDir: false,
			}

			parentPath := filepath.Dir(path)
			if parentNode, exists := nodeMap[parentPath]; exists {
				parentNode.children = append(parentNode.children, fileNode)
			}
		}

		if listOnly {
			*pathList = append(*pathList, displayPath)
		} else {
			output, err := dumpFile(path, displayPath, filter)
			if err == nil {
				*outputs = append(*outputs, output)
			}
		}

		return nil
	})
	if err != nil {
		return err
	}

	if treeRoot != nil {
		// add directory nodes to their parents
		for path, node := range nodeMap {
			if path == baseDir {
				continue
			}
			parentPath := filepath.Dir(path)
			if parentNode, exists := nodeMap[parentPath]; exists {
				if node.isDir {
					parentNode.children = append(parentNode.children, node)
				}
			}
		}
	}

	return nil
}

func fetchURLsConcurrently(
	urls []string, apiKey string, liveCrawl bool, timeoutSec int,
	wg *sync.WaitGroup, results chan *Dumped,
) {
	if len(urls) == 0 {
		return
	}

	const maxConcurrency = 3
	const rateLimitDelay = 350 * time.Millisecond // ~3 requests per second (350ms * 3 = ~1050ms)

	urlsChan := make(chan string, len(urls))

	// Send URLs to channel
	for _, url := range urls {
		urlsChan <- url
	}
	close(urlsChan)

	// start worker goroutines
	for i := range maxConcurrency {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()
			for url := range urlsChan {
				// rate limiting: stagger requests
				time.Sleep(time.Duration(workerID) * rateLimitDelay / maxConcurrency)

				result, err := fetchURLContent(url, apiKey, liveCrawl, timeoutSec)
				if err != nil {
					fmt.Fprintf(os.Stderr, "error fetching URL %s: %v\n", url, err)
					continue
				}
				results <- result

				// Add delay between requests from same worker
				time.Sleep(rateLimitDelay)
			}
		}(i)
	}
}

func fetchURLContent(targetURL string, apiKey string, liveCrawl bool, timeoutSec int) (*Dumped, error) {
	u, err := url.ParseRequestURI(targetURL)
	if err != nil {
		return nil, fmt.Errorf("invalid URL: %w", err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return nil, fmt.Errorf("URL must use HTTP or HTTPS scheme")
	}

	reqBody := ExaRequest{
		URLs:      []string{targetURL},
		Text:      true,
		Context:   true,
		Livecrawl: "fallback",
	}
	if liveCrawl {
		reqBody.Livecrawl = "always"
	}

	jsonData, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	req, err := http.NewRequest("POST", exaBaseURL, bytes.NewBuffer(jsonData))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-api-key", apiKey)

	client := &http.Client{Timeout: time.Duration(timeoutSec) * time.Second}
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

	if len(strings.TrimSpace(exaResp.Context)) == 0 {
		return nil, fmt.Errorf("no context field in response")
	}

	return &Dumped{
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

func formatTreeNode(node *treeNode, prefix string, isLast bool) string {
	var result strings.Builder

	if node.name != "." {
		if isLast {
			result.WriteString(prefix + "└── " + node.name + "\n")
		} else {
			result.WriteString(prefix + "├── " + node.name + "\n")
		}
	}

	for i, child := range node.children {
		childIsLast := i == len(node.children)-1
		var childPrefix string
		if node.name == "." {
			childPrefix = prefix
		} else if isLast {
			childPrefix = prefix + "    "
		} else {
			childPrefix = prefix + "│   "
		}
		result.WriteString(formatTreeNode(child, childPrefix, childIsLast))
	}

	return result.String()
}

func formatTreeOutput(tree *treeNode, format string) string {
	treeStr := formatTreeNode(tree, "", true)
	switch format {
	case "md":
		return fmt.Sprintf("```tree\n%s```\n\n", treeStr)
	default:
		return fmt.Sprintf("<tree path='%s'>\n%s</tree>\n", tree.path, treeStr)
	}
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
	flag.BoolVar(&liveCrawl, "live", false, "fetch the most recent content from the URL")
	flag.IntVar(&timeoutSec, "timeout", 15, "timeout for fetching URL content")
	flag.StringVar(&filterRgx, "f", "", "skip lines matching this regex")
	flag.StringVar(&filterRgx, "filter", "", "skip lines matching this regex")
	flag.StringVar(&outfmt, "o", "xml", "output format: xml or md")
	flag.StringVar(&outfmt, "out-fmt", "xml", "output format: xml or md")
	flag.StringVar(&xmlTag, "xml-tag", "file", "custom XML tag name (only for xml output)")
	flag.BoolVar(&listOnly, "l", false, "list file paths only")
	flag.BoolVar(&listOnly, "list", false, "list file paths only")
	flag.BoolVar(&treeFlag, "t", false, "show directory tree")
	flag.BoolVar(&treeFlag, "tree", false, "show directory tree")
	flag.BoolVar(&helpFlag, "h", false, "display help message")
	flag.BoolVar(&helpFlag, "help", false, "display help message")

	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, `usage: dump [options] [directories...]

  recursively dumps text files from specified directories,
  respecting .gitignore and custom ignore rules.
  can also fetch content from URLs via Exa API.

  if no content sources are specified (directories or URLs), defaults to current directory.

options:
  -d|--dir <value>       directory to scan (can be repeated)
  -g|--glob <value>      glob pattern to match (can be repeated)
  -f|--filter <string>   skip lines matching this regex
  -h|--help              display help message
  -i|--ignore <value>    glob pattern to ignore (can be repeated)
  -o|--out-fmt <string>  xml or md (default "xml")
  -l|--list              list file paths only
  -t|--tree              show directory tree
  -u|--url <value>       URL to fetch content from via Exa API (can be repeated)

  --xml-tag <string>     custom XML tag name (only for xml output) (default "file")
  --timeout <int>        timeout for fetching URL content (default=15)
  --live                 fetch the most recent content from URL (sets 'livecrawl'='always', default='fallback')
                         see https://docs.exa.ai/reference/get-contents for more details

environment variables:
  EXA_API_KEY            required for URL fetching via Exa API

examples:
  dump                   dumps current directory (default behavior)
  dump -u https://...    dumps only specified URL
  dump -d src -u https://...  dumps src directory and URL
`)
	}
}

func main() {
	flag.Parse()

	if helpFlag {
		flag.Usage()
		os.Exit(0)
	}

	if outfmt != "xml" && outfmt != "md" {
		fmt.Fprintf(os.Stderr, "invalid output format %q (must be xml or md)\n", outfmt)
		os.Exit(1)
	}

	// process urls concurrently
	urlResults := make(chan *Dumped, len(urls))
	urlWg := sync.WaitGroup{}
	if len(urls) > 0 && !listOnly {
		apiKey := os.Getenv("EXA_API_KEY")
		if apiKey == "" {
			fmt.Fprintf(os.Stderr, "EXA_API_KEY environment variable is required for URL fetching\n")
			os.Exit(1)
		}
		fetchURLsConcurrently(urls, apiKey, liveCrawl, timeoutSec, &urlWg, urlResults)
	}
	go func() {
		// close channel when all urls are processed
		urlWg.Wait()
		close(urlResults)
	}()

	allDirs := append([]string{}, dirs...)
	if len(allDirs) == 0 && len(urls) == 0 {
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

	// process directories concurrently
	fileResults := make(chan directoryResult, len(allDirs))
	var fileWg sync.WaitGroup

	for _, dir := range allDirs {
		fileWg.Add(1)
		go func(dir string) {
			defer fileWg.Done()

			absDir, err := filepath.Abs(dir)
			if err != nil {
				fmt.Fprintf(os.Stderr, "failed to resolve directory %q: %v\n", dir, err)
				return
			}

			gitIgnore, err := buildIgnoreList(absDir, ignoreValues)
			if err != nil {
				fmt.Fprintf(os.Stderr, "failed to build ignore list for %q: %v\n", dir, err)
				return
			}

			// directory-specific outputs
			var dirOutputs []*Dumped
			var dirPathList []string
			var dirTree *treeNode

			if treeFlag {
				dirTree = &treeNode{
					name:     filepath.Base(absDir),
					path:     absDir,
					isDir:    true,
					children: []*treeNode{},
				}
			}

			err = processDirectory(absDir, globs, gitIgnore, filter, &dirOutputs, &dirPathList, dirTree)
			if err != nil {
				fmt.Fprintf(os.Stderr, "failed to process directory %q: %v\n", dir, err)
				return
			}

			fileResults <- directoryResult{
				tree:     dirTree,
				outputs:  dirOutputs,
				pathList: dirPathList,
				dirName:  dir,
			}
		}(dir)
	}

	go func() {
		// close channel when all directories are processed
		fileWg.Wait()
		close(fileResults)
	}()

	// output results as they complete
	for result := range fileResults {
		if listOnly {
			for _, path := range result.pathList {
				fmt.Println(path)
			}
		} else {
			if treeFlag && result.tree != nil {
				fmt.Print(formatTreeOutput(result.tree, outfmt))
			}
			for _, output := range result.outputs {
				fmt.Print(formatOutput(*output, outfmt, xmlTag))
			}
		}
	}
	for result := range urlResults {
		fmt.Print(formatOutput(*result, outfmt, xmlTag))
	}
}
