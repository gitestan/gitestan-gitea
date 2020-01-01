// Copyright 2018 The Gitea Authors.
// Copyright 2014 The Gogs Authors.
// All rights reserved.
// Use of this source code is governed by a MIT-style
// license that can be found in the LICENSE file.

package repo

import (
	"container/list"
	"crypto/subtle"
	"fmt"
	"html"
	"net/http"
	"path"
	"strings"

	"code.gitea.io/gitea/models"
	"code.gitea.io/gitea/modules/auth"
	"code.gitea.io/gitea/modules/base"
	"code.gitea.io/gitea/modules/context"
	"code.gitea.io/gitea/modules/git"
	"code.gitea.io/gitea/modules/log"
	"code.gitea.io/gitea/modules/notification"
	"code.gitea.io/gitea/modules/repofiles"
	"code.gitea.io/gitea/modules/setting"
	"code.gitea.io/gitea/modules/util"
	"code.gitea.io/gitea/services/gitdiff"
	pull_service "code.gitea.io/gitea/services/pull"
	repo_service "code.gitea.io/gitea/services/repository"

	"github.com/unknwon/com"
)

const (
	tplFork        base.TplName = "repo/pulls/fork"
	tplCompareDiff base.TplName = "repo/diff/compare"
	tplPullCommits base.TplName = "repo/pulls/commits"
	tplPullFiles   base.TplName = "repo/pulls/files"

	pullRequestTemplateKey = "PullRequestTemplate"
)

var (
	pullRequestTemplateCandidates = []string{
		"PULL_REQUEST_TEMPLATE.md",
		"pull_request_template.md",
		".gitea/PULL_REQUEST_TEMPLATE.md",
		".gitea/pull_request_template.md",
		".github/PULL_REQUEST_TEMPLATE.md",
		".github/pull_request_template.md",
	}
)

func getRepository(ctx *context.Context, repoID int64) *models.Repository {
	repo, err := models.GetRepositoryByID(repoID)
	if err != nil {
		if models.IsErrRepoNotExist(err) {
			ctx.NotFound("GetRepositoryByID", nil)
		} else {
			ctx.ServerError("GetRepositoryByID", err)
		}
		return nil
	}

	perm, err := models.GetUserRepoPermission(repo, ctx.User)
	if err != nil {
		ctx.ServerError("GetUserRepoPermission", err)
		return nil
	}

	if !perm.CanRead(models.UnitTypeCode) {
		log.Trace("Permission Denied: User %-v cannot read %-v of repo %-v\n"+
			"User in repo has Permissions: %-+v",
			ctx.User,
			models.UnitTypeCode,
			ctx.Repo,
			perm)
		ctx.NotFound("getRepository", nil)
		return nil
	}
	return repo
}

func getForkRepository(ctx *context.Context) *models.Repository {
	forkRepo := getRepository(ctx, ctx.ParamsInt64(":repoid"))
	if ctx.Written() {
		return nil
	}

	if forkRepo.IsEmpty {
		log.Trace("Empty repository %-v", forkRepo)
		ctx.NotFound("getForkRepository", nil)
		return nil
	}

	ctx.Data["repo_name"] = forkRepo.Name
	ctx.Data["description"] = forkRepo.Description
	ctx.Data["IsPrivate"] = forkRepo.IsPrivate
	canForkToUser := forkRepo.OwnerID != ctx.User.ID && !ctx.User.HasForkedRepo(forkRepo.ID)

	if err := forkRepo.GetOwner(); err != nil {
		ctx.ServerError("GetOwner", err)
		return nil
	}
	ctx.Data["ForkFrom"] = forkRepo.Owner.Name + "/" + forkRepo.Name
	ctx.Data["ForkFromOwnerID"] = forkRepo.Owner.ID

	if err := ctx.User.GetOwnedOrganizations(); err != nil {
		ctx.ServerError("GetOwnedOrganizations", err)
		return nil
	}
	var orgs []*models.User
	for _, org := range ctx.User.OwnedOrgs {
		if forkRepo.OwnerID != org.ID && !org.HasForkedRepo(forkRepo.ID) {
			orgs = append(orgs, org)
		}
	}

	var traverseParentRepo = forkRepo
	var err error
	for {
		if ctx.User.ID == traverseParentRepo.OwnerID {
			canForkToUser = false
		} else {
			for i, org := range orgs {
				if org.ID == traverseParentRepo.OwnerID {
					orgs = append(orgs[:i], orgs[i+1:]...)
					break
				}
			}
		}

		if !traverseParentRepo.IsFork {
			break
		}
		traverseParentRepo, err = models.GetRepositoryByID(traverseParentRepo.ForkID)
		if err != nil {
			ctx.ServerError("GetRepositoryByID", err)
			return nil
		}
	}

	ctx.Data["CanForkToUser"] = canForkToUser
	ctx.Data["Orgs"] = orgs

	if canForkToUser {
		ctx.Data["ContextUser"] = ctx.User
	} else if len(orgs) > 0 {
		ctx.Data["ContextUser"] = orgs[0]
	}

	return forkRepo
}

