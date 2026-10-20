package git

import (
	"crypto/sha256"
	"math/big"
	"os"
	"path/filepath"
	"strings"

	"github.com/go-git/go-git/v5/config"
)

// Configuration for slug names/hashes.
// Names and hashes are shortened to fit the limits (image name/tag: 255/128, sandbox name: 128).
const (
	// Docker requires lowercase letters, therefore base36 was chosen to achieve max entropy per hash length.
	base = 36
	// Length of 14 -> ~72 bits entropy, collision probability is below 1e-9 for millions of inputs. Enough for naming.
	hashIDLen        = 14
	maxFolderNameLen = 20
)

func sanitizeFolderName(name string) string {
	name = strings.ToLower(name)
	fields := strings.FieldsFunc(name, func(r rune) bool {
		return r < '0' || (r > '9' && r < 'a') || r > 'z'
	})
	s := strings.Join(fields, "-")
	if len(s) > maxFolderNameLen {
		s = s[:maxFolderNameLen]
	}
	return strings.Trim(s, "-")
}

func HashID(input string) string {
	sum := sha256.Sum256([]byte(input))
	num := new(big.Int).SetBytes(sum[:])
	encoded := num.Text(base)
	if len(encoded) > hashIDLen {
		encoded = encoded[:hashIDLen]
	}
	return encoded
}

// ProjectSlug returns the repo slug of the current working directory; see
// RepoSlug.
//
// TODO(#144): replaced by ResolveIdentity; deleted by the cmd wiring task.
func ProjectSlug() string {
	return RepoSlug()
}

// originURL returns the origin remote URL for the checkout rooted at root, or
// "" if there is none. For a normal checkout it reads the repo's own config;
// for a linked worktree (whose .git is a file pointing into a shared gitdir)
// go-git does not load the shared config, so the config file is parsed
// directly with go-git's config.ReadConfig.
func originURL(root string) string {
	gitDir := resolveGitDir(root)
	if gitDir == "" {
		return ""
	}
	cfg, err := readRepoConfig(gitDir)
	if err != nil {
		return ""
	}
	if remote, ok := cfg.Remotes["origin"]; ok && len(remote.URLs) > 0 {
		return remote.URLs[0]
	}
	return ""
}

// readRepoConfig parses the config of the repository whose shared git directory
// is gitDir. go-git does not load a linked worktree's shared config through the
// repository object, so the config file is read directly. Callers treat a
// missing or unparseable config as "unknown" rather than failing.
func readRepoConfig(gitDir string) (*config.Config, error) {
	f, err := os.Open(filepath.Join(gitDir, "config"))
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return config.ReadConfig(f)
}

// isBare reports whether gitDir (a repository's shared git directory) is a bare
// repository, read from its config's core.bare. A missing or unreadable config
// means "not bare": that is today's behavior for an unrecognized repo.
func isBare(gitDir string) bool {
	cfg, err := readRepoConfig(gitDir)
	if err != nil {
		return false
	}
	return cfg.Core.IsBare
}

// resolveGitDir returns the absolute git directory for the checkout rooted at
// root. For a normal checkout that is root/.git; for a linked worktree, whose
// root/.git is a file containing "gitdir: <shared>.git/worktrees/<name>", it
// returns the shared <shared>.git directory. It returns "" if the checkout is
// not a repo.
func resolveGitDir(root string) string {
	path := filepath.Join(root, ".git")
	if info, err := os.Stat(path); err == nil && info.IsDir() {
		return path
	}
	dir, ok := gitdirFile(root)
	if !ok {
		return ""
	}
	// Strip a worktree registration before making the path absolute: the raw
	// text still carries its ancestors when git wrote an absolute gitdir, and a
	// "worktrees" component there is not a registration (see
	// worktreeRegistration).
	if shared, ok := worktreeRegistration(dir); ok {
		dir = shared
	}
	if !filepath.IsAbs(dir) {
		dir = filepath.Join(root, dir)
	}
	return dir
}

// isLinkedWorktree reports whether root is a linked worktree: its .git is a file
// recording a worktree registration. A --separate-git-dir checkout and a
// submodule also have a .git file, but their gitdir is not a registration, so
// they are not linked worktrees and are never treated as outside.
func isLinkedWorktree(root string) bool {
	dir, ok := gitdirFile(root)
	if !ok {
		return false
	}
	_, ok = worktreeRegistration(dir)
	return ok
}

// worktreeRegistration returns the shared git dir recorded in gitdir when gitdir
// is a linked-worktree registration, i.e. "<shared>/worktrees/<name>" with a
// single final segment; git never lets a worktree name contain "/". Requiring
// exactly one segment after the last "/worktrees/" keeps a "worktrees" component
// in an ancestor — or in the shared dir path itself — from being mistaken for a
// registration, which matters because an absolute gitdir carries its ancestors in
// the same text.
func worktreeRegistration(gitdir string) (string, bool) {
	shared, name, found := strings.CutLast(gitdir, "/worktrees/")
	if !found || name == "" || strings.Contains(name, "/") {
		return "", false
	}
	return shared, true
}

// gitdirFile returns the gitdir path recorded in root/.git when that entry is a
// file (a linked worktree, or a --separate-git-dir or submodule checkout),
// exactly as git wrote it: still relative when git recorded it relative
// (worktree.useRelativePaths). Callers classify and strip on that text before
// making it absolute, so a "worktrees" path component in an ancestor of root
// cannot masquerade as a worktree registration. It reports false when .git is a
// directory, missing, unreadable, or lacks the "gitdir:" prefix.
func gitdirFile(root string) (string, bool) {
	path := filepath.Join(root, ".git")
	info, err := os.Stat(path)
	if err != nil || info.IsDir() {
		return "", false
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "", false
	}
	dir, ok := strings.CutPrefix(strings.TrimSpace(string(data)), "gitdir:")
	if !ok {
		return "", false
	}
	return strings.TrimSpace(dir), true
}

// lastPathSegment returns the last path segment of a git remote URL, dropping
// a trailing ".git". It handles both scp-like SSH URLs (git@host:org/repo.git)
// and URLs with an explicit scheme (https://host/org/repo.git).
func lastPathSegment(url string) string {
	s := url
	if i := strings.Index(s, "://"); i >= 0 {
		s = s[i+3:]
	}
	if i := strings.Index(s, "@"); i >= 0 {
		s = s[i+1:]
	}
	if i := strings.LastIndex(s, "/"); i >= 0 {
		s = s[i+1:]
	}
	if i := strings.LastIndex(s, ":"); i >= 0 {
		s = s[i+1:]
	}
	return strings.TrimSuffix(s, ".git")
}

// projectSlug builds the slug from a sanitized folder name and an input for
// hashing. A sanitized folder name that is empty (e.g., the folder was all
// digits) falls back to "project" so the slug never starts with a bare dash.
func projectSlug(folderName, id string) string {
	if folderName == "" {
		folderName = "project"
	}
	return folderName + "-" + HashID(id)
}
