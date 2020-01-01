// Copyright 2019 The Gitea Authors.
// All rights reserved.
// Use of this source code is governed by a MIT-style
// license that can be found in the LICENSE file.

package pull

import (
	"bufio"
	"bytes"
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"code.gitea.io/gitea/models"
	"code.gitea.io/gitea/modules/cache"
	"code.gitea.io/gitea/modules/git"
	"code.gitea.io/gitea/modules/log"
	"code.gitea.io/gitea/modules/notification"
	"code.gitea.io/gitea/modules/references"
	"code.gitea.io/gitea/modules/setting"
	"code.gitea.io/gitea/modules/timeutil"
	issue_service "code.gitea.io/gitea/services/issue"

	"github.com/mcuadros/go-version"
)

// Merge merges pull request to base repository.
// FIXME: add repoWorkingPull make sure two merges does not happen at same time.
func Merge(pr *models.PullRequest, doer *models.User, baseGitRepo *git.Repository, mergeStyle models.MergeStyle, message string) (err error) {
	binVersion, err := git.BinVersion()
	if err != nil {
		log.Error("git.BinVersion: %v", err)
		return fmt.Errorf("Unable to get git version: %v", err)
	}

	if err = pr.GetHeadRepo(); err != nil {
		log.Error("GetHeadRepo: %v", err)
		return fmt.Errorf("GetHeadRepo: %v", err)
	} else if err = pr.GetBaseRepo(); err != nil {
		log.Error("GetBaseRepo: %v", err)
		return fmt.Errorf("GetBaseRepo: %v", err)
	}

	prUnit, err := pr.BaseRepo.GetUnit(models.UnitTypePullRequests)
	if err != nil {
		log.Error("pr.BaseRepo.GetUnit(models.UnitTypePullRequests): %v", err)
		return err
	}
	prConfig := prUnit.PullRequestsConfig()

	if err := pr.CheckUserAllowedToMerge(doer); err != nil {
		log.Error("CheckUserAllowedToMerge(%v): %v", doer, err)
		return fmt.Errorf("CheckUserAllowedToMerge: %v", err)
	}

	// Check if merge style is correct and allowed
	if !prConfig.IsMergeStyleAllowed(mergeStyle) {
		return models.ErrInvalidMergeStyle{ID: pr.BaseRepo.ID, Style: mergeStyle}
	}

	defer func() {
		go AddTestPullRequestTask(doer, pr.BaseRepo.ID, pr.BaseBranch, false)
	}()

	// Clone base repo.
	tmpBasePath, err := createTemporaryRepo(pr)
	if err != nil {
		log.Error("CreateTemporaryPath: %v", err)
		return err
	}
	defer func() {
		if err := models.RemoveTemporaryPath(tmpBasePath); err != nil {
			log.Error("Merge: RemoveTemporaryPath: %s", err)
		}
	}()

	baseBranch := "base"
	trackingBranch := "tracking"
	stagingBranch := "staging"

	var outbuf, errbuf strings.Builder

	// Enable sparse-checkout
	sparseCheckoutList, err := getDiffTree(tmpBasePath, baseBranch, trackingBranch)
	if err != nil {
		log.Error("getDiffTree(%s, %s, %s): %v", tmpBasePath, baseBranch, trackingBranch, err)
		return fmt.Errorf("getDiffTree: %v", err)
	}

	infoPath := filepath.Join(tmpBasePath, ".git", "info")
	if err := os.MkdirAll(infoPath, 0700); err != nil {
		log.Error("Unable to create .git/info in %s: %v", tmpBasePath, err)
		return fmt.Errorf("Unable to create .git/info in tmpBasePath: %v", err)
	}

	sparseCheckoutListPath := filepath.Join(infoPath, "sparse-checkout")
	if err := ioutil.WriteFile(sparseCheckoutListPath, []byte(sparseCheckoutList), 0600); err != nil {
		log.Error("Unable to write .git/info/sparse-checkout file in %s: %v", tmpBasePath, err)
		return fmt.Errorf("Unable to write .git/info/sparse-checkout file in tmpBasePath: %v", err)
	}

	var gitConfigCommand func() *git.Command
	if version.Compare(binVersion, "1.8.0", ">=") {
		gitConfigCommand = func() *git.Command {
			return git.NewCommand("config", "--local")
		}
	} else {
		gitConfigCommand = func() *git.Command {
			return git.NewCommand("config")
		}
	}

	// Switch off LFS process (set required, clean and smudge here also)
	if err := gitConfigCommand().AddArguments("filter.lfs.process", "").RunInDirPipeline(tmpBasePath, &outbuf, &errbuf); err != nil {
		log.Error("git config [filter.lfs.process -> <> ]: %v\n%s\n%s", err, outbuf.String(), errbuf.String())
		return fmt.Errorf("git config [filter.lfs.process -> <> ]: %v\n%s\n%s", err, outbuf.String(), errbuf.String())
	}
	outbuf.Reset()
	errbuf.Reset()

	if err := gitConfigCommand().AddArguments("filter.lfs.required", "false").RunInDirPipeline(tmpBasePath, &outbuf, &errbuf); err != nil {
		log.Error("git config [filter.lfs.required -> <false> ]: %v\n%s\n%s", err, outbuf.String(), errbuf.String())
		return fmt.Errorf("git config [filter.lfs.required -> <false> ]: %v\n%s\n%s", err, outbuf.String(), errbuf.String())
	}
	outbuf.Reset()
	errbuf.Reset()

	if err := gitConfigCommand().AddArguments("filter.lfs.clean", "").RunInDirPipeline(tmpBasePath, &outbuf, &errbuf); err != nil {
		log.Error("git config [filter.lfs.clean -> <> ]: %v\n%s\n%s", err, outbuf.String(), errbuf.String())
		return fmt.Errorf("git config [filter.lfs.clean -> <> ]: %v\n%s\n%s", err, outbuf.String(), errbuf.String())
	}
	outbuf.Reset()
	errbuf.Reset()

	if err := gitConfigCommand().AddArguments("filter.lfs.smudge", "").RunInDirPipeline(tmpBasePath, &outbuf, &errbuf); err != nil {
		log.Error("git config [filter.lfs.smudge -> <> ]: %v\n%s\n%s", err, outbuf.String(), errbuf.String())
		return fmt.Errorf("git config [filter.lfs.smudge -> <> ]: %v\n%s\n%s", err, outbuf.String(), errbuf.String())
	}
	outbuf.Reset()
	errbuf.Reset()

	if err := gitConfigCommand().AddArguments("core.sparseCheckout", "true").RunInDirPipeline(tmpBasePath, &outbuf, &errbuf); err != nil {
		log.Error("git config [core.sparseCheckout -> true ]: %v\n%s\n%s", err, outbuf.String(), errbuf.String())
		return fmt.Errorf("git config [core.sparsecheckout -> true]: %v\n%s\n%s", err, outbuf.String(), errbuf.String())
	}
	outbuf.Reset()
	errbuf.Reset()

	// Read base branch index
	if err := git.NewCommand("read-tree", "HEAD").RunInDirPipeline(tmpBasePath, &outbuf, &errbuf); err != nil {
		log.Error("git read-tree HEAD: %v\n%s\n%s", err, outbuf.String(), errbuf.String())
		return fmt.Errorf("Unable to read base branch in to the index: %v\n%s\n%s", err, outbuf.String(), errbuf.String())
	}
	outbuf.Reset()
	errbuf.Reset()

	// Determine if we should sign
	signArg := ""
	if version.Compare(binVersion, "1.7.9", ">=") {
		sign, keyID := pr.SignMerge(doer, tmpBasePath, "HEAD", trackingBranch)
		if sign {
			signArg = "-S" + keyID
		} else if version.Compare(binVersion, "2.0.0", ">=") {
			signArg = "--no-gpg-sign"
		}
	}

	sig := doer.NewGitSig()
	commitTimeStr := time.Now().Format(time.RFC3339)

	// Because this may call hooks we should pass in the environment
	env := append(os.Environ(),
		"GIT_AUTHOR_NAME="+sig.Name,
		"GIT_AUTHOR_EMAIL="+sig.Email,
		"GIT_AUTHOR_DATE="+commitTimeStr,
		"GIT_COMMITTER_NAME="+sig.Name,
		"GIT_COMMITTER_EMAIL="+sig.Email,
		"GIT_COMMITTER_DATE="+commitTimeStr,
	)

	// Merge commits.
	switch mergeStyle {
	case models.MergeStyleMerge:
		cmd := git.NewCommand("merge", "--no-ff", "--no-commit", trackingBranch)
		if err := runMergeCommand(pr, mergeStyle, cmd, tmpBasePath); err != nil {
			log.Error("Unable to merge tracking into base: %v", err)
			return err
		}

		if err := commitAndSignNoAuthor(pr, message, signArg, tmpBasePath, env); err != nil {
			log.Error("Unable to make final commit: %v", err)
			return err
		}
	case models.MergeStyleRebase:
		fallthrough
	case models.MergeStyleRebaseMerge:
		// Checkout head branch
		if err := git.NewCommand("checkout", "-b", stagingBranch, trackingBranch).RunInDirPipeline(tmpBasePath, &outbuf, &errbuf); err != nil {
			log.Error("git checkout base prior to merge post staging rebase [%s:%s -> %s:%s]: %v\n%s\n%s", pr.HeadRepo.FullName(), pr.HeadBranch, pr.BaseRepo.FullName(), pr.BaseBranch, err, outbuf.String(), errbuf.String())
			return fmt.Errorf("git checkout base prior to merge post staging rebase  [%s:%s -> %s:%s]: %v\n%s\n%s", pr.HeadRepo.FullName(), pr.HeadBranch, pr.BaseRepo.FullName(), pr.BaseBranch, err, outbuf.String(), errbuf.String())
		}
		outbuf.Reset()
		errbuf.Reset()

		// Rebase before merging
		if err := git.NewCommand("rebase", baseBranch).RunInDirPipeline(tmpBasePath, &outbuf, &errbuf); err != nil {
			// Rebase will leave a REBASE_HEAD file in .git if there is a conflict
			if _, statErr := os.Stat(filepath.Join(tmpBasePath, ".git", "REBASE_HEAD")); statErr == nil {
				// The original commit SHA1 that is failing will be in .git/rebase-apply/original-commit
				commitShaBytes, readErr := ioutil.ReadFile(filepath.Join(tmpBasePath, ".git", "rebase-apply", "original-commit"))
				if readErr != nil {
					// Abandon this attempt to handle the error
					log.Error("git rebase staging on to base [%s:%s -> %s:%s]: %v\n%s\n%s", pr.HeadRepo.FullName(), pr.HeadBranch, pr.BaseRepo.FullName(), pr.BaseBranch, err, outbuf.String(), errbuf.String())
					return fmt.Errorf("git rebase staging on to base [%s:%s -> %s:%s]: %v\n%s\n%s", pr.HeadRepo.FullName(), pr.HeadBranch, pr.BaseRepo.FullName(), pr.BaseBranch, err, outbuf.String(), errbuf.String())
				}
				log.Debug("RebaseConflict at %s [%s:%s -> %s:%s]: %v\n%s\n%s", strings.TrimSpace(string(commitShaBytes)), pr.HeadRepo.FullName(), pr.HeadBranch, pr.BaseRepo.FullName(), pr.BaseBranch, err, outbuf.String(), errbuf.String())
				return models.ErrRebaseConflicts{
					Style:     mergeStyle,
					CommitSHA: strings.TrimSpace(string(commitShaBytes)),
					StdOut:    outbuf.String(),
					StdErr:    errbuf.String(),
					Err:       err,
				}
			}
			log.Error("git rebase staging on to base [%s:%s -> %s:%s]: %v\n%s\n%s", pr.HeadRepo.FullName(), pr.HeadBranch, pr.BaseRepo.FullName(), pr.BaseBranch, err, outbuf.String(), errbuf.String())
			return fmt.Errorf("git rebase staging on to base [%s:%s -> %s:%s]: %v\n%s\n%s", pr.HeadRepo.FullName(), pr.HeadBranch, pr.BaseRepo.FullName(), pr.BaseBranch, err, outbuf.String(), errbuf.String())
		}
		outbuf.Reset()
		errbuf.Reset()

		// Checkout base branch again
		if err := git.NewCommand("checkout", baseBranch).RunInDirPipeline(tmpBasePath, &outbuf, &errbuf); err != nil {
			log.Error("git checkout base prior to merge post staging rebase [%s:%s -> %s:%s]: %v\n%s\n%s", pr.HeadRepo.FullName(), pr.HeadBranch, pr.BaseRepo.FullName(), pr.BaseBranch, err, outbuf.String(), errbuf.String())
			return fmt.Errorf("git checkout base prior to merge post staging rebase  [%s:%s -> %s:%s]: %v\n%s\n%s", pr.HeadRepo.FullName(), pr.HeadBranch, pr.BaseRepo.FullName(), pr.BaseBranch, err, outbuf.String(), errbuf.String())
		}
		outbuf.Reset()
		errbuf.Reset()

		cmd := git.NewCommand("merge")
		if mergeStyle == models.MergeStyleRebase {
			cmd.AddArguments("--ff-only")
		} else {
			cmd.AddArguments("--no-ff", "--no-commit")
		}
		cmd.AddArguments(stagingBranch)

		// Prepare merge with commit
		if err := runMergeCommand(pr, mergeStyle, cmd, tmpBasePath); err != nil {
			log.Error("Unable to merge staging into base: %v", err)
			return err
		}
		if mergeStyle == models.MergeStyleRebaseMerge {
			if err := commitAndSignNoAuthor(pr, message, signArg, tmpBasePath, env); err != nil {
				log.Error("Unable to make final commit: %v", err)
				return err
			}
		}
	case models.MergeStyleSquash:
		// Merge with squash
		cmd := git.NewCommand("merge", "--squash", trackingBranch)
		if err := runMergeCommand(pr, mergeStyle, cmd, tmpBasePath); err != nil {
			log.Error("Unable to merge --squash tracking into base: %v", err)
			return err
		}

		sig := pr.Issue.Poster.NewGitSig()
		if signArg == "" {
			if err := git.NewCommand("commit", fmt.Sprintf("--author='%s <%s>'", sig.Name, sig.Email), "-m", message).RunInDirTimeoutEnvPipeline(env, -1, tmpBasePath, &outbuf, &errbuf); err != nil {
				log.Error("git commit [%s:%s -> %s:%s]: %v\n%s\n%s", pr.HeadRepo.FullName(), pr.HeadBranch, pr.BaseRepo.FullName(), pr.BaseBranch, err, outbuf.String(), errbuf.String())
				return fmt.Errorf("git commit [%s:%s -> %s:%s]: %v\n%s\n%s", pr.HeadRepo.FullName(), pr.HeadBranch, pr.BaseRepo.FullName(), pr.BaseBranch, err, outbuf.String(), errbuf.String())
			}
		} else {
			if err := git.NewCommand("commit", signArg, fmt.Sprintf("--author='%s <%s>'", sig.Name, sig.Email), "-m", message).RunInDirTimeoutEnvPipeline(env, -1, tmpBasePath, &outbuf, &errbuf); err != nil {
				log.Error("git commit [%s:%s -> %s:%s]: %v\n%s\n%s", pr.HeadRepo.FullName(), pr.HeadBranch, pr.BaseRepo.FullName(), pr.BaseBranch, err, outbuf.String(), errbuf.String())
				return fmt.Errorf("git commit [%s:%s -> %s:%s]: %v\n%s\n%s", pr.HeadRepo.FullName(), pr.HeadBranch, pr.BaseRepo.FullName(), pr.BaseBranch, err, outbuf.String(), errbuf.String())
			}
		}
		outbuf.Reset()
		errbuf.Reset()
	default:
		return models.ErrInvalidMergeStyle{ID: pr.BaseRepo.ID, Style: mergeStyle}
	}

	// OK we should cache our current head and origin/headbranch
	mergeHeadSHA, err := git.GetFullCommitID(tmpBasePath, "HEAD")
	if err != nil {
		return fmt.Errorf("Failed to get full commit id for HEAD: %v", err)
	}
	mergeBaseSHA, err := git.GetFullCommitID(tmpBasePath, "original_"+baseBranch)
	if err != nil {
		return fmt.Errorf("Failed to get full commit id for origin/%s: %v", pr.BaseBranch, err)
	}

	// Now it's questionable about where this should go - either after or before the push
	// I think in the interests of data safety - failures to push to the lfs should prevent
	// the merge as you can always remerge.
	if setting.LFS.StartServer {
		if err := LFSPush(tmpBasePath, mergeHeadSHA, mergeBaseSHA, pr); err != nil {
			return err
		}
	}

	var headUser *models.User
	err = pr.HeadRepo.GetOwner()
	if err != nil {
		if !models.IsErrUserNotExist(err) {
			log.Error("Can't find user: %d for head repository - %v", pr.HeadRepo.OwnerID, err)
			return err
		}
		log.Error("Can't find user: %d for head repository - defaulting to doer: %s - %v", pr.HeadRepo.OwnerID, doer.Name, err)
		headUser = doer
	} else {
		headUser = pr.HeadRepo.Owner
	}

	env = models.FullPushingEnvironment(
		headUser,
		doer,
		pr.BaseRepo,
		pr.BaseRepo.Name,
		pr.ID,
	)

	// Push back to upstream.
	if err := git.NewCommand("push", "origin", baseBranch+":"+pr.BaseBranch).RunInDirTimeoutEnvPipeline(env, -1, tmpBasePath, &outbuf, &errbuf); err != nil {
		if strings.Contains(errbuf.String(), "non-fast-forward") {
			return models.ErrMergePushOutOfDate{
				Style:  mergeStyle,
				StdOut: outbuf.String(),
				StdErr: errbuf.String(),
				Err:    err,
			}
		}
		return fmt.Errorf("git push: %s", errbuf.String())
	}
	outbuf.Reset()
	errbuf.Reset()

	pr.MergedCommitID, err = baseGitRepo.GetBranchCommitID(pr.BaseBranch)
	if err != nil {
		return fmt.Errorf("GetBranchCommit: %v", err)
	}

	pr.MergedUnix = timeutil.TimeStampNow()
	pr.Merger = doer
	pr.MergerID = doer.ID

	if err = pr.SetMerged(); err != nil {
		log.Error("setMerged [%d]: %v", pr.ID, err)
	}

	notification.NotifyMergePullRequest(pr, doer, baseGitRepo)

	// Reset cached commit count
	cache.Remove(pr.Issue.Repo.GetCommitsCountCacheKey(pr.BaseBranch, true))

	// Resolve cross references
	refs, err := pr.ResolveCrossReferences()
	if err != nil {
		log.Error("ResolveCrossReferences: %v", err)
		return nil
	}

	for _, ref := range refs {
		if err = ref.LoadIssue(); err != nil {
			return err
		}
		if err = ref.Issue.LoadRepo(); err != nil {
			return err
		}
		close := (ref.RefAction == references.XRefActionCloses)
		if err = issue_service.ChangeStatus(ref.Issue, doer, close); err != nil {
			return err
		}
	}

	return nil
}

