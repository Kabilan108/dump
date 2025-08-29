package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"net/url"
	"os"
	"os/exec"
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
	dirs          arrayFlags
	patterns      arrayFlags
	filterRgx     string
	ignoreValues  arrayFlags
	urls          arrayFlags
	liveCrawl     bool
	timeoutSec    int
	outfmt        string
	fileTag       string
	listOnly      bool
	treeFlag      bool
	helpFlag      bool
	tmuxSelectors arrayFlags
	tmuxLines     int
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

type Item struct {
	path    string
	content string
}

type TmuxPaneItem struct {
	id      string
	session string
	window  string
	pane    string
	content string
}

type TreeNode struct {
	name     string
	path     string
	isDir    bool
	children []*TreeNode
}

type DirectoryOutput struct {
	tree     *TreeNode
	items    []*Item
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

func formatItem(item Item, format string, tag string) string {
	switch format {
	case "md":
		return fmt.Sprintf("```%s\n%s```\n", item.path, item.content)
	default:
		if strings.HasPrefix(item.path, "http://") || strings.HasPrefix(item.path, "https://") {
			return fmt.Sprintf("<web url='%s'>\n%s</web>\n", item.path, item.content)
		}
		return fmt.Sprintf("<%s path='%s'>\n%s</%s>\n", tag, item.path, item.content, tag)
	}
}

func formatTmuxItem(item TmuxPaneItem, format string) string {
	switch format {
	case "md":
		header := fmt.Sprintf("# tmux-pane: id='%s' session='%s' window='%s' pane='%s'\n\n",
			item.id, item.session, item.window, item.pane)
		return fmt.Sprintf("```shell\n%s%s```\n", header, item.content)
	default:
		return fmt.Sprintf("<tmux_pane id='%s' session='%s' window='%s' pane='%s'>\n%s</tmux_pane>\n",
			item.id, item.session, item.window, item.pane, item.content)
	}
}

// filterContent applies the line filter regex to a block of text.
func filterContent(r io.Reader, filter *regexp.Regexp) (string, error) {
	var buf bytes.Buffer
	scanner := bufio.NewScanner(r)
	// Increase max token size to handle very long lines (default ~64KB)
	const maxLineSize = 10 * 1024 * 1024 // 10 MiB
	scanner.Buffer(make([]byte, 1024), maxLineSize)
	for scanner.Scan() {
		line := scanner.Text()
		if filter != nil && filter.MatchString(line) {
			continue
		}
		buf.WriteString(line)
		buf.WriteByte('\n')
	}
	if err := scanner.Err(); err != nil {
		return "", err
	}
	return buf.String(), nil
}

func dumpFile(path, displayPath string, filter *regexp.Regexp) (*Item, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	var content string
	if filter != nil {
		content, err = filterContent(file, filter)
		if err != nil {
			return nil, err
		}
	} else {
		cb, err := io.ReadAll(file)
		if err != nil {
			return nil, err
		}
		content = string(cb)
	}

	return &Item{
		path:    displayPath,
		content: content,
	}, nil
}

func processDirectory(
	baseDir string, globs []glob.Glob, gitIgnore *ignore.GitIgnore, filter *regexp.Regexp,
	items *[]*Item, pathList *[]string, treeRoot *TreeNode,
) error {
	parentDir := filepath.Base(baseDir)

	var nodeMap map[string]*TreeNode
	if treeRoot != nil {
		nodeMap = make(map[string]*TreeNode)
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
				node := &TreeNode{
					name:     d.Name(),
					path:     path,
					isDir:    true,
					children: []*TreeNode{},
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
			fileNode := &TreeNode{
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
				*items = append(*items, output)
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
	wg *sync.WaitGroup, results chan *Item,
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
	for i := 0; i < maxConcurrency; i++ {
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

// runCmd runs a command and returns its trimmed stdout.
func runCmd(name string, args ...string) (string, error) {
	cmd := exec.Command(name, args...)
	var out bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &stderr
	err := cmd.Run()
	if err != nil {
		if stderr.Len() > 0 {
			return "", fmt.Errorf("%w: %s", err, strings.TrimSpace(stderr.String()))
		}
		return "", err
	}
	return strings.TrimRight(out.String(), "\n"), nil
}

// resolveTmuxSelectors resolves tmux selectors to a unique list of pane IDs (e.g., %1).
func resolveTmuxSelectors(selectors []string) ([]string, []error) {
	if _, err := exec.LookPath("tmux"); err != nil {
		return nil, []error{fmt.Errorf("tmux binary not found: %w", err)}
	}

	seen := make(map[string]struct{})
	var panes []string
	var errs []error

	for _, sel := range selectors {
		sel = strings.TrimSpace(sel)
		if sel == "" {
			continue
		}
		var out string
		var err error
		switch sel {
		case "current":
			out, err = runCmd("tmux", "display-message", "-p", "-F", "#{pane_id}")
		case "all":
			// list panes in the current window of the current session (no -a)
			out, err = runCmd("tmux", "list-panes", "-F", "#{pane_id}")
		default:
			// specific target (e.g., %1, 0.1, @uuid)
			out, err = runCmd("tmux", "display-message", "-p", "-t", sel, "-F", "#{pane_id}")
		}
		if err != nil {
			errs = append(errs, fmt.Errorf("failed to resolve selector %q: %v", sel, err))
			continue
		}
		// 'all' may return multiple lines
		ids := strings.Split(out, "\n")
		for _, id := range ids {
			id = strings.TrimSpace(id)
			if id == "" {
				continue
			}
			if _, ok := seen[id]; ok {
				continue
			}
			seen[id] = struct{}{}
			panes = append(panes, id)
		}
	}
	return panes, errs
}

// getPaneMetadata fetches session, window, and pane indexes for a pane id.
func getPaneMetadata(paneID string) (session, window, pane string, err error) {
	// Fetch session, window, and pane in a single call (tab-separated)
	out, err := runCmd("tmux", "display-message", "-p", "-t", paneID, "-F", "#{session_name}\t#{window_index}\t#{pane_index}")
	if err != nil {
		return "", "", "", err
	}
	parts := strings.Split(out, "\t")
	if len(parts) != 3 {
		return "", "", "", fmt.Errorf("unexpected tmux metadata format for %s: %q", paneID, out)
	}
	return parts[0], parts[1], parts[2], nil
}

// capturePaneContent captures the last N lines (or full history if N==0) from a pane.
func capturePaneContent(paneID string, lastLines int) (string, error) {
	args := []string{"capture-pane", "-pJ", "-t", paneID}
	if lastLines > 0 {
		args = append(args, "-S", fmt.Sprintf("-%d", lastLines))
	} else {
		// full available history
		args = append(args, "-S", "-", "-E", "-")
	}
	out, err := runCmd("tmux", args...)
	if err != nil {
		return "", err
	}
	return out + "\n", nil // normalize with trailing newline
}

// fetchTmuxConcurrently captures tmux panes via a worker pool and streams results.
func fetchTmuxConcurrently(selectors []string, lines int, filter *regexp.Regexp, wg *sync.WaitGroup, results chan *TmuxPaneItem) (int, []error) {
	panes, errs := resolveTmuxSelectors(selectors)
	if len(panes) == 0 {
		return 0, errs
	}

	jobs := make(chan string, len(panes))
	const maxConcurrency = 6
	workerCount := len(panes)
	if workerCount > maxConcurrency {
		workerCount = maxConcurrency
	}

	// enqueue jobs
	for _, id := range panes {
		jobs <- id
	}
	close(jobs)

	// start workers
	for i := 0; i < workerCount; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for id := range jobs {
				sess, win, pn, err := getPaneMetadata(id)
				if err != nil {
					fmt.Fprintf(os.Stderr, "error getting tmux metadata for %s: %v\n", id, err)
					continue
				}
				content, err := capturePaneContent(id, lines)
				if err != nil {
					fmt.Fprintf(os.Stderr, "error capturing tmux pane %s: %v\n", id, err)
					continue
				}
				if filter != nil {
					content, err = filterContent(strings.NewReader(content), filter)
					if err != nil {
						fmt.Fprintf(os.Stderr, "error filtering tmux pane %s: %v\n", id, err)
						continue
					}
				}
				results <- &TmuxPaneItem{
					id:      id,
					session: sess,
					window:  win,
					pane:    pn,
					content: content,
				}
			}
		}()
	}

	return len(panes), errs
}

func fetchURLContent(targetURL string, apiKey string, liveCrawl bool, timeoutSec int) (*Item, error) {
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

	return &Item{
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

func formatTreeNode(node *TreeNode, prefix string, isLast bool) string {
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

func formatTreeOutput(tree *TreeNode, format string) string {
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
	flag.StringVar(&fileTag, "file-tag", "file", "custom XML tag name for files (only for xml output)")
	flag.BoolVar(&listOnly, "l", false, "list file paths only")
	flag.BoolVar(&listOnly, "list", false, "list file paths only")
	flag.BoolVar(&treeFlag, "t", false, "show directory tree")
	flag.BoolVar(&treeFlag, "tree", false, "show directory tree")
	flag.BoolVar(&helpFlag, "h", false, "display help message")
	flag.BoolVar(&helpFlag, "help", false, "display help message")

	// tmux flags
	flag.Var(&tmuxSelectors, "tmux", "tmux selector: current|all|%<id>|<win>.<pane>|@<pane_id> (repeatable)")
	flag.IntVar(&tmuxLines, "tmux-lines", 500, "lines of history per tmux pane (default 500; 0 = full)")

	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, `usage: dump [options] [directories...]

  recursively dumps text files from specified directories,
  respecting .gitignore and custom ignore rules.
  can also fetch content from URLs via Exa API.

  if no content sources are specified (directories or URLs), defaults to current directory.

options:
  -d|--dir <value>       directory to scan (can be repeated)
  -g|--glob <value>      glob pattern to match files (can be repeated)
  -f|--filter <string>   skip lines matching this regex
  -h|--help              display help message
  -i|--ignore <value>    glob pattern to ignore files/dirs (can be repeated)
  -l|--list              list file paths only (no content)
  -o|--out-fmt <string>  output format: xml or md (default "xml")
  -t|--tree              show directory tree structure
  -u|--url <value>       URL to fetch content from via Exa API (can be repeated)

  --file-tag <string>    custom XML tag name for files (only for xml output) (default "file")
  --timeout <int>        timeout in seconds for URL fetching (default 15)
  --live                 force fresh content from URLs (livecrawl=always vs fallback)

  --tmux <selector>      capture tmux panes: current|all (current window)|%%<id>|<win>.<pane>|@<pane_id> (repeatable)
  --tmux-lines <int>     number of history lines per tmux pane (default 500; 0 = full)

environment variables:
  EXA_API_KEY            required for URL fetching via Exa API

examples:
  dump                          dumps current directory
  dump -d src -d docs           dumps src and docs directories
  dump -g "**.go" -g "**.md"    dumps only Go and Markdown files
  dump -l                       list file paths in the current directory
  dump -t                       dumps current directory and shows tree structure
  dump -u https://example.com   fetches and dumps URL content
  dump -d src -u https://...    dumps src directory and URL content
  dump -o md -f "^\s*#"         markdown format, skip comment lines

  dump --tmux current           dump the current tmux pane
  dump --tmux %%1 --tmux 0.1     dump specific tmux panes
  dump --tmux all --tmux-lines 0  dump all panes in current window with full history
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

	if tmuxLines < 0 {
		fmt.Fprintf(os.Stderr, "invalid --tmux-lines %d (must be >= 0)\n", tmuxLines)
		os.Exit(1)
	}

	// process urls concurrently
	urlItems := make(chan *Item, len(urls))
	urlWg := sync.WaitGroup{}
	if len(urls) > 0 && !listOnly {
		apiKey := os.Getenv("EXA_API_KEY")
		if apiKey == "" {
			fmt.Fprintf(os.Stderr, "EXA_API_KEY environment variable is required for URL fetching\n")
			os.Exit(1)
		}
		fetchURLsConcurrently(urls, apiKey, liveCrawl, timeoutSec, &urlWg, urlItems)
	}
	go func() {
		// close channel when all urls are processed
		urlWg.Wait()
		close(urlItems)
	}()

	// process tmux panes concurrently (if requested)
	tmuxItems := make(chan *TmuxPaneItem, 8)
	tmuxWg := sync.WaitGroup{}
	var tmuxPaneCount int
	var tmuxResolveErrs []error
	// defer starting tmux capture until filter (if any) is compiled below

	allDirs := append([]string{}, dirs...)
	if len(allDirs) == 0 && len(urls) == 0 && len(tmuxSelectors) == 0 {
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
	dirOutputs := make(chan DirectoryOutput, len(allDirs))
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
			var items []*Item
			var paths []string
			var dirTree *TreeNode

			if treeFlag && !listOnly {
				dirTree = &TreeNode{
					name:     filepath.Base(absDir),
					path:     absDir,
					isDir:    true,
					children: []*TreeNode{},
				}
			}

			err = processDirectory(absDir, globs, gitIgnore, filter, &items, &paths, dirTree)
			if err != nil {
				fmt.Fprintf(os.Stderr, "failed to process directory %q: %v\n", dir, err)
				return
			}

			dirOutputs <- DirectoryOutput{
				tree:     dirTree,
				items:    items,
				pathList: paths,
				dirName:  dir,
			}
		}(dir)
	}

	go func() {
		// close channel when all directories are processed
		fileWg.Wait()
		close(dirOutputs)
	}()

	for do := range dirOutputs {
		if listOnly {
			for _, path := range do.pathList {
				fmt.Println(path)
			}
		} else {
			if treeFlag && do.tree != nil {
				fmt.Print(formatTreeOutput(do.tree, outfmt))
			}
			for _, item := range do.items {
				fmt.Print(formatItem(*item, outfmt, fileTag))
			}
		}
	}

	// Now that filter is compiled, if tmux capture was requested, start it. Otherwise close channel.
	if len(tmuxSelectors) > 0 && !listOnly {
		// Re-invoke concurrent capture now that we have compiled filter
		// Note: tmuxWg and tmuxItems were initialized earlier; reuse them here
		tmuxPaneCount, tmuxResolveErrs = fetchTmuxConcurrently(tmuxSelectors, tmuxLines, filter, &tmuxWg, tmuxItems)
		// Always surface tmux resolution errors
		if len(tmuxResolveErrs) > 0 {
			for _, e := range tmuxResolveErrs {
				fmt.Fprintf(os.Stderr, "%v\n", e)
			}
		}
		go func() {
			tmuxWg.Wait()
			close(tmuxItems)
		}()
	} else {
		close(tmuxItems)
	}

	for tmi := range tmuxItems {
		fmt.Print(formatTmuxItem(*tmi, outfmt))
	}
	for result := range urlItems {
		fmt.Print(formatItem(*result, outfmt, fileTag))
	}

	// If tmux was the only requested source and it failed, exit non-zero
	if len(tmuxSelectors) > 0 && len(dirs) == 0 && len(urls) == 0 && !listOnly {
		if tmuxPaneCount == 0 {
			os.Exit(1)
		}
	}
}