// Fork render repository fork page
func Fork(ctx *context.Context) {
	ctx.Data["Title"] = ctx.Tr("new_fork")

	getForkRepository(ctx)
	if ctx.Written() {
		return
	}

	ctx.HTML(200, tplFork)
}

// ForkPost response for forking a repository
func ForkPost(ctx *context.Context, form auth.CreateRepoForm) {
	ctx.Data["Title"] = ctx.Tr("new_fork")

	ctxUser := checkContextUser(ctx, form.UID)
	if ctx.Written() {
		return
	}

	forkRepo := getForkRepository(ctx)
	if ctx.Written() {
		return
	}

	ctx.Data["ContextUser"] = ctxUser

	if ctx.HasError() {
		ctx.HTML(200, tplFork)
		return
	}

	var err error
	var traverseParentRepo = forkRepo
	for {
		if ctxUser.ID == traverseParentRepo.OwnerID {
			ctx.RenderWithErr(ctx.Tr("repo.settings.new_owner_has_same_repo"), tplFork, &form)
			return
		}
		repo, has := models.HasForkedRepo(ctxUser.ID, traverseParentRepo.ID)
		if has {
			ctx.Redirect(setting.AppSubURL + "/" + ctxUser.Name + "/" + repo.Name)
			return
		}
		if !traverseParentRepo.IsFork {
			break
		}
		traverseParentRepo, err = models.GetRepositoryByID(traverseParentRepo.ForkID)
		if err != nil {
			ctx.ServerError("GetRepositoryByID", err)
			return
		}
	}

	// Check ownership of organization.
	if ctxUser.IsOrganization() {
		isOwner, err := ctxUser.IsOwnedBy(ctx.User.ID)
		if err != nil {
			ctx.ServerError("IsOwnedBy", err)
			return
		} else if !isOwner {
			ctx.Error(403)
			return
		}
	}

	repo, err := repo_service.ForkRepository(ctx.User, ctxUser, forkRepo, form.RepoName, form.Description)
	if err != nil {
		ctx.Data["Err_RepoName"] = true
		switch {
		case models.IsErrRepoAlreadyExist(err):
			ctx.RenderWithErr(ctx.Tr("repo.settings.new_owner_has_same_repo"), tplFork, &form)
		case models.IsErrNameReserved(err):
			ctx.RenderWithErr(ctx.Tr("repo.form.name_reserved", err.(models.ErrNameReserved).Name), tplFork, &form)
		case models.IsErrNamePatternNotAllowed(err):
			ctx.RenderWithErr(ctx.Tr("repo.form.name_pattern_not_allowed", err.(models.ErrNamePatternNotAllowed).Pattern), tplFork, &form)
		default:
			ctx.ServerError("ForkPost", err)
		}
		return
	}

	log.Trace("Repository forked[%d]: %s/%s", forkRepo.ID, ctxUser.Name, repo.Name)
	ctx.Redirect(setting.AppSubURL + "/" + ctxUser.Name + "/" + repo.Name)
}

func checkPullInfo(ctx *context.Context) *models.Issue {
	issue, err := models.GetIssueByIndex(ctx.Repo.Repository.ID, ctx.ParamsInt64(":index"))
	if err != nil {
		if models.IsErrIssueNotExist(err) {
			ctx.NotFound("GetIssueByIndex", err)
		} else {
			ctx.ServerError("GetIssueByIndex", err)
		}
		return nil
	}
	if err = issue.LoadPoster(); err != nil {
		ctx.ServerError("LoadPoster", err)
		return nil
	}
	if err := issue.LoadRepo(); err != nil {
		ctx.ServerError("LoadRepo", err)
		return nil
	}
	ctx.Data["Title"] = fmt.Sprintf("#%d - %s", issue.Index, issue.Title)
	ctx.Data["Issue"] = issue

	if !issue.IsPull {
		ctx.NotFound("ViewPullCommits", nil)
		return nil
	}

	if err = issue.LoadPullRequest(); err != nil {
		ctx.ServerError("LoadPullRequest", err)
		return nil
	}

	if err = issue.PullRequest.GetHeadRepo(); err != nil {
		ctx.ServerError("GetHeadRepo", err)
		return nil
	}

	if ctx.IsSigned {
		// Update issue-user.
		if err = issue.ReadBy(ctx.User.ID); err != nil {
			ctx.ServerError("ReadBy", err)
			return nil
		}
	}

	return issue
}