func commitAndSignNoAuthor(pr *models.PullRequest, message, signArg, tmpBasePath string, env []string) error {
	var outbuf, errbuf strings.Builder
	if signArg == "" {
		if err := git.NewCommand("commit", "-m", message).RunInDirTimeoutEnvPipeline(env, -1, tmpBasePath, &outbuf, &errbuf); err != nil {
			log.Error("git commit [%s:%s -> %s:%s]: %v\n%s\n%s", pr.HeadRepo.FullName(), pr.HeadBranch, pr.BaseRepo.FullName(), pr.BaseBranch, err, outbuf.String(), errbuf.String())
			return fmt.Errorf("git commit [%s:%s -> %s:%s]: %v\n%s\n%s", pr.HeadRepo.FullName(), pr.HeadBranch, pr.BaseRepo.FullName(), pr.BaseBranch, err, outbuf.String(), errbuf.String())
		}
	} else {
		if err := git.NewCommand("commit", signArg, "-m", message).RunInDirTimeoutEnvPipeline(env, -1, tmpBasePath, &outbuf, &errbuf); err != nil {
			log.Error("git commit [%s:%s -> %s:%s]: %v\n%s\n%s", pr.HeadRepo.FullName(), pr.HeadBranch, pr.BaseRepo.FullName(), pr.BaseBranch, err, outbuf.String(), errbuf.String())
			return fmt.Errorf("git commit [%s:%s -> %s:%s]: %v\n%s\n%s", pr.HeadRepo.FullName(), pr.HeadBranch, pr.BaseRepo.FullName(), pr.BaseBranch, err, outbuf.String(), errbuf.String())
		}
	}
	return nil
}

