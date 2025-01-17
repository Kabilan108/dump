#include <iostream>
#include <fstream>
#include <string>
#include <vector>
#include <algorithm>
#include <filesystem> // C++17
namespace fs = std::filesystem;

/**
 * Utility function: Split string by delimiter into tokens.
 */
static std::vector<std::string> split(const std::string &str, char delimiter) {
    std::vector<std::string> tokens;
    std::string token;
    for (char c : str) {
        if (c == delimiter) {
            if (!token.empty()) tokens.push_back(token);
            token.clear();
        } else {
            token.push_back(c);
        }
    }
    if (!token.empty()) tokens.push_back(token);
    return tokens;
}

/**
 * Simple wildcard matching function.
 * - Supports '*' (zero or more of any character).
 * - Does not support advanced .gitignore features like "**\/", "!", etc.
 *
 * This is enough to illustrate how you might filter out files. 
 * For full .gitignore support, consider using a dedicated .gitignore parser.
 */
static bool wildcardMatch(const std::string &str, const std::string &pattern) {
    // If pattern is empty, str must be empty
    if (pattern.empty()) return str.empty();

    // dp[i][j] = does str up to i match pattern up to j?
    std::vector<std::vector<bool>> dp(str.size() + 1, std::vector<bool>(pattern.size() + 1, false));

    // Both empty -> match
    dp[0][0] = true;

    // Handle patterns like "*" or multiple '*' in a row
    for (size_t j = 1; j <= pattern.size(); ++j) {
        if (pattern[j - 1] == '*') {
            dp[0][j] = dp[0][j - 1];
        }
    }

    for (size_t i = 1; i <= str.size(); ++i) {
        for (size_t j = 1; j <= pattern.size(); ++j) {
            if (pattern[j - 1] == '*') {
                // '*' can match zero or more characters
                dp[i][j] = dp[i][j - 1] || dp[i - 1][j];
            } else if (pattern[j - 1] == '?' || pattern[j - 1] == str[i - 1]) {
                // '?' matches a single character, or exact char match
                dp[i][j] = dp[i - 1][j - 1];
            } else {
                // no match
                dp[i][j] = false;
            }
        }
    }

    return dp[str.size()][pattern.size()];
}

/**
 * Return true if 'filename' matches any pattern in 'patterns'.
 */
static bool matchesAnyPattern(const std::string &filename, const std::vector<std::string> &patterns) {
    for (const auto &pat : patterns) {
        if (wildcardMatch(filename, pat)) {
            return true;
        }
    }
    return false;
}

/**
 * Heuristic to determine if a file is "text" by scanning some bytes.
 * Returns true if the file is likely text, false otherwise.
 */
static bool isTextFile(const fs::path &filePath) {
    // We'll read a small chunk of the file and check if we see non-printable control characters.
    std::ifstream fin(filePath, std::ios::binary);
    if (!fin.is_open()) return false;

    static const size_t CHUNK_SIZE = 1024;
    char buffer[CHUNK_SIZE];
    fin.read(buffer, CHUNK_SIZE);
    std::streamsize bytesRead = fin.gcount();

    // Simple check: if we find a '\0' or a suspicious set of characters, consider it binary.
    for (std::streamsize i = 0; i < bytesRead; ++i) {
        unsigned char c = static_cast<unsigned char>(buffer[i]);
        if (c == 0) {
            // Null byte -> likely binary
            return false;
        }
        // Heuristic: 
        // If too many non-ASCII or control chars, we might treat as binary.
        // Here we just do a minimal check.
        if (c < 9 || (c > 13 && c < 32)) {
            // Exclude common whitespace / newline / tab
            return false;
        }
    }
    return true;
}

/**
 * Load ignore patterns from a local .gitignore file in 'directory' if it exists.
 * Lines starting with '#' are comments; skip them.
 * Empty lines are skipped as well.
 */
