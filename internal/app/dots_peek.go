package app

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	udiff "github.com/aymanbagabas/go-udiff"

	"github.com/lkshrk/omni/internal/dots"
)

const DotsPeekReadLimit = 256 * 1024

type DotsPeekMode string

const (
	DotsPeekModeText     DotsPeekMode = "text"
	DotsPeekModeDiff     DotsPeekMode = "diff"
	DotsPeekModeMetadata DotsPeekMode = "metadata"
)

type DotsPeekSource string

const (
	DotsPeekSourceRepo  DotsPeekSource = "repo"
	DotsPeekSourceLocal DotsPeekSource = "local"
)

type DotsPeekRequest struct {
	Entry DotStatus
	Child *DotChild
}

type DotsPeekSide struct {
	Source        DotsPeekSource `json:"source"`
	Label         string         `json:"label"`
	Path          string         `json:"path"`
	Exists        bool           `json:"exists"`
	Size          int64          `json:"size,omitempty"`
	Binary        bool           `json:"binary,omitempty"`
	Truncated     bool           `json:"truncated,omitempty"`
	SymlinkTarget string         `json:"symlink_target,omitempty"`
	Notice        string         `json:"notice,omitempty"`
}

type DotsPeekResult struct {
	Title     string         `json:"title"`
	Name      string         `json:"name"`
	Mode      DotsPeekMode   `json:"mode"`
	Source    DotsPeekSource `json:"source,omitempty"`
	Path      string         `json:"path,omitempty"`
	Repo      DotsPeekSide   `json:"repo"`
	Local     DotsPeekSide   `json:"local"`
	Content   string         `json:"content,omitempty"`
	Notice    string         `json:"notice,omitempty"`
	Truncated bool           `json:"truncated,omitempty"`
}

type dotsPeekTarget struct {
	name      string
	repoPath  string
	localPath string
	repoRoot  string
	isDir     bool
}

type dotsPeekRead struct {
	side    DotsPeekSide
	content string
	text    bool
}

func (a *App) DotsPeek(ctx context.Context, req DotsPeekRequest) (DotsPeekResult, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return DotsPeekResult{}, err
	}
	target, err := dotsPeekTargetForRequest(req)
	if err != nil {
		return DotsPeekResult{}, err
	}
	result := DotsPeekResult{
		Title: target.name,
		Name:  target.name,
		Repo:  dotsPeekSide(DotsPeekSourceRepo, target.repoPath),
		Local: dotsPeekSide(DotsPeekSourceLocal, target.localPath),
	}
	if target.isDir {
		return result, fmt.Errorf("dots peek: %s is a directory", target.name)
	}

	repo := readDotsPeekPath(target.repoPath, DotsPeekSourceRepo, target.repoRoot)
	local := readDotsPeekPath(target.localPath, DotsPeekSourceLocal, "")
	result.Repo = repo.side
	result.Local = local.side

	if repo.text && local.text {
		if repo.side.Truncated || local.side.Truncated {
			result.Mode = DotsPeekModeMetadata
			result.Notice = "Files are too large to diff; showing metadata only."
			return result, nil
		}
		if repo.content != local.content {
			result.Mode = DotsPeekModeDiff
			result.Content = udiff.Unified(dotsPeekDiffLabel(repo.side), dotsPeekDiffLabel(local.side), repo.content, local.content)
			return result, nil
		}
		result.Mode = DotsPeekModeText
		result.Source = DotsPeekSourceRepo
		result.Path = repo.side.Path
		result.Content = repo.content
		return result, nil
	}

	if local.text {
		result.Mode = DotsPeekModeText
		result.Source = DotsPeekSourceLocal
		result.Path = local.side.Path
		result.Content = local.content
		result.Truncated = local.side.Truncated
		result.Notice = local.side.Notice
		return result, nil
	}
	if repo.text {
		result.Mode = DotsPeekModeText
		result.Source = DotsPeekSourceRepo
		result.Path = repo.side.Path
		result.Content = repo.content
		result.Truncated = repo.side.Truncated
		result.Notice = repo.side.Notice
		return result, nil
	}

	result.Mode = DotsPeekModeMetadata
	result.Notice = dotsPeekMetadataNotice(repo.side, local.side)
	return result, nil
}

