// Copyright 2019 The Gitea Authors. All rights reserved.
// Use of this source code is governed by a MIT-style
// license that can be found in the LICENSE file.

package code

import (
	"strconv"
	"strings"

	"code.gitea.io/gitea/models"
	"code.gitea.io/gitea/modules/git"
	"code.gitea.io/gitea/modules/log"
	"code.gitea.io/gitea/modules/setting"
)

type fileUpdate struct {
	Filename string
	BlobSha  string
}

// repoChanges changes (file additions/updates/removals) to a repo
type repoChanges struct {
	Updates          []fileUpdate
	RemovedFilenames []string
}

func getDefaultBranchSha(repo *models.Repository) (string, error) {
	stdout, err := git.NewCommand("show-ref", "-s", git.BranchPrefix+repo.DefaultBranch).RunInDir(repo.RepoPath())
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(stdout), nil
}

// getRepoChanges returns changes to repo since last indexer update
func getRepoChanges(repo *models.Repository, revision string) (*repoChanges, error) {
	if err := repo.GetIndexerStatus(); err != nil {
		return nil, err
	}

	if len(repo.IndexerStatus.CommitSha) == 0 {
		return genesisChanges(repo, revision)
	}
	return nonGenesisChanges(repo, revision)
}

func isIndexable(entry *git.TreeEntry) bool {
	if !entry.IsRegular() && !entry.IsExecutable() {
		return false
	}
	name := strings.ToLower(entry.Name())
	for _, g := range setting.Indexer.ExcludePatterns {
		if g.Match(name) {
			return false
		}
	}
	for _, g := range setting.Indexer.IncludePatterns {
		if g.Match(name) {
			return true
		}
	}
	return len(setting.Indexer.IncludePatterns) == 0
}

// parseGitLsTreeOutput parses the output of a `git ls-tree -r --full-name` command
func parseGitLsTreeOutput(stdout []byte) ([]fileUpdate, error) {
	entries, err := git.ParseTreeEntries(stdout)
	if err != nil {
		return nil, err
	}
	var idxCount = 0
	updates := make([]fileUpdate, len(entries))
	for _, entry := range entries {
		if isIndexable(entry) {
			updates[idxCount] = fileUpdate{
				Filename: entry.Name(),
				BlobSha:  entry.ID.String(),
			}
			idxCount++
		}
	}
	return updates[:idxCount], nil
}

// genesisChanges get changes to add repo to the indexer for the first time
func genesisChanges(repo *models.Repository, revision string) (*repoChanges, error) {
	var changes repoChanges
	stdout, err := git.NewCommand("ls-tree", "--full-tree", "-r", revision).
		RunInDirBytes(repo.RepoPath())
	if err != nil {
		return nil, err
	}
	changes.Updates, err = parseGitLsTreeOutput(stdout)
	return &changes, err
}

// nonGenesisChanges get changes since the previous indexer update
func nonGenesisChanges(repo *models.Repository, revision string) (*repoChanges, error) {
	diffCmd := git.NewCommand("diff", "--name-status",
		repo.IndexerStatus.CommitSha, revision)
	stdout, err := diffCmd.RunInDir(repo.RepoPath())
	if err != nil {
		// previous commit sha may have been removed by a force push, so
		// try rebuilding from scratch
		log.Warn("git diff: %v", err)
		if err = indexer.Delete(repo.ID); err != nil {
			return nil, err
		}
		return genesisChanges(repo, revision)
	}
	var changes repoChanges
	updatedFilenames := make([]string, 0, 10)
	for _, line := range strings.Split(stdout, "\n") {
		line = strings.TrimSpace(line)
		if len(line) == 0 {
			continue
		}
		filename := strings.TrimSpace(line[1:])
		if len(filename) == 0 {
			continue
		} else if filename[0] == '"' {
			filename, err = strconv.Unquote(filename)
			if err != nil {
				return nil, err
			}
		}

		switch status := line[0]; status {
		case 'M', 'A':
			updatedFilenames = append(updatedFilenames, filename)
		case 'D':
			changes.RemovedFilenames = append(changes.RemovedFilenames, filename)
		default:
			log.Warn("Unrecognized status: %c (line=%s)", status, line)
		}
	}

	cmd := git.NewCommand("ls-tree", "--full-tree", revision, "--")
	cmd.AddArguments(updatedFilenames...)
	lsTreeStdout, err := cmd.RunInDirBytes(repo.RepoPath())
	if err != nil {
		return nil, err
	}
	changes.Updates, err = parseGitLsTreeOutput(lsTreeStdout)
	return &changes, err
}