func setMergeTarget(ctx *context.Context, pull *models.PullRequest) {
	if ctx.Repo.Owner.Name == pull.MustHeadUserName() {
		ctx.Data["HeadTarget"] = pull.HeadBranch
	} else if pull.HeadRepo == nil {
		ctx.Data["HeadTarget"] = pull.MustHeadUserName() + ":" + pull.HeadBranch
	} else {
		ctx.Data["HeadTarget"] = pull.MustHeadUserName() + "/" + pull.HeadRepo.Name + ":" + pull.HeadBranch
	}
	ctx.Data["BaseTarget"] = pull.BaseBranch
}

// PrepareMergedViewPullInfo show meta information for a merged pull request view page
func PrepareMergedViewPullInfo(ctx *context.Context, issue *models.Issue) *git.CompareInfo {
	pull := issue.PullRequest

	setMergeTarget(ctx, pull)
	ctx.Data["HasMerged"] = true

	compareInfo, err := ctx.Repo.GitRepo.GetCompareInfo(ctx.Repo.Repository.RepoPath(),
		pull.MergeBase, pull.GetGitRefName())

	if err != nil {
		if strings.Contains(err.Error(), "fatal: Not a valid object name") {
			ctx.Data["IsPullRequestBroken"] = true
			ctx.Data["BaseTarget"] = "deleted"
			ctx.Data["NumCommits"] = 0
			ctx.Data["NumFiles"] = 0
			return nil
		}

		ctx.ServerError("GetCompareInfo", err)
		return nil
	}
	ctx.Data["NumCommits"] = compareInfo.Commits.Len()
	ctx.Data["NumFiles"] = compareInfo.NumFiles
	return compareInfo
}

// PrepareViewPullInfo show meta information for a pull request preview page
func PrepareViewPullInfo(ctx *context.Context, issue *models.Issue) *git.CompareInfo {
	repo := ctx.Repo.Repository
	pull := issue.PullRequest

	var err error
	if err = pull.GetHeadRepo(); err != nil {
		ctx.ServerError("GetHeadRepo", err)
		return nil
	}

	setMergeTarget(ctx, pull)

	if err = pull.LoadProtectedBranch(); err != nil {
		ctx.ServerError("GetLatestCommitStatus", err)
		return nil
	}
	ctx.Data["EnableStatusCheck"] = pull.ProtectedBranch != nil && pull.ProtectedBranch.EnableStatusCheck

	var headGitRepo *git.Repository
	var headBranchExist bool
	// HeadRepo may be missing
	if pull.HeadRepo != nil {
		headGitRepo, err = git.OpenRepository(pull.HeadRepo.RepoPath())
		if err != nil {
			ctx.ServerError("OpenRepository", err)
			return nil
		}
		defer headGitRepo.Close()

		headBranchExist = headGitRepo.IsBranchExist(pull.HeadBranch)

		if headBranchExist {
			sha, err := headGitRepo.GetBranchCommitID(pull.HeadBranch)
			if err != nil {
				ctx.ServerError("GetBranchCommitID", err)
				return nil
			}

			commitStatuses, err := models.GetLatestCommitStatus(repo, sha, 0)
			if err != nil {
				ctx.ServerError("GetLatestCommitStatus", err)
				return nil
			}
			if len(commitStatuses) > 0 {
				ctx.Data["LatestCommitStatuses"] = commitStatuses
				ctx.Data["LatestCommitStatus"] = models.CalcCommitStatus(commitStatuses)
			}

			if pull.ProtectedBranch != nil && pull.ProtectedBranch.EnableStatusCheck {
				ctx.Data["is_context_required"] = func(context string) bool {
					for _, c := range pull.ProtectedBranch.StatusCheckContexts {
						if c == context {
							return true
						}
					}
					return false
				}
				ctx.Data["IsRequiredStatusCheckSuccess"] = pull_service.IsCommitStatusContextSuccess(commitStatuses, pull.ProtectedBranch.StatusCheckContexts)
			}
		}
	}

	if pull.HeadRepo == nil || !headBranchExist {
		ctx.Data["IsPullRequestBroken"] = true
		ctx.Data["HeadTarget"] = "deleted"
		ctx.Data["NumCommits"] = 0
		ctx.Data["NumFiles"] = 0
		return nil
	}

	compareInfo, err := headGitRepo.GetCompareInfo(models.RepoPath(repo.Owner.Name, repo.Name),
		pull.BaseBranch, pull.HeadBranch)
	if err != nil {
		if strings.Contains(err.Error(), "fatal: Not a valid object name") {
			ctx.Data["IsPullRequestBroken"] = true
			ctx.Data["BaseTarget"] = "deleted"
			ctx.Data["NumCommits"] = 0
			ctx.Data["NumFiles"] = 0
			return nil
		}

		ctx.ServerError("GetCompareInfo", err)
		return nil
	}

	if pull.IsWorkInProgress() {
		ctx.Data["IsPullWorkInProgress"] = true
		ctx.Data["WorkInProgressPrefix"] = pull.GetWorkInProgressPrefix()
	}

	if pull.IsFilesConflicted() {
		ctx.Data["IsPullFilesConflicted"] = true
		ctx.Data["ConflictedFiles"] = pull.ConflictedFiles
	}

	ctx.Data["NumCommits"] = compareInfo.Commits.Len()
	ctx.Data["NumFiles"] = compareInfo.NumFiles
	return compareInfo
}

