// Copyright 2016 The Gitea Authors. All rights reserved.
// Use of this source code is governed by a MIT-style
// license that can be found in the LICENSE file.

package repo

import (
	"fmt"
	"net/http"
	"strings"
	"time"

	"code.gitea.io/gitea/models"
	"code.gitea.io/gitea/modules/auth"
	"code.gitea.io/gitea/modules/context"
	"code.gitea.io/gitea/modules/git"
	"code.gitea.io/gitea/modules/log"
	"code.gitea.io/gitea/modules/notification"
	api "code.gitea.io/gitea/modules/structs"
	"code.gitea.io/gitea/modules/timeutil"
	issue_service "code.gitea.io/gitea/services/issue"
	pull_service "code.gitea.io/gitea/services/pull"
)

// ListPullRequests returns a list of all PRs
func ListPullRequests(ctx *context.APIContext, form api.ListPullRequestsOptions) {
	// swagger:operation GET /repos/{owner}/{repo}/pulls repository repoListPullRequests
	// ---
	// summary: List a repo's pull requests
	// produces:
	// - application/json
	// parameters:
	// - name: owner
	//   in: path
	//   description: owner of the repo
	//   type: string
	//   required: true
	// - name: repo
	//   in: path
	//   description: name of the repo
	//   type: string
	//   required: true
	// - name: page
	//   in: query
	//   description: Page number
	//   type: integer
	// - name: state
	//   in: query
	//   description: "State of pull request: open or closed (optional)"
	//   type: string
	//   enum: [closed, open, all]
	// - name: sort
	//   in: query
	//   description: "Type of sort"
	//   type: string
	//   enum: [oldest, recentupdate, leastupdate, mostcomment, leastcomment, priority]
	// - name: milestone
	//   in: query
	//   description: "ID of the milestone"
	//   type: integer
	//   format: int64
	// - name: labels
	//   in: query
	//   description: "Label IDs"
	//   type: array
	//   collectionFormat: multi
	//   items:
	//     type: integer
	//     format: int64
	// responses:
	//   "200":
	//     "$ref": "#/responses/PullRequestList"

	prs, maxResults, err := models.PullRequests(ctx.Repo.Repository.ID, &models.PullRequestsOptions{
		Page:        ctx.QueryInt("page"),
		State:       ctx.QueryTrim("state"),
		SortType:    ctx.QueryTrim("sort"),
		Labels:      ctx.QueryStrings("labels"),
		MilestoneID: ctx.QueryInt64("milestone"),
	})

	if err != nil {
		ctx.Error(http.StatusInternalServerError, "PullRequests", err)
		return
	}

	apiPrs := make([]*api.PullRequest, len(prs))
	for i := range prs {
		if err = prs[i].LoadIssue(); err != nil {
			ctx.Error(http.StatusInternalServerError, "LoadIssue", err)
			return
		}
		if err = prs[i].LoadAttributes(); err != nil {
			ctx.Error(http.StatusInternalServerError, "LoadAttributes", err)
			return
		}
		if err = prs[i].GetBaseRepo(); err != nil {
			ctx.Error(http.StatusInternalServerError, "GetBaseRepo", err)
			return
		}
		if err = prs[i].GetHeadRepo(); err != nil {
			ctx.Error(http.StatusInternalServerError, "GetHeadRepo", err)
			return
		}
		apiPrs[i] = prs[i].APIFormat()
	}

	ctx.SetLinkHeader(int(maxResults), models.ItemsPerPage)
	ctx.JSON(http.StatusOK, &apiPrs)
}

// GetPullRequest returns a single PR based on index
func GetPullRequest(ctx *context.APIContext) {
	// swagger:operation GET /repos/{owner}/{repo}/pulls/{index} repository repoGetPullRequest
	// ---
	// summary: Get a pull request
	// produces:
	// - application/json
	// parameters:
	// - name: owner
	//   in: path
	//   description: owner of the repo
	//   type: string
	//   required: true
	// - name: repo
	//   in: path
	//   description: name of the repo
	//   type: string
	//   required: true
	// - name: index
	//   in: path
	//   description: index of the pull request to get
	//   type: integer
	//   format: int64
	//   required: true
	// responses:
	//   "200":
	//     "$ref": "#/responses/PullRequest"

	pr, err := models.GetPullRequestByIndex(ctx.Repo.Repository.ID, ctx.ParamsInt64(":index"))
	if err != nil {
		if models.IsErrPullRequestNotExist(err) {
			ctx.NotFound()
		} else {
			ctx.Error(http.StatusInternalServerError, "GetPullRequestByIndex", err)
		}
		return
	}

	if err = pr.GetBaseRepo(); err != nil {
		ctx.Error(http.StatusInternalServerError, "GetBaseRepo", err)
		return
	}
	if err = pr.GetHeadRepo(); err != nil {
		ctx.Error(http.StatusInternalServerError, "GetHeadRepo", err)
		return
	}
	ctx.JSON(http.StatusOK, pr.APIFormat())
}