static void loadGitignorePatterns(const fs::path &directory, std::vector<std::string> &patterns) {
    fs::path gitignorePath = directory / ".gitignore";
    if (!fs::exists(gitignorePath)) return;

    std::ifstream file(gitignorePath);
    if (!file.is_open()) return;

    std::string line;
    while (std::getline(file, line)) {
        // Trim
        if (line.empty() || line[0] == '#') continue;
        // Some .gitignore lines might have preceding slash, e.g. '/build'
        // For simplicity, strip leading '/' to unify matching approach
        if (!line.empty() && line[0] == '/') {
            line.erase(line.begin());
        }
        patterns.push_back(line);
    }
}

/**
 * Recursively iterate the given directory, ignoring:
 *   - anything matching the patterns
 *   - binary files
 * For text files, dumps content to stdout in the specified XML-like format.
 */
static void dumpDirectory(const fs::path &directory, const std::vector<std::string> &patterns) {
    for (auto &entry : fs::recursive_directory_iterator(directory)) {
        // Skip directories explicitly if they match patterns
        if (entry.is_directory()) {
            std::string dirName = entry.path().filename().string();
            if (matchesAnyPattern(dirName, patterns)) {
                // Skip directory entirely
                entry.disable_recursion_pending();
                continue;
            }
            continue; // Just continue recursion
        }

        // For files
        auto relPath = fs::relative(entry.path(), directory).string();
        if (matchesAnyPattern(relPath, patterns) || matchesAnyPattern(entry.path().filename().string(), patterns)) {
            // ignore
            continue;
        }

        // Check if text file
        if (!isTextFile(entry.path())) {
            continue;
        }

        // Output format:
        // <file path="{file_path}">
        // {file_content}
        // </file>
        std::ifstream fin(entry.path());
        if (!fin.is_open()) {
            continue; // skip unreadable
        }
        std::string content((std::istreambuf_iterator<char>(fin)), std::istreambuf_iterator<char>());

        std::cout << "<file path=\"" << relPath << "\">\n";
        std::cout << content << "\n";
        std::cout << "</file>\n\n";
    }
}

/**
 * Print usage information.
 */
static void printUsage(const std::string &programName) {
    std::cout << "Usage: " << programName << " [OPTIONS]\n"
              << "Recursively dump text files to stdout, respecting .gitignore and custom ignore patterns.\n\n"
              << "Options:\n"
              << "  -h, --help            Show this help message.\n"
              << "  -d, --dir <PATH>      Directory to run in (default: current working directory).\n"
              << "  -i, --ignore <PATTERN> Add pattern to ignore (e.g. -i *.sh)\n"
              << std::endl;
}

int main(int argc, char *argv[]) {
    fs::path targetDir = fs::current_path();
    std::vector<std::string> ignorePatterns;

    // Parse command line
    for (int i = 1; i < argc; ++i) {
        std::string arg = argv[i];
        if (arg == "-h" || arg == "--help") {
            printUsage(argv[0]);
            return 0;
        } else if ((arg == "-d" || arg == "--dir") && i + 1 < argc) {
            targetDir = fs::path(argv[++i]);
        } else if ((arg == "-i" || arg == "--ignore") && i + 1 < argc) {
            ignorePatterns.push_back(argv[++i]);
        } else {
            // Unrecognized arguments: 
            // For simplicity, we'll just ignore or break. 
            // Alternatively, show help or error.
            std::cerr << "Unrecognized option: " << arg << std::endl;
            printUsage(argv[0]);
            return 1;
        }
    }

    // Ensure directory exists
    if (!fs::exists(targetDir) || !fs::is_directory(targetDir)) {
        std::cerr << "Directory " << targetDir << " does not exist or is not a directory.\n";
        return 1;
    }

    // Load .gitignore patterns if present
    loadGitignorePatterns(targetDir, ignorePatterns);

    // Dump the directory
    dumpDirectory(targetDir, ignorePatterns);

    return 0;
}
