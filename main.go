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
	dirs         arrayFlags
	patterns     arrayFlags
	filterRgx    string
	ignoreValues arrayFlags
	outfmt       string
	xmlTag       string
	listOnly     bool
	treeFlag     bool
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

type treeNode struct {
	name     string
	path     string
	isDir    bool
	children []*treeNode
}

func formatOutput(output fileOutput, format string, tag string) string {
	switch format {
	case "md":
		return fmt.Sprintf("```%s\n%s```\n", output.path, output.content)
	default:
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

func writeContents(w io.Writer, contents []string) error {
	for _, c := range contents {
		// treat snippet as raw text NOT a format string (Fprintf)
		if _, err := io.WriteString(w, c); err != nil {
			return err
		}
	}
	return nil
}

func buildTree(baseDir string, globs []glob.Glob, gitIgnore *ignore.GitIgnore) (*treeNode, error) {
	root := &treeNode{
		name:     filepath.Base(baseDir),
		path:     baseDir,
		isDir:    true,
		children: []*treeNode{},
	}

	nodeMap := make(map[string]*treeNode)
	nodeMap[baseDir] = root

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

		if path == baseDir {
			return nil
		}

		if d.IsDir() {
			node := &treeNode{
				name:     d.Name(),
				path:     path,
				isDir:    true,
				children: []*treeNode{},
			}
			nodeMap[path] = node
			return nil
		}

		if !isTextFile(path) {
			return nil
		}

		if len(globs) > 0 && !matchesAny(relPath, globs) {
			return nil
		}

		node := &treeNode{
			name:  d.Name(),
			path:  path,
			isDir: false,
		}

		parentPath := filepath.Dir(path)
		if parentNode, exists := nodeMap[parentPath]; exists {
			parentNode.children = append(parentNode.children, node)
		}

		return nil
	})

	if err != nil {
		return nil, err
	}

	// Add directories to their parents and remove empty directories
	for path, node := range nodeMap {
		if path == baseDir {
			continue
		}
		parentPath := filepath.Dir(path)
		if parentNode, exists := nodeMap[parentPath]; exists {
			if node.isDir && len(node.children) > 0 {
				parentNode.children = append(parentNode.children, node)
			}
		}
	}

	return root, nil
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
		return fmt.Sprintf("<tree>\n%s</tree>\n\n", treeStr)
	}
}

func init() {
	flag.Var(&dirs, "d", "directory to scan (can be repeated)")
	flag.Var(&dirs, "dir", "directory to scan (can be repeated)")
	flag.Var(&patterns, "g", "glob pattern to match (can be repeated)")
	flag.Var(&patterns, "glob", "glob pattern to match (can be repeated)")
	flag.Var(&ignoreValues, "i", "glob pattern to ignore (can be repeated)")
	flag.Var(&ignoreValues, "ignore", "glob pattern to ignore (can be repeated)")
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

options:
  -d|--dir <value>       directory to scan (can be repeated)
  -g|--glob <value>      glob pattern to match (can be repeated)
  -f|--filter <string>   skip lines matching this regex
  -h|--help              display help message
  -i|--ignore <value>    glob pattern to ignore (can be repeated)
  -o|--out-fmt <string>  xml or md (default "xml")
  -l|--list              list file paths only
  -t|--tree              show directory tree
  --xml-tag <string>     custom XML tag name (only for xml output) (default "file")
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

	if treeFlag {
		for _, dir := range allDirs {
			absDir, err := filepath.Abs(dir)
			if err != nil {
				continue
			}

			gitIgnore, err := buildIgnoreList(absDir, ignoreValues)
			if err != nil {
				continue
			}

			tree, err := buildTree(absDir, globs, gitIgnore)
			if err == nil {
				fmt.Print(formatTreeOutput(tree, outfmt))
			}
		}
	}

	if listOnly {
		for _, path := range pathList {
			fmt.Println(path)
		}
	} else if !treeFlag {
		for _, output := range outputs {
			fmt.Print(formatOutput(*output, outfmt, xmlTag))
		}
	} else {
		// Show both tree and file contents
		for _, output := range outputs {
			fmt.Print(formatOutput(*output, outfmt, xmlTag))
		}
	}
}