func runMergeCommand(pr *models.PullRequest, mergeStyle models.MergeStyle, cmd *git.Command, tmpBasePath string) error {
	var outbuf, errbuf strings.Builder
	if err := cmd.RunInDirPipeline(tmpBasePath, &outbuf, &errbuf); err != nil {
		// Merge will leave a MERGE_HEAD file in the .git folder if there is a conflict
		if _, statErr := os.Stat(filepath.Join(tmpBasePath, ".git", "MERGE_HEAD")); statErr == nil {
			// We have a merge conflict error
			log.Debug("MergeConflict [%s:%s -> %s:%s]: %v\n%s\n%s", pr.HeadRepo.FullName(), pr.HeadBranch, pr.BaseRepo.FullName(), pr.BaseBranch, err, outbuf.String(), errbuf.String())
			return models.ErrMergeConflicts{
				Style:  mergeStyle,
				StdOut: outbuf.String(),
				StdErr: errbuf.String(),
				Err:    err,
			}
		} else if strings.Contains(errbuf.String(), "refusing to merge unrelated histories") {
			log.Debug("MergeUnrelatedHistories [%s:%s -> %s:%s]: %v\n%s\n%s", pr.HeadRepo.FullName(), pr.HeadBranch, pr.BaseRepo.FullName(), pr.BaseBranch, err, outbuf.String(), errbuf.String())
			return models.ErrMergeUnrelatedHistories{
				Style:  mergeStyle,
				StdOut: outbuf.String(),
				StdErr: errbuf.String(),
				Err:    err,
			}
		}
		log.Error("git merge [%s:%s -> %s:%s]: %v\n%s\n%s", pr.HeadRepo.FullName(), pr.HeadBranch, pr.BaseRepo.FullName(), pr.BaseBranch, err, outbuf.String(), errbuf.String())
		return fmt.Errorf("git merge [%s:%s -> %s:%s]: %v\n%s\n%s", pr.HeadRepo.FullName(), pr.HeadBranch, pr.BaseRepo.FullName(), pr.BaseBranch, err, outbuf.String(), errbuf.String())
	}

	return nil
}