// ViewPullCommits show commits for a pull request
func ViewPullCommits(ctx *context.Context) {
	ctx.Data["PageIsPullList"] = true
	ctx.Data["PageIsPullCommits"] = true

	issue := checkPullInfo(ctx)
	if ctx.Written() {
		return
	}
	pull := issue.PullRequest

	var commits *list.List
	var prInfo *git.CompareInfo
	if pull.HasMerged {
		prInfo = PrepareMergedViewPullInfo(ctx, issue)
	} else {
		prInfo = PrepareViewPullInfo(ctx, issue)
	}

	if ctx.Written() {
		return
	} else if prInfo == nil {
		ctx.NotFound("ViewPullCommits", nil)
		return
	}

	ctx.Data["Username"] = ctx.Repo.Owner.Name
	ctx.Data["Reponame"] = ctx.Repo.Repository.Name
	commits = prInfo.Commits
	commits = models.ValidateCommitsWithEmails(commits)
	commits = models.ParseCommitsWithSignature(commits)
	commits = models.ParseCommitsWithStatus(commits, ctx.Repo.Repository)
	ctx.Data["Commits"] = commits
	ctx.Data["CommitCount"] = commits.Len()

	getBranchData(ctx, issue)
	ctx.HTML(200, tplPullCommits)
}

// ViewPullFiles render pull request changed files list page
func ViewPullFiles(ctx *context.Context) {
	ctx.Data["PageIsPullList"] = true
	ctx.Data["PageIsPullFiles"] = true

	issue := checkPullInfo(ctx)
	if ctx.Written() {
		return
	}
	pull := issue.PullRequest

	whitespaceFlags := map[string]string{
		"ignore-all":    "-w",
		"ignore-change": "-b",
		"ignore-eol":    "--ignore-space-at-eol",
		"":              ""}

	var (
		diffRepoPath  string
		startCommitID string
		endCommitID   string
		gitRepo       *git.Repository
	)

	var headTarget string
	var prInfo *git.CompareInfo
	if pull.HasMerged {
		prInfo = PrepareMergedViewPullInfo(ctx, issue)
	} else {
		prInfo = PrepareViewPullInfo(ctx, issue)
	}

	if ctx.Written() {
		return
	} else if prInfo == nil {
		ctx.NotFound("ViewPullFiles", nil)
		return
	}

	diffRepoPath = ctx.Repo.GitRepo.Path
	gitRepo = ctx.Repo.GitRepo

	headCommitID, err := gitRepo.GetRefCommitID(pull.GetGitRefName())
	if err != nil {
		ctx.ServerError("GetRefCommitID", err)
		return
	}

	startCommitID = prInfo.MergeBase
	endCommitID = headCommitID

	headTarget = path.Join(ctx.Repo.Owner.Name, ctx.Repo.Repository.Name)
	ctx.Data["Username"] = ctx.Repo.Owner.Name
	ctx.Data["Reponame"] = ctx.Repo.Repository.Name
	ctx.Data["AfterCommitID"] = endCommitID

	diff, err := gitdiff.GetDiffRangeWithWhitespaceBehavior(diffRepoPath,
		startCommitID, endCommitID, setting.Git.MaxGitDiffLines,
		setting.Git.MaxGitDiffLineCharacters, setting.Git.MaxGitDiffFiles,
		whitespaceFlags[ctx.Data["WhitespaceBehavior"].(string)])
	if err != nil {
		ctx.ServerError("GetDiffRangeWithWhitespaceBehavior", err)
		return
	}

	if err = diff.LoadComments(issue, ctx.User); err != nil {
		ctx.ServerError("LoadComments", err)
		return
	}

	ctx.Data["Diff"] = diff
	ctx.Data["DiffNotAvailable"] = diff.NumFiles() == 0

	baseCommit, err := ctx.Repo.GitRepo.GetCommit(startCommitID)
	if err != nil {
		ctx.ServerError("GetCommit", err)
		return
	}
	commit, err := gitRepo.GetCommit(endCommitID)
	if err != nil {
		ctx.ServerError("GetCommit", err)
		return
	}

	setImageCompareContext(ctx, baseCommit, commit)
	setPathsCompareContext(ctx, baseCommit, commit, headTarget)

	ctx.Data["RequireHighlightJS"] = true
	ctx.Data["RequireSimpleMDE"] = true
	ctx.Data["RequireTribute"] = true
	if ctx.Data["Assignees"], err = ctx.Repo.Repository.GetAssignees(); err != nil {
		ctx.ServerError("GetAssignees", err)
		return
	}
	ctx.Data["CurrentReview"], err = models.GetCurrentReview(ctx.User, issue)
	if err != nil && !models.IsErrReviewNotExist(err) {
		ctx.ServerError("GetCurrentReview", err)
		return
	}
	getBranchData(ctx, issue)
	ctx.HTML(200, tplPullFiles)
}