func dotsPeekTargetForRequest(req DotsPeekRequest) (dotsPeekTarget, error) {
	entry := req.Entry
	name := entry.Name
	repoPath := entry.SourcePath
	localPath := entry.TargetPath
	repoRoot := dotsPeekRepoRoot(entry.SourcePath, entry.IsDir)
	isDir := entry.IsDir

	if req.Child != nil {
		child := *req.Child
		name = child.Name
		if child.RelPath != "" {
			name = child.RelPath
		}
		if child.RelPath != "" && entry.SourcePath != "" {
			repoPath = filepath.Join(entry.SourcePath, filepath.FromSlash(child.RelPath))
		} else {
			repoPath = ""
		}
		localPath = child.Path
		if localPath == "" && child.RelPath != "" && entry.TargetPath != "" {
			localPath = filepath.Join(entry.TargetPath, filepath.FromSlash(child.RelPath))
		}
		repoRoot = dotsPeekRepoRoot(entry.SourcePath, true)
		isDir = child.IsDir
	}
	if name == "" {
		name = filepath.Base(localPath)
		if name == "." || name == string(filepath.Separator) || name == "" {
			name = filepath.Base(repoPath)
		}
	}
	if repoPath == "" && localPath == "" {
		return dotsPeekTarget{}, errors.New("dots peek: missing repo and local paths")
	}
	var err error
	repoPath, err = expandDotsPeekPath(repoPath)
	if err != nil {
		return dotsPeekTarget{}, err
	}
	localPath, err = expandDotsPeekPath(localPath)
	if err != nil {
		return dotsPeekTarget{}, err
	}
	repoRoot, err = expandDotsPeekPath(repoRoot)
	if err != nil {
		return dotsPeekTarget{}, err
	}
	return dotsPeekTarget{name: name, repoPath: repoPath, localPath: localPath, repoRoot: repoRoot, isDir: isDir}, nil
}

func dotsPeekRepoRoot(sourcePath string, isDir bool) string {
	if sourcePath == "" {
		return ""
	}
	if isDir {
		return sourcePath
	}
	return filepath.Dir(sourcePath)
}

func expandDotsPeekPath(path string) (string, error) {
	if path == "" {
		return "", nil
	}
	return dots.ExpandTilde(path)
}

func dotsPeekSide(source DotsPeekSource, path string) DotsPeekSide {
	return DotsPeekSide{
		Source: source,
		Label:  string(source),
		Path:   path,
	}
}

func readDotsPeekPath(path string, source DotsPeekSource, repoRoot string) dotsPeekRead {
	side := dotsPeekSide(source, path)
	if path == "" {
		side.Notice = "path not configured"
		return dotsPeekRead{side: side}
	}

	info, err := os.Lstat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return dotsPeekRead{side: side}
		}
		side.Notice = err.Error()
		return dotsPeekRead{side: side}
	}
	side.Exists = true
	readPath := path
	if info.Mode()&os.ModeSymlink != 0 {
		target, err := os.Readlink(path)
		if err != nil {
			side.Notice = err.Error()
			return dotsPeekRead{side: side}
		}
		side.SymlinkTarget = target
		if source == DotsPeekSourceRepo {
			resolved, err := filepath.EvalSymlinks(path)
			if err != nil {
				side.Notice = err.Error()
				return dotsPeekRead{side: side}
			}
			if !dotsPeekPathInside(repoRoot, resolved) {
				side.Notice = "repo symlink target is outside the dotfiles repo"
				return dotsPeekRead{side: side}
			}
			readPath = resolved
		}
	}

	stat, err := os.Stat(readPath)
	if err != nil {
		side.Notice = err.Error()
		return dotsPeekRead{side: side}
	}
	side.Size = stat.Size()
	if stat.IsDir() {
		side.Notice = "directory"
		return dotsPeekRead{side: side}
	}

	content, truncated, err := readDotsPeekContent(readPath)
	if err != nil {
		side.Notice = err.Error()
		return dotsPeekRead{side: side}
	}
	side.Truncated = truncated
	if bytes.IndexByte(content, 0) >= 0 {
		side.Binary = true
		side.Notice = "binary content"
		return dotsPeekRead{side: side}
	}
	text := string(content)
	text = strings.ToValidUTF8(text, "?")
	if truncated {
		side.Notice = fmt.Sprintf("truncated to %d KiB", DotsPeekReadLimit/1024)
	}
	return dotsPeekRead{side: side, content: text, text: true}
}

func readDotsPeekContent(path string) ([]byte, bool, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, false, err
	}
	defer func() { _ = f.Close() }()

	var buf bytes.Buffer
	_, err = io.CopyN(&buf, f, DotsPeekReadLimit+1)
	if err != nil && !errors.Is(err, io.EOF) {
		return nil, false, err
	}
	data := buf.Bytes()
	if len(data) > DotsPeekReadLimit {
		return data[:DotsPeekReadLimit], true, nil
	}
	return data, false, nil
}

func dotsPeekPathInside(root, path string) bool {
	if root == "" {
		return false
	}
	root = filepath.Clean(root)
	path = filepath.Clean(path)
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return false
	}
	return rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)))
}

func dotsPeekDiffLabel(side DotsPeekSide) string {
	if side.Path == "" {
		return side.Label
	}
	return side.Label + "\t" + side.Path
}

func dotsPeekMetadataNotice(repo, local DotsPeekSide) string {
	switch {
	case repo.Binary || local.Binary:
		return "Binary content cannot be previewed."
	case repo.Truncated || local.Truncated:
		return "File is too large to preview."
	case repo.Notice != "":
		return repo.Label + ": " + repo.Notice
	case local.Notice != "":
		return local.Label + ": " + local.Notice
	case !repo.Exists && !local.Exists:
		return "No repo or local file exists."
	default:
		return "No readable text content is available."
	}
}