var escapedSymbols = regexp.MustCompile(`([*[?! \\])`)

func getDiffTree(repoPath, baseBranch, headBranch string) (string, error) {
	getDiffTreeFromBranch := func(repoPath, baseBranch, headBranch string) (string, error) {
		var outbuf, errbuf strings.Builder
		// Compute the diff-tree for sparse-checkout
		if err := git.NewCommand("diff-tree", "--no-commit-id", "--name-only", "-r", "-z", "--root", baseBranch, headBranch, "--").RunInDirPipeline(repoPath, &outbuf, &errbuf); err != nil {
			return "", fmt.Errorf("git diff-tree [%s base:%s head:%s]: %s", repoPath, baseBranch, headBranch, errbuf.String())
		}
		return outbuf.String(), nil
	}

	scanNullTerminatedStrings := func(data []byte, atEOF bool) (advance int, token []byte, err error) {
		if atEOF && len(data) == 0 {
			return 0, nil, nil
		}
		if i := bytes.IndexByte(data, '\x00'); i >= 0 {
			return i + 1, data[0:i], nil
		}
		if atEOF {
			return len(data), data, nil
		}
		return 0, nil, nil
	}

	list, err := getDiffTreeFromBranch(repoPath, baseBranch, headBranch)
	if err != nil {
		return "", err
	}

	// Prefixing '/' for each entry, otherwise all files with the same name in subdirectories would be matched.
	out := bytes.Buffer{}
	scanner := bufio.NewScanner(strings.NewReader(list))
	scanner.Split(scanNullTerminatedStrings)
	for scanner.Scan() {
		filepath := scanner.Text()
		// escape '*', '?', '[', spaces and '!' prefix
		filepath = escapedSymbols.ReplaceAllString(filepath, `\$1`)
		// no necessary to escape the first '#' symbol because the first symbol is '/'
		fmt.Fprintf(&out, "/%s\n", filepath)
	}

	return out.String(), nil
}