// CreatePullRequest does what it says
func CreatePullRequest(ctx *context.APIContext, form api.CreatePullRequestOption) {
	// swagger:operation POST /repos/{owner}/{repo}/pulls repository repoCreatePullRequest
	// ---
	// summary: Create a pull request
	// consumes:
	// - application/json
	// produces:
	// - application/json
	// parameters:
	// - name: owner
	//   in: path
	//   description: owner of the repo
	//   type: string
	//   required: true
	// - name: repo
	//   in: path
	//   description: name of the repo
	//   type: string
	//   required: true
	// - name: body
	//   in: body
	//   schema:
	//     "$ref": "#/definitions/CreatePullRequestOption"
	// responses:
	//   "201":
	//     "$ref": "#/responses/PullRequest"
	//   "409":
	//     "$ref": "#/responses/error"
	//   "422":
	//     "$ref": "#/responses/validationError"

	var (
		repo        = ctx.Repo.Repository
		labelIDs    []int64
		assigneeID  int64
		milestoneID int64
	)

	// Get repo/branch information
	_, headRepo, headGitRepo, compareInfo, baseBranch, headBranch := parseCompareInfo(ctx, form)
	if ctx.Written() {
		return
	}
	defer headGitRepo.Close()

	// Check if another PR exists with the same targets
	existingPr, err := models.GetUnmergedPullRequest(headRepo.ID, ctx.Repo.Repository.ID, headBranch, baseBranch)
	if err != nil {
		if !models.IsErrPullRequestNotExist(err) {
			ctx.Error(http.StatusInternalServerError, "GetUnmergedPullRequest", err)
			return
		}
	} else {
		err = models.ErrPullRequestAlreadyExists{
			ID:         existingPr.ID,
			IssueID:    existingPr.Index,
			HeadRepoID: existingPr.HeadRepoID,
			BaseRepoID: existingPr.BaseRepoID,
			HeadBranch: existingPr.HeadBranch,
			BaseBranch: existingPr.BaseBranch,
		}
		ctx.Error(http.StatusConflict, "GetUnmergedPullRequest", err)
		return
	}

	if len(form.Labels) > 0 {
		labels, err := models.GetLabelsInRepoByIDs(ctx.Repo.Repository.ID, form.Labels)
		if err != nil {
			ctx.Error(http.StatusInternalServerError, "GetLabelsInRepoByIDs", err)
			return
		}

		labelIDs = make([]int64, len(labels))
		for i := range labels {
			labelIDs[i] = labels[i].ID
		}
	}

	if form.Milestone > 0 {
		milestone, err := models.GetMilestoneByRepoID(ctx.Repo.Repository.ID, milestoneID)
		if err != nil {
			if models.IsErrMilestoneNotExist(err) {
				ctx.NotFound()
			} else {
				ctx.Error(http.StatusInternalServerError, "GetMilestoneByRepoID", err)
			}
			return
		}

		milestoneID = milestone.ID
	}

	var deadlineUnix timeutil.TimeStamp
	if form.Deadline != nil {
		deadlineUnix = timeutil.TimeStamp(form.Deadline.Unix())
	}

	prIssue := &models.Issue{
		RepoID:       repo.ID,
		Title:        form.Title,
		PosterID:     ctx.User.ID,
		Poster:       ctx.User,
		MilestoneID:  milestoneID,
		AssigneeID:   assigneeID,
		IsPull:       true,
		Content:      form.Body,
		DeadlineUnix: deadlineUnix,
	}
	pr := &models.PullRequest{
		HeadRepoID: headRepo.ID,
		BaseRepoID: repo.ID,
		HeadBranch: headBranch,
		BaseBranch: baseBranch,
		HeadRepo:   headRepo,
		BaseRepo:   repo,
		MergeBase:  compareInfo.MergeBase,
		Type:       models.PullRequestGitea,
	}

	// Get all assignee IDs
	assigneeIDs, err := models.MakeIDsFromAPIAssigneesToAdd(form.Assignee, form.Assignees)
	if err != nil {
		if models.IsErrUserNotExist(err) {
			ctx.Error(http.StatusUnprocessableEntity, "", fmt.Sprintf("Assignee does not exist: [name: %s]", err))
		} else {
			ctx.Error(http.StatusInternalServerError, "AddAssigneeByName", err)
		}
		return
	}
	// Check if the passed assignees is assignable
	for _, aID := range assigneeIDs {
		assignee, err := models.GetUserByID(aID)
		if err != nil {
			ctx.Error(http.StatusInternalServerError, "GetUserByID", err)
			return
		}

		valid, err := models.CanBeAssigned(assignee, repo, true)
		if err != nil {
			ctx.Error(http.StatusInternalServerError, "canBeAssigned", err)
			return
		}
		if !valid {
			ctx.Error(http.StatusUnprocessableEntity, "canBeAssigned", models.ErrUserDoesNotHaveAccessToRepo{UserID: aID, RepoName: repo.Name})
			return
		}
	}

	if err := pull_service.NewPullRequest(repo, prIssue, labelIDs, []string{}, pr, assigneeIDs); err != nil {
		if models.IsErrUserDoesNotHaveAccessToRepo(err) {
			ctx.Error(http.StatusBadRequest, "UserDoesNotHaveAccessToRepo", err)
			return
		}
		ctx.Error(http.StatusInternalServerError, "NewPullRequest", err)
		return
	}

	notification.NotifyNewPullRequest(pr)

	log.Trace("Pull request created: %d/%d", repo.ID, prIssue.ID)
	ctx.JSON(http.StatusCreated, pr.APIFormat())
}

