// Copyright 2019 The Gitea Authors. All rights reserved.
// Use of this source code is governed by a MIT-style
// license that can be found in the LICENSE file.

package pull

import (
	"context"
	"fmt"
	"os"
	"path"

	"code.gitea.io/gitea/models"
	"code.gitea.io/gitea/modules/git"
	"code.gitea.io/gitea/modules/graceful"
	"code.gitea.io/gitea/modules/log"
	"code.gitea.io/gitea/modules/notification"
	issue_service "code.gitea.io/gitea/services/issue"
)

// NewPullRequest creates new pull request with labels for repository.
func NewPullRequest(repo *models.Repository, pull *models.Issue, labelIDs []int64, uuids []string, pr *models.PullRequest, assigneeIDs []int64) error {
	if err := TestPatch(pr); err != nil {
		return err
	}

	if err := models.NewPullRequest(repo, pull, labelIDs, uuids, pr); err != nil {
		return err
	}

	for _, assigneeID := range assigneeIDs {
		if err := issue_service.AddAssigneeIfNotAssigned(pull, pull.Poster, assigneeID); err != nil {
			return err
		}
	}

	pr.Issue = pull
	pull.PullRequest = pr

	if err := PushToBaseRepo(pr); err != nil {
		return err
	}

	notification.NotifyNewPullRequest(pr)

	return nil
}

// ChangeTargetBranch changes the target branch of this pull request, as the given user.
func ChangeTargetBranch(pr *models.PullRequest, doer *models.User, targetBranch string) (err error) {
	// Current target branch is already the same
	if pr.BaseBranch == targetBranch {
		return nil
	}

	if pr.Issue.IsClosed {
		return models.ErrIssueIsClosed{
			ID:     pr.Issue.ID,
			RepoID: pr.Issue.RepoID,
			Index:  pr.Issue.Index,
		}
	}

	if pr.HasMerged {
		return models.ErrPullRequestHasMerged{
			ID:         pr.ID,
			IssueID:    pr.Index,
			HeadRepoID: pr.HeadRepoID,
			BaseRepoID: pr.BaseRepoID,
			HeadBranch: pr.HeadBranch,
			BaseBranch: pr.BaseBranch,
		}
	}

	// Check if branches are equal
	branchesEqual, err := pr.IsHeadEqualWithBranch(targetBranch)
	if err != nil {
		return err
	}
	if branchesEqual {
		return models.ErrBranchesEqual{
			HeadBranchName: pr.HeadBranch,
			BaseBranchName: targetBranch,
		}
	}

	// Check if pull request for the new target branch already exists
	existingPr, err := models.GetUnmergedPullRequest(pr.HeadRepoID, pr.BaseRepoID, pr.HeadBranch, targetBranch)
	if existingPr != nil {
		return models.ErrPullRequestAlreadyExists{
			ID:         existingPr.ID,
			IssueID:    existingPr.Index,
			HeadRepoID: existingPr.HeadRepoID,
			BaseRepoID: existingPr.BaseRepoID,
			HeadBranch: existingPr.HeadBranch,
			BaseBranch: existingPr.BaseBranch,
		}
	}
	if err != nil && !models.IsErrPullRequestNotExist(err) {
		return err
	}

	// Set new target branch
	oldBranch := pr.BaseBranch
	pr.BaseBranch = targetBranch

	// Refresh patch
	if err := TestPatch(pr); err != nil {
		return err
	}

	// Update target branch, PR diff and status
	// This is the same as checkAndUpdateStatus in check service, but also updates base_branch
	if pr.Status == models.PullRequestStatusChecking {
		pr.Status = models.PullRequestStatusMergeable
	}
	if err := pr.UpdateCols("status, conflicted_files, base_branch"); err != nil {
		return err
	}

	// Create comment
	options := &models.CreateCommentOptions{
		Type:   models.CommentTypeChangeTargetBranch,
		Doer:   doer,
		Repo:   pr.Issue.Repo,
		Issue:  pr.Issue,
		OldRef: oldBranch,
		NewRef: targetBranch,
	}
	if _, err = models.CreateComment(options); err != nil {
		return fmt.Errorf("CreateChangeTargetBranchComment: %v", err)
	}

	return nil
}

func checkForInvalidation(requests models.PullRequestList, repoID int64, doer *models.User, branch string) error {
	repo, err := models.GetRepositoryByID(repoID)
	if err != nil {
		return fmt.Errorf("GetRepositoryByID: %v", err)
	}
	gitRepo, err := git.OpenRepository(repo.RepoPath())
	if err != nil {
		return fmt.Errorf("git.OpenRepository: %v", err)
	}
	go func() {
		// FIXME: graceful: We need to tell the manager we're doing something...
		err := requests.InvalidateCodeComments(doer, gitRepo, branch)
		if err != nil {
			log.Error("PullRequestList.InvalidateCodeComments: %v", err)
		}
		gitRepo.Close()
	}()
	return nil
}