// MergePullRequest response for merging pull request
func MergePullRequest(ctx *context.Context, form auth.MergePullRequestForm) {
	issue := checkPullInfo(ctx)
	if ctx.Written() {
		return
	}
	if issue.IsClosed {
		ctx.NotFound("MergePullRequest", nil)
		return
	}

	pr := issue.PullRequest

	if !pr.CanAutoMerge() || pr.HasMerged {
		ctx.NotFound("MergePullRequest", nil)
		return
	}

	if pr.IsWorkInProgress() {
		ctx.Flash.Error(ctx.Tr("repo.pulls.no_merge_wip"))
		ctx.Redirect(ctx.Repo.RepoLink + "/pulls/" + com.ToStr(pr.Index))
		return
	}

	isPass, err := pull_service.IsPullCommitStatusPass(pr)
	if err != nil {
		ctx.ServerError("IsPullCommitStatusPass", err)
		return
	}
	if !isPass && !ctx.IsUserRepoAdmin() {
		ctx.Flash.Error(ctx.Tr("repo.pulls.no_merge_status_check"))
		ctx.Redirect(ctx.Repo.RepoLink + "/pulls/" + com.ToStr(pr.Index))
		return
	}

	if ctx.HasError() {
		ctx.Flash.Error(ctx.Data["ErrorMsg"].(string))
		ctx.Redirect(ctx.Repo.RepoLink + "/pulls/" + com.ToStr(pr.Index))
		return
	}

	message := strings.TrimSpace(form.MergeTitleField)
	if len(message) == 0 {
		if models.MergeStyle(form.Do) == models.MergeStyleMerge {
			message = pr.GetDefaultMergeMessage()
		}
		if models.MergeStyle(form.Do) == models.MergeStyleRebaseMerge {
			message = pr.GetDefaultMergeMessage()
		}
		if models.MergeStyle(form.Do) == models.MergeStyleSquash {
			message = pr.GetDefaultSquashMessage()
		}
	}

	form.MergeMessageField = strings.TrimSpace(form.MergeMessageField)
	if len(form.MergeMessageField) > 0 {
		message += "\n\n" + form.MergeMessageField
	}

	pr.Issue = issue
	pr.Issue.Repo = ctx.Repo.Repository

	noDeps, err := models.IssueNoDependenciesLeft(issue)
	if err != nil {
		return
	}

	if !noDeps {
		ctx.Flash.Error(ctx.Tr("repo.issues.dependency.pr_close_blocked"))
		ctx.Redirect(ctx.Repo.RepoLink + "/pulls/" + com.ToStr(pr.Index))
		return
	}

	if err = pull_service.Merge(pr, ctx.User, ctx.Repo.GitRepo, models.MergeStyle(form.Do), message); err != nil {
		sanitize := func(x string) string {
			runes := []rune(x)

			if len(runes) > 512 {
				x = "..." + string(runes[len(runes)-512:])
			}

			return strings.Replace(html.EscapeString(x), "\n", "<br>", -1)
		}
		if models.IsErrInvalidMergeStyle(err) {
			ctx.Flash.Error(ctx.Tr("repo.pulls.invalid_merge_option"))
			ctx.Redirect(ctx.Repo.RepoLink + "/pulls/" + com.ToStr(pr.Index))
			return
		} else if models.IsErrMergeConflicts(err) {
			conflictError := err.(models.ErrMergeConflicts)
			ctx.Flash.Error(ctx.Tr("repo.pulls.merge_conflict", sanitize(conflictError.StdErr), sanitize(conflictError.StdOut)))
			ctx.Redirect(ctx.Repo.RepoLink + "/pulls/" + com.ToStr(pr.Index))
			return
		} else if models.IsErrRebaseConflicts(err) {
			conflictError := err.(models.ErrRebaseConflicts)
			ctx.Flash.Error(ctx.Tr("repo.pulls.rebase_conflict", sanitize(conflictError.CommitSHA), sanitize(conflictError.StdErr), sanitize(conflictError.StdOut)))
			ctx.Redirect(ctx.Repo.RepoLink + "/pulls/" + com.ToStr(pr.Index))
			return
		} else if models.IsErrMergeUnrelatedHistories(err) {
			log.Debug("MergeUnrelatedHistories error: %v", err)
			ctx.Flash.Error(ctx.Tr("repo.pulls.unrelated_histories"))
			ctx.Redirect(ctx.Repo.RepoLink + "/pulls/" + com.ToStr(pr.Index))
			return
		} else if models.IsErrMergePushOutOfDate(err) {
			log.Debug("MergePushOutOfDate error: %v", err)
			ctx.Flash.Error(ctx.Tr("repo.pulls.merge_out_of_date"))
			ctx.Redirect(ctx.Repo.RepoLink + "/pulls/" + com.ToStr(pr.Index))
			return
		}
		ctx.ServerError("Merge", err)
		return
	}

	if err := stopTimerIfAvailable(ctx.User, issue); err != nil {
		ctx.ServerError("CreateOrStopIssueStopwatch", err)
		return
	}

	log.Trace("Pull request merged: %d", pr.ID)
	ctx.Redirect(ctx.Repo.RepoLink + "/pulls/" + com.ToStr(pr.Index))
}