// EditPullRequest does what it says
func EditPullRequest(ctx *context.APIContext, form api.EditPullRequestOption) {
	// swagger:operation PATCH /repos/{owner}/{repo}/pulls/{index} repository repoEditPullRequest
	// ---
	// summary: Update a pull request. If using deadline only the date will be taken into account, and time of day ignored.
	// consumes:
	// - application/json
	// produces:
	// - application/json
	// parameters:
	// - name: owner
	//   in: path
	//   description: owner of the repo
	//   type: string
	//   required: true
	// - name: repo
	//   in: path
	//   description: name of the repo
	//   type: string
	//   required: true
	// - name: index
	//   in: path
	//   description: index of the pull request to edit
	//   type: integer
	//   format: int64
	//   required: true
	// - name: body
	//   in: body
	//   schema:
	//     "$ref": "#/definitions/EditPullRequestOption"
	// responses:
	//   "201":
	//     "$ref": "#/responses/PullRequest"
	//   "403":
	//     "$ref": "#/responses/forbidden"
	//   "412":
	//     "$ref": "#/responses/error"
	//   "422":
	//     "$ref": "#/responses/validationError"

	pr, err := models.GetPullRequestByIndex(ctx.Repo.Repository.ID, ctx.ParamsInt64(":index"))
	if err != nil {
		if models.IsErrPullRequestNotExist(err) {
			ctx.NotFound()
		} else {
			ctx.Error(http.StatusInternalServerError, "GetPullRequestByIndex", err)
		}
		return
	}

	err = pr.LoadIssue()
	if err != nil {
		ctx.Error(http.StatusInternalServerError, "LoadIssue", err)
		return
	}
	issue := pr.Issue
	issue.Repo = ctx.Repo.Repository

	if !issue.IsPoster(ctx.User.ID) && !ctx.Repo.CanWrite(models.UnitTypePullRequests) {
		ctx.Status(http.StatusForbidden)
		return
	}

	if len(form.Title) > 0 {
		issue.Title = form.Title
	}
	if len(form.Body) > 0 {
		issue.Content = form.Body
	}

	// Update or remove deadline if set
	if form.Deadline != nil || form.RemoveDeadline != nil {
		var deadlineUnix timeutil.TimeStamp
		if (form.RemoveDeadline == nil || !*form.RemoveDeadline) && !form.Deadline.IsZero() {
			deadline := time.Date(form.Deadline.Year(), form.Deadline.Month(), form.Deadline.Day(),
				23, 59, 59, 0, form.Deadline.Location())
			deadlineUnix = timeutil.TimeStamp(deadline.Unix())
		}

		if err := models.UpdateIssueDeadline(issue, deadlineUnix, ctx.User); err != nil {
			ctx.Error(http.StatusInternalServerError, "UpdateIssueDeadline", err)
			return
		}
		issue.DeadlineUnix = deadlineUnix
	}

	// Add/delete assignees

	// Deleting is done the GitHub way (quote from their api documentation):
	// https://developer.github.com/v3/issues/#edit-an-issue
	// "assignees" (array): Logins for Users to assign to this issue.
	// Pass one or more user logins to replace the set of assignees on this Issue.
	// Send an empty array ([]) to clear all assignees from the Issue.

	if ctx.Repo.CanWrite(models.UnitTypePullRequests) && (form.Assignees != nil || len(form.Assignee) > 0) {
		err = issue_service.UpdateAssignees(issue, form.Assignee, form.Assignees, ctx.User)
		if err != nil {
			if models.IsErrUserNotExist(err) {
				ctx.Error(http.StatusUnprocessableEntity, "", fmt.Sprintf("Assignee does not exist: [name: %s]", err))
			} else {
				ctx.Error(http.StatusInternalServerError, "UpdateAssignees", err)
			}
			return
		}
	}

	if ctx.Repo.CanWrite(models.UnitTypePullRequests) && form.Milestone != 0 &&
		issue.MilestoneID != form.Milestone {
		oldMilestoneID := issue.MilestoneID
		issue.MilestoneID = form.Milestone
		if err = issue_service.ChangeMilestoneAssign(issue, ctx.User, oldMilestoneID); err != nil {
			ctx.Error(http.StatusInternalServerError, "ChangeMilestoneAssign", err)
			return
		}
	}

	if ctx.Repo.CanWrite(models.UnitTypePullRequests) && form.Labels != nil {
		labels, err := models.GetLabelsInRepoByIDs(ctx.Repo.Repository.ID, form.Labels)
		if err != nil {
			ctx.Error(http.StatusInternalServerError, "GetLabelsInRepoByIDsError", err)
			return
		}
		if err = issue.ReplaceLabels(labels, ctx.User); err != nil {
			ctx.Error(http.StatusInternalServerError, "ReplaceLabelsError", err)
			return
		}
	}

	if err = models.UpdateIssue(issue); err != nil {
		ctx.Error(http.StatusInternalServerError, "UpdateIssue", err)
		return
	}
	if form.State != nil {
		if err = issue_service.ChangeStatus(issue, ctx.User, api.StateClosed == api.StateType(*form.State)); err != nil {
			if models.IsErrDependenciesLeft(err) {
				ctx.Error(http.StatusPreconditionFailed, "DependenciesLeft", "cannot close this pull request because it still has open dependencies")
				return
			}
			ctx.Error(http.StatusInternalServerError, "ChangeStatus", err)
			return
		}
	}

	// Refetch from database
	pr, err = models.GetPullRequestByIndex(ctx.Repo.Repository.ID, pr.Index)
	if err != nil {
		if models.IsErrPullRequestNotExist(err) {
			ctx.NotFound()
		} else {
			ctx.Error(http.StatusInternalServerError, "GetPullRequestByIndex", err)
		}
		return
	}

	// TODO this should be 200, not 201
	ctx.JSON(http.StatusCreated, pr.APIFormat())
}

