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
	"strings"
	"unicode/utf8"

	"github.com/sabhiram/go-gitignore"
)

var (
	helpFlag     bool
	dirFlag      string
	ignoreValues arrayFlags // Custom type to accumulate multiple -i/--ignore patterns
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
	flag.StringVar(&dirFlag, "d", ".", "directory to scan")
	flag.StringVar(&dirFlag, "dir", ".", "directory to scan")
	flag.Var(&ignoreValues, "i", "pattern to ignore (can be repeated)")
	flag.Var(&ignoreValues, "ignore", "pattern to ignore (can be repeated)")
}

func usage() {
	fmt.Printf(`usage: dump [options]

description:
  the 'dump' tool recursively scans the specified directory (default: current),
  respects .gitignore (if present), applies any additional ignore patterns
  provided, and prints all text files to stdout.

options:
  -h, --help          display this help message and exit.
  -d, --dir <path>    specify directory to scan (default ".").
  -i, --ignore <pat>  add an ignore pattern (may be repeated).
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

  // build the ignore list from .gitignore + extra patterns
  gitIgnore, err := buildIgnoreList(dirFlag, ignoreValues)
  if err != nil {
    fmt.Fprintf(os.Stderr, "error building ignore list: %v\n", err)
    os.Exit(1)
  }

  // check if dirFlag is a directory
  info, err := os.Stat(dirFlag)
  if err != nil {
    fmt.Fprintf(os.Stderr, "error accessing directory '%s': %v\n", dirFlag, err)
    os.Exit(1)
  }
  if !info.IsDir() {
    fmt.Fprintf(os.Stderr, "%s is not a directory\n", dirFlag)
    os.Exit(1)
  }

  // walk the directory
  err = filepath.Walk(dirFlag, func(path string, info fs.FileInfo, err error) error {
    if err != nil {
      // skip if can't read
      return nil
    }
    // skip if a dir
    if info.IsDir() {
      return nil
    }
    // convert path to relative path (to dirFlag)
    relPath, relErr := filepath.Rel(dirFlag, path)
    if relErr != nil {
      // fallback to abs path
      relPath = path
    }
    // check if file is ignored
    if gitIgnore.MatchesPath(relPath) {
      return nil
    }
    if !isTextFile(path) {
      return nil
    }
    // print file contents
    err = dumpFile(path, relPath)
    if err != nil {
      fmt.Fprintf(os.Stderr, "error dumping file '%s': %v\n", relPath, err)
    }
    
    return nil
  })

  if err != nil {
    fmt.Fprintf(os.Stderr, "Error walking directory: %v\n", err)
    os.Exit(1)
  }
}

// print file contents as:
// <file path="{file_path}">
// {file_contents}
// </file>
func dumpFile(absolutePath, relativePath string) error {
  file, err := os.Open(absolutePath)
  if err != nil {
    return err
  }
  defer file.Close()

  fmt.Printf("<file path=\"%s\">\n", relativePath)

  scanner := bufio.NewScanner(file)
  for scanner.Scan() {
    fmt.Println(scanner.Text())
  }
  if err := scanner.Err(); err != nil {
    return errors.New("error reading file content")
  }

  fmt.Println("</file>")
  fmt.Println()
  return nil
}