func stopTimerIfAvailable(user *models.User, issue *models.Issue) error {

	if models.StopwatchExists(user.ID, issue.ID) {
		if err := models.CreateOrStopIssueStopwatch(user, issue); err != nil {
			return err
		}
	}

	return nil
}

// CompareAndPullRequestPost response for creating pull request
func CompareAndPullRequestPost(ctx *context.Context, form auth.CreateIssueForm) {
	ctx.Data["Title"] = ctx.Tr("repo.pulls.compare_changes")
	ctx.Data["PageIsComparePull"] = true
	ctx.Data["IsDiffCompare"] = true
	ctx.Data["RequireHighlightJS"] = true
	ctx.Data["PullRequestWorkInProgressPrefixes"] = setting.Repository.PullRequest.WorkInProgressPrefixes
	renderAttachmentSettings(ctx)

	var (
		repo        = ctx.Repo.Repository
		attachments []string
	)

	headUser, headRepo, headGitRepo, prInfo, baseBranch, headBranch := ParseCompareInfo(ctx)
	if ctx.Written() {
		return
	}
	defer headGitRepo.Close()

	labelIDs, assigneeIDs, milestoneID := ValidateRepoMetas(ctx, form, true)
	if ctx.Written() {
		return
	}

	if setting.AttachmentEnabled {
		attachments = form.Files
	}

	if ctx.HasError() {
		auth.AssignForm(form, ctx.Data)

		// This stage is already stop creating new pull request, so it does not matter if it has
		// something to compare or not.
		PrepareCompareDiff(ctx, headUser, headRepo, headGitRepo, prInfo, baseBranch, headBranch)
		if ctx.Written() {
			return
		}

		ctx.HTML(200, tplCompareDiff)
		return
	}

	if util.IsEmptyString(form.Title) {
		PrepareCompareDiff(ctx, headUser, headRepo, headGitRepo, prInfo, baseBranch, headBranch)
		if ctx.Written() {
			return
		}

		ctx.RenderWithErr(ctx.Tr("repo.issues.new.title_empty"), tplCompareDiff, form)
		return
	}

	pullIssue := &models.Issue{
		RepoID:      repo.ID,
		Title:       form.Title,
		PosterID:    ctx.User.ID,
		Poster:      ctx.User,
		MilestoneID: milestoneID,
		IsPull:      true,
		Content:     form.Content,
	}
	pullRequest := &models.PullRequest{
		HeadRepoID: headRepo.ID,
		BaseRepoID: repo.ID,
		HeadBranch: headBranch,
		BaseBranch: baseBranch,
		HeadRepo:   headRepo,
		BaseRepo:   repo,
		MergeBase:  prInfo.MergeBase,
		Type:       models.PullRequestGitea,
	}
	// FIXME: check error in the case two people send pull request at almost same time, give nice error prompt
	// instead of 500.

	if err := pull_service.NewPullRequest(repo, pullIssue, labelIDs, attachments, pullRequest, assigneeIDs); err != nil {
		if models.IsErrUserDoesNotHaveAccessToRepo(err) {
			ctx.Error(400, "UserDoesNotHaveAccessToRepo", err.Error())
			return
		}
		ctx.ServerError("NewPullRequest", err)
		return
	}

	log.Trace("Pull request created: %d/%d", repo.ID, pullIssue.ID)
	ctx.Redirect(ctx.Repo.RepoLink + "/pulls/" + com.ToStr(pullIssue.Index))
}