// IsPullRequestMerged checks if a PR exists given an index
func IsPullRequestMerged(ctx *context.APIContext) {
	// swagger:operation GET /repos/{owner}/{repo}/pulls/{index}/merge repository repoPullRequestIsMerged
	// ---
	// summary: Check if a pull request has been merged
	// produces:
	// - application/json
	// parameters:
	// - name: owner
	//   in: path
	//   description: owner of the repo
	//   type: string
	//   required: true
	// - name: repo
	//   in: path
	//   description: name of the repo
	//   type: string
	//   required: true
	// - name: index
	//   in: path
	//   description: index of the pull request
	//   type: integer
	//   format: int64
	//   required: true
	// responses:
	//   "204":
	//     description: pull request has been merged
	//   "404":
	//     description: pull request has not been merged

	pr, err := models.GetPullRequestByIndex(ctx.Repo.Repository.ID, ctx.ParamsInt64(":index"))
	if err != nil {
		if models.IsErrPullRequestNotExist(err) {
			ctx.NotFound()
		} else {
			ctx.Error(http.StatusInternalServerError, "GetPullRequestByIndex", err)
		}
		return
	}

	if pr.HasMerged {
		ctx.Status(http.StatusNoContent)
	}
	ctx.NotFound()
}

// MergePullRequest merges a PR given an index
func MergePullRequest(ctx *context.APIContext, form auth.MergePullRequestForm) {
	// swagger:operation POST /repos/{owner}/{repo}/pulls/{index}/merge repository repoMergePullRequest
	// ---
	// summary: Merge a pull request
	// produces:
	// - application/json
	// parameters:
	// - name: owner
	//   in: path
	//   description: owner of the repo
	//   type: string
	//   required: true
	// - name: repo
	//   in: path
	//   description: name of the repo
	//   type: string
	//   required: true
	// - name: index
	//   in: path
	//   description: index of the pull request to merge
	//   type: integer
	//   format: int64
	//   required: true
	// - name: body
	//   in: body
	//   schema:
	//     $ref: "#/definitions/MergePullRequestOption"
	// responses:
	//   "200":
	//     "$ref": "#/responses/empty"
	//   "405":
	//     "$ref": "#/responses/empty"
	//   "409":
	//     "$ref": "#/responses/error"

	pr, err := models.GetPullRequestByIndex(ctx.Repo.Repository.ID, ctx.ParamsInt64(":index"))
	if err != nil {
		if models.IsErrPullRequestNotExist(err) {
			ctx.NotFound("GetPullRequestByIndex", err)
		} else {
			ctx.Error(http.StatusInternalServerError, "GetPullRequestByIndex", err)
		}
		return
	}

	if err = pr.GetHeadRepo(); err != nil {
		ctx.ServerError("GetHeadRepo", err)
		return
	}

	err = pr.LoadIssue()
	if err != nil {
		ctx.Error(http.StatusInternalServerError, "LoadIssue", err)
		return
	}
	pr.Issue.Repo = ctx.Repo.Repository

	if ctx.IsSigned {
		// Update issue-user.
		if err = pr.Issue.ReadBy(ctx.User.ID); err != nil {
			ctx.Error(http.StatusInternalServerError, "ReadBy", err)
			return
		}
	}

	if pr.Issue.IsClosed {
		ctx.NotFound()
		return
	}

	if !pr.CanAutoMerge() || pr.HasMerged || pr.IsWorkInProgress() {
		ctx.Status(http.StatusMethodNotAllowed)
		return
	}

	isPass, err := pull_service.IsPullCommitStatusPass(pr)
	if err != nil {
		ctx.Error(http.StatusInternalServerError, "IsPullCommitStatusPass", err)
		return
	}

	if !isPass && !ctx.IsUserRepoAdmin() {
		ctx.Status(http.StatusMethodNotAllowed)
		return
	}

	if len(form.Do) == 0 {
		form.Do = string(models.MergeStyleMerge)
	}

	message := strings.TrimSpace(form.MergeTitleField)
	if len(message) == 0 {
		if models.MergeStyle(form.Do) == models.MergeStyleMerge {
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

	if err := pull_service.Merge(pr, ctx.User, ctx.Repo.GitRepo, models.MergeStyle(form.Do), message); err != nil {
		if models.IsErrInvalidMergeStyle(err) {
			ctx.Status(http.StatusMethodNotAllowed)
			return
		} else if models.IsErrMergeConflicts(err) {
			conflictError := err.(models.ErrMergeConflicts)
			ctx.JSON(http.StatusConflict, conflictError)
		} else if models.IsErrRebaseConflicts(err) {
			conflictError := err.(models.ErrRebaseConflicts)
			ctx.JSON(http.StatusConflict, conflictError)
		} else if models.IsErrMergeUnrelatedHistories(err) {
			conflictError := err.(models.ErrMergeUnrelatedHistories)
			ctx.JSON(http.StatusConflict, conflictError)
		} else if models.IsErrMergePushOutOfDate(err) {
			ctx.Error(http.StatusConflict, "Merge", "merge push out of date")
			return
		}
		ctx.Error(http.StatusInternalServerError, "Merge", err)
		return
	}

	log.Trace("Pull request merged: %d", pr.ID)
	ctx.Status(http.StatusOK)
}

func parseCompareInfo(ctx *context.APIContext, form api.CreatePullRequestOption) (*models.User, *models.Repository, *git.Repository, *git.CompareInfo, string, string) {
	baseRepo := ctx.Repo.Repository

	// Get compared branches information
	// format: <base branch>...[<head repo>:]<head branch>
	// base<-head: master...head:feature
	// same repo: master...feature

	// TODO: Validate form first?

	baseBranch := form.Base

	var (
		headUser   *models.User
		headBranch string
		isSameRepo bool
		err        error
	)

	// If there is no head repository, it means pull request between same repository.
	headInfos := strings.Split(form.Head, ":")
	if len(headInfos) == 1 {
		isSameRepo = true
		headUser = ctx.Repo.Owner
		headBranch = headInfos[0]

	} else if len(headInfos) == 2 {
		headUser, err = models.GetUserByName(headInfos[0])
		if err != nil {
			if models.IsErrUserNotExist(err) {
				ctx.NotFound("GetUserByName")
			} else {
				ctx.ServerError("GetUserByName", err)
			}
			return nil, nil, nil, nil, "", ""
		}
		headBranch = headInfos[1]

	} else {
		ctx.NotFound()
		return nil, nil, nil, nil, "", ""
	}

	ctx.Repo.PullRequest.SameRepo = isSameRepo
	log.Info("Base branch: %s", baseBranch)
	log.Info("Repo path: %s", ctx.Repo.GitRepo.Path)
	// Check if base branch is valid.
	if !ctx.Repo.GitRepo.IsBranchExist(baseBranch) {
		ctx.NotFound("IsBranchExist")
		return nil, nil, nil, nil, "", ""
	}

	// Check if current user has fork of repository or in the same repository.
	headRepo, has := models.HasForkedRepo(headUser.ID, baseRepo.ID)
	if !has && !isSameRepo {
		log.Trace("parseCompareInfo[%d]: does not have fork or in same repository", baseRepo.ID)
		ctx.NotFound("HasForkedRepo")
		return nil, nil, nil, nil, "", ""
	}

	var headGitRepo *git.Repository
	if isSameRepo {
		headRepo = ctx.Repo.Repository
		headGitRepo = ctx.Repo.GitRepo
	} else {
		headGitRepo, err = git.OpenRepository(models.RepoPath(headUser.Name, headRepo.Name))
		if err != nil {
			ctx.Error(http.StatusInternalServerError, "OpenRepository", err)
			return nil, nil, nil, nil, "", ""
		}
	}

	// user should have permission to read baseRepo's codes and pulls, NOT headRepo's
	permBase, err := models.GetUserRepoPermission(baseRepo, ctx.User)
	if err != nil {
		headGitRepo.Close()
		ctx.ServerError("GetUserRepoPermission", err)
		return nil, nil, nil, nil, "", ""
	}
	if !permBase.CanReadIssuesOrPulls(true) || !permBase.CanRead(models.UnitTypeCode) {
		if log.IsTrace() {
			log.Trace("Permission Denied: User %-v cannot create/read pull requests or cannot read code in Repo %-v\nUser in baseRepo has Permissions: %-+v",
				ctx.User,
				baseRepo,
				permBase)
		}
		headGitRepo.Close()
		ctx.NotFound("Can't read pulls or can't read UnitTypeCode")
		return nil, nil, nil, nil, "", ""
	}

	// user should have permission to read headrepo's codes
	permHead, err := models.GetUserRepoPermission(headRepo, ctx.User)
	if err != nil {
		headGitRepo.Close()
		ctx.ServerError("GetUserRepoPermission", err)
		return nil, nil, nil, nil, "", ""
	}
	if !permHead.CanRead(models.UnitTypeCode) {
		if log.IsTrace() {
			log.Trace("Permission Denied: User: %-v cannot read code in Repo: %-v\nUser in headRepo has Permissions: %-+v",
				ctx.User,
				headRepo,
				permHead)
		}
		headGitRepo.Close()
		ctx.NotFound("Can't read headRepo UnitTypeCode")
		return nil, nil, nil, nil, "", ""
	}

	// Check if head branch is valid.
	if !headGitRepo.IsBranchExist(headBranch) {
		headGitRepo.Close()
		ctx.NotFound()
		return nil, nil, nil, nil, "", ""
	}

	compareInfo, err := headGitRepo.GetCompareInfo(models.RepoPath(baseRepo.Owner.Name, baseRepo.Name), baseBranch, headBranch)
	if err != nil {
		headGitRepo.Close()
		ctx.Error(http.StatusInternalServerError, "GetCompareInfo", err)
		return nil, nil, nil, nil, "", ""
	}

	return headUser, headRepo, headGitRepo, compareInfo, baseBranch, headBranch
}