func addHeadRepoTasks(prs []*models.PullRequest) {
	for _, pr := range prs {
		log.Trace("addHeadRepoTasks[%d]: composing new test task", pr.ID)
		if err := PushToBaseRepo(pr); err != nil {
			log.Error("PushToBaseRepo: %v", err)
			continue
		}

		AddToTaskQueue(pr)
	}
}

// AddTestPullRequestTask adds new test tasks by given head/base repository and head/base branch,
// and generate new patch for testing as needed.
func AddTestPullRequestTask(doer *models.User, repoID int64, branch string, isSync bool) {
	log.Trace("AddTestPullRequestTask [head_repo_id: %d, head_branch: %s]: finding pull requests", repoID, branch)
	graceful.GetManager().RunWithShutdownContext(func(ctx context.Context) {
		// There is no sensible way to shut this down ":-("
		// If you don't let it run all the way then you will lose data
		// FIXME: graceful: AddTestPullRequestTask needs to become a queue!

		prs, err := models.GetUnmergedPullRequestsByHeadInfo(repoID, branch)
		if err != nil {
			log.Error("Find pull requests [head_repo_id: %d, head_branch: %s]: %v", repoID, branch, err)
			return
		}

		if isSync {
			requests := models.PullRequestList(prs)
			if err = requests.LoadAttributes(); err != nil {
				log.Error("PullRequestList.LoadAttributes: %v", err)
			}
			if invalidationErr := checkForInvalidation(requests, repoID, doer, branch); invalidationErr != nil {
				log.Error("checkForInvalidation: %v", invalidationErr)
			}
			if err == nil {
				for _, pr := range prs {
					pr.Issue.PullRequest = pr
					notification.NotifyPullRequestSynchronized(doer, pr)
				}
			}
		}

		addHeadRepoTasks(prs)

		log.Trace("AddTestPullRequestTask [base_repo_id: %d, base_branch: %s]: finding pull requests", repoID, branch)
		prs, err = models.GetUnmergedPullRequestsByBaseInfo(repoID, branch)
		if err != nil {
			log.Error("Find pull requests [base_repo_id: %d, base_branch: %s]: %v", repoID, branch, err)
			return
		}
		for _, pr := range prs {
			AddToTaskQueue(pr)
		}
	})
}

// PushToBaseRepo pushes commits from branches of head repository to
// corresponding branches of base repository.
// FIXME: Only push branches that are actually updates?
func PushToBaseRepo(pr *models.PullRequest) (err error) {
	log.Trace("PushToBaseRepo[%d]: pushing commits to base repo '%s'", pr.BaseRepoID, pr.GetGitRefName())

	headRepoPath := pr.HeadRepo.RepoPath()
	headGitRepo, err := git.OpenRepository(headRepoPath)
	if err != nil {
		return fmt.Errorf("OpenRepository: %v", err)
	}
	defer headGitRepo.Close()

	tmpRemoteName := fmt.Sprintf("tmp-pull-%d", pr.ID)
	if err = headGitRepo.AddRemote(tmpRemoteName, pr.BaseRepo.RepoPath(), false); err != nil {
		return fmt.Errorf("headGitRepo.AddRemote: %v", err)
	}
	// Make sure to remove the remote even if the push fails
	defer func() {
		if err := headGitRepo.RemoveRemote(tmpRemoteName); err != nil {
			log.Error("PushToBaseRepo: RemoveRemote: %s", err)
		}
	}()

	headFile := pr.GetGitRefName()

	// Remove head in case there is a conflict.
	file := path.Join(pr.BaseRepo.RepoPath(), headFile)

	_ = os.Remove(file)

	if err = pr.LoadIssue(); err != nil {
		return fmt.Errorf("unable to load issue %d for pr %d: %v", pr.IssueID, pr.ID, err)
	}
	if err = pr.Issue.LoadPoster(); err != nil {
		return fmt.Errorf("unable to load poster %d for pr %d: %v", pr.Issue.PosterID, pr.ID, err)
	}

	if err = git.Push(headRepoPath, git.PushOptions{
		Remote: tmpRemoteName,
		Branch: fmt.Sprintf("%s:%s", pr.HeadBranch, headFile),
		Force:  true,
		// Use InternalPushingEnvironment here because we know that pre-receive and post-receive do not run on a refs/pulls/...
		Env: models.InternalPushingEnvironment(pr.Issue.Poster, pr.BaseRepo),
	}); err != nil {
		return fmt.Errorf("Push: %v", err)
	}

	return nil
}