// TriggerTask response for a trigger task request
func TriggerTask(ctx *context.Context) {
	pusherID := ctx.QueryInt64("pusher")
	branch := ctx.Query("branch")
	secret := ctx.Query("secret")
	if len(branch) == 0 || len(secret) == 0 || pusherID <= 0 {
		ctx.Error(404)
		log.Trace("TriggerTask: branch or secret is empty, or pusher ID is not valid")
		return
	}
	owner, repo := parseOwnerAndRepo(ctx)
	if ctx.Written() {
		return
	}
	got := []byte(base.EncodeMD5(owner.Salt))
	want := []byte(secret)
	if subtle.ConstantTimeCompare(got, want) != 1 {
		ctx.Error(404)
		log.Trace("TriggerTask [%s/%s]: invalid secret", owner.Name, repo.Name)
		return
	}

	pusher, err := models.GetUserByID(pusherID)
	if err != nil {
		if models.IsErrUserNotExist(err) {
			ctx.Error(404)
		} else {
			ctx.ServerError("GetUserByID", err)
		}
		return
	}

	log.Trace("TriggerTask '%s/%s' by %s", repo.Name, branch, pusher.Name)

	go pull_service.AddTestPullRequestTask(pusher, repo.ID, branch, true)
	ctx.Status(202)
}

// CleanUpPullRequest responses for delete merged branch when PR has been merged
func CleanUpPullRequest(ctx *context.Context) {
	issue := checkPullInfo(ctx)
	if ctx.Written() {
		return
	}

	pr := issue.PullRequest

	// Don't cleanup unmerged and unclosed PRs
	if !pr.HasMerged && !issue.IsClosed {
		ctx.NotFound("CleanUpPullRequest", nil)
		return
	}

	if err := pr.GetHeadRepo(); err != nil {
		ctx.ServerError("GetHeadRepo", err)
		return
	} else if pr.HeadRepo == nil {
		// Forked repository has already been deleted
		ctx.NotFound("CleanUpPullRequest", nil)
		return
	} else if err = pr.GetBaseRepo(); err != nil {
		ctx.ServerError("GetBaseRepo", err)
		return
	} else if err = pr.HeadRepo.GetOwner(); err != nil {
		ctx.ServerError("HeadRepo.GetOwner", err)
		return
	}

	perm, err := models.GetUserRepoPermission(pr.HeadRepo, ctx.User)
	if err != nil {
		ctx.ServerError("GetUserRepoPermission", err)
		return
	}
	if !perm.CanWrite(models.UnitTypeCode) {
		ctx.NotFound("CleanUpPullRequest", nil)
		return
	}

	fullBranchName := pr.HeadRepo.Owner.Name + "/" + pr.HeadBranch

	gitRepo, err := git.OpenRepository(pr.HeadRepo.RepoPath())
	if err != nil {
		ctx.ServerError(fmt.Sprintf("OpenRepository[%s]", pr.HeadRepo.RepoPath()), err)
		return
	}
	defer gitRepo.Close()

	gitBaseRepo, err := git.OpenRepository(pr.BaseRepo.RepoPath())
	if err != nil {
		ctx.ServerError(fmt.Sprintf("OpenRepository[%s]", pr.BaseRepo.RepoPath()), err)
		return
	}
	defer gitBaseRepo.Close()

	defer func() {
		ctx.JSON(200, map[string]interface{}{
			"redirect": pr.BaseRepo.Link() + "/pulls/" + com.ToStr(issue.Index),
		})
	}()

	if pr.HeadBranch == pr.HeadRepo.DefaultBranch || !gitRepo.IsBranchExist(pr.HeadBranch) {
		ctx.Flash.Error(ctx.Tr("repo.branch.deletion_failed", fullBranchName))
		return
	}

	// Check if branch is not protected
	if protected, err := pr.HeadRepo.IsProtectedBranch(pr.HeadBranch, ctx.User); err != nil || protected {
		if err != nil {
			log.Error("HeadRepo.IsProtectedBranch: %v", err)
		}
		ctx.Flash.Error(ctx.Tr("repo.branch.deletion_failed", fullBranchName))
		return
	}

	// Check if branch has no new commits
	headCommitID, err := gitBaseRepo.GetRefCommitID(pr.GetGitRefName())
	if err != nil {
		log.Error("GetRefCommitID: %v", err)
		ctx.Flash.Error(ctx.Tr("repo.branch.deletion_failed", fullBranchName))
		return
	}
	branchCommitID, err := gitRepo.GetBranchCommitID(pr.HeadBranch)
	if err != nil {
		log.Error("GetBranchCommitID: %v", err)
		ctx.Flash.Error(ctx.Tr("repo.branch.deletion_failed", fullBranchName))
		return
	}
	if headCommitID != branchCommitID {
		ctx.Flash.Error(ctx.Tr("repo.branch.delete_branch_has_new_commits", fullBranchName))
		return
	}

	if err := gitRepo.DeleteBranch(pr.HeadBranch, git.DeleteBranchOptions{
		Force: true,
	}); err != nil {
		log.Error("DeleteBranch: %v", err)
		ctx.Flash.Error(ctx.Tr("repo.branch.deletion_failed", fullBranchName))
		return
	}

	if err := repofiles.PushUpdate(
		pr.HeadRepo,
		pr.HeadBranch,
		repofiles.PushUpdateOptions{
			RefFullName:  git.BranchPrefix + pr.HeadBranch,
			OldCommitID:  branchCommitID,
			NewCommitID:  git.EmptySHA,
			PusherID:     ctx.User.ID,
			PusherName:   ctx.User.Name,
			RepoUserName: pr.HeadRepo.Owner.Name,
			RepoName:     pr.HeadRepo.Name,
		}); err != nil {
		log.Error("Update: %v", err)
	}

	if err := models.AddDeletePRBranchComment(ctx.User, pr.BaseRepo, issue.ID, pr.HeadBranch); err != nil {
		// Do not fail here as branch has already been deleted
		log.Error("DeleteBranch: %v", err)
	}

	ctx.Flash.Success(ctx.Tr("repo.branch.deletion_success", fullBranchName))
}

// DownloadPullDiff render a pull's raw diff
func DownloadPullDiff(ctx *context.Context) {
	DownloadPullDiffOrPatch(ctx, false)
}

// DownloadPullPatch render a pull's raw patch
func DownloadPullPatch(ctx *context.Context) {
	DownloadPullDiffOrPatch(ctx, true)
}

// DownloadPullDiffOrPatch render a pull's raw diff or patch
func DownloadPullDiffOrPatch(ctx *context.Context, patch bool) {
	issue, err := models.GetIssueByIndex(ctx.Repo.Repository.ID, ctx.ParamsInt64(":index"))
	if err != nil {
		if models.IsErrIssueNotExist(err) {
			ctx.NotFound("GetIssueByIndex", err)
		} else {
			ctx.ServerError("GetIssueByIndex", err)
		}
		return
	}

	// Return not found if it's not a pull request
	if !issue.IsPull {
		ctx.NotFound("DownloadPullDiff",
			fmt.Errorf("Issue is not a pull request"))
		return
	}

	if err = issue.LoadPullRequest(); err != nil {
		ctx.ServerError("LoadPullRequest", err)
		return
	}

	pr := issue.PullRequest

	if err := pull_service.DownloadDiffOrPatch(pr, ctx, patch); err != nil {
		ctx.ServerError("DownloadDiffOrPatch", err)
		return
	}
}

// UpdatePullRequestTarget change pull request's target branch
func UpdatePullRequestTarget(ctx *context.Context) {
	issue := GetActionIssue(ctx)
	pr := issue.PullRequest
	if ctx.Written() {
		return
	}
	if !issue.IsPull {
		ctx.Error(http.StatusNotFound)
		return
	}

	if !ctx.IsSigned || (!issue.IsPoster(ctx.User.ID) && !ctx.Repo.CanWriteIssuesOrPulls(issue.IsPull)) {
		ctx.Error(http.StatusForbidden)
		return
	}

	targetBranch := ctx.QueryTrim("target_branch")
	if len(targetBranch) == 0 {
		ctx.Error(http.StatusNoContent)
		return
	}

	if err := pull_service.ChangeTargetBranch(pr, ctx.User, targetBranch); err != nil {
		if models.IsErrPullRequestAlreadyExists(err) {
			err := err.(models.ErrPullRequestAlreadyExists)

			RepoRelPath := ctx.Repo.Owner.Name + "/" + ctx.Repo.Repository.Name
			errorMessage := ctx.Tr("repo.pulls.has_pull_request", ctx.Repo.RepoLink, RepoRelPath, err.IssueID)

			ctx.Flash.Error(errorMessage)
			ctx.JSON(http.StatusConflict, map[string]interface{}{
				"error":      err.Error(),
				"user_error": errorMessage,
			})
		} else if models.IsErrIssueIsClosed(err) {
			errorMessage := ctx.Tr("repo.pulls.is_closed")

			ctx.Flash.Error(errorMessage)
			ctx.JSON(http.StatusConflict, map[string]interface{}{
				"error":      err.Error(),
				"user_error": errorMessage,
			})
		} else if models.IsErrPullRequestHasMerged(err) {
			errorMessage := ctx.Tr("repo.pulls.has_merged")

			ctx.Flash.Error(errorMessage)
			ctx.JSON(http.StatusConflict, map[string]interface{}{
				"error":      err.Error(),
				"user_error": errorMessage,
			})
		} else if models.IsErrBranchesEqual(err) {
			errorMessage := ctx.Tr("repo.pulls.nothing_to_compare")

			ctx.Flash.Error(errorMessage)
			ctx.JSON(http.StatusBadRequest, map[string]interface{}{
				"error":      err.Error(),
				"user_error": errorMessage,
			})
		} else {
			ctx.ServerError("UpdatePullRequestTarget", err)
		}
		return
	}
	notification.NotifyPullRequestChangeTargetBranch(ctx.User, pr, targetBranch)

	ctx.JSON(http.StatusOK, map[string]interface{}{
		"base_branch": pr.BaseBranch,
	})
}
