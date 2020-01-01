// Copyright 2015 The Gogs Authors. All rights reserved.
// Use of this source code is governed by a MIT-style
// license that can be found in the LICENSE file.

package repo

import (
	"errors"
	"net/http"
	"time"

	"code.gitea.io/gitea/models"
	"code.gitea.io/gitea/modules/context"
	api "code.gitea.io/gitea/modules/structs"
	comment_service "code.gitea.io/gitea/services/comments"
)

// ListIssueComments list all the comments of an issue
func ListIssueComments(ctx *context.APIContext) {
	// swagger:operation GET /repos/{owner}/{repo}/issues/{index}/comments issue issueGetComments
	// ---
	// summary: List all comments on an issue
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
	//   description: index of the issue
	//   type: integer
	//   format: int64
	//   required: true
	// - name: since
	//   in: query
	//   description: if provided, only comments updated since the specified time are returned.
	//   type: string
	// responses:
	//   "200":
	//     "$ref": "#/responses/CommentList"

	var since time.Time
	if len(ctx.Query("since")) > 0 {
		since, _ = time.Parse(time.RFC3339, ctx.Query("since"))
	}

	// comments,err:=models.GetCommentsByIssueIDSince(, since)
	issue, err := models.GetIssueByIndex(ctx.Repo.Repository.ID, ctx.ParamsInt64(":index"))
	if err != nil {
		ctx.Error(http.StatusInternalServerError, "GetRawIssueByIndex", err)
		return
	}
	issue.Repo = ctx.Repo.Repository

	comments, err := models.FindComments(models.FindCommentsOptions{
		IssueID: issue.ID,
		Since:   since.Unix(),
		Type:    models.CommentTypeComment,
	})
	if err != nil {
		ctx.Error(http.StatusInternalServerError, "FindComments", err)
		return
	}

	if err := models.CommentList(comments).LoadPosters(); err != nil {
		ctx.Error(http.StatusInternalServerError, "LoadPosters", err)
		return
	}

	apiComments := make([]*api.Comment, len(comments))
	for i, comment := range comments {
		comment.Issue = issue
		apiComments[i] = comments[i].APIFormat()
	}
	ctx.JSON(http.StatusOK, &apiComments)
}

// ListRepoIssueComments returns all issue-comments for a repo
func ListRepoIssueComments(ctx *context.APIContext) {
	// swagger:operation GET /repos/{owner}/{repo}/issues/comments issue issueGetRepoComments
	// ---
	// summary: List all comments in a repository
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
	// - name: since
	//   in: query
	//   description: if provided, only comments updated since the provided time are returned.
	//   type: string
	// responses:
	//   "200":
	//     "$ref": "#/responses/CommentList"

	var since time.Time
	if len(ctx.Query("since")) > 0 {
		since, _ = time.Parse(time.RFC3339, ctx.Query("since"))
	}

	comments, err := models.FindComments(models.FindCommentsOptions{
		RepoID: ctx.Repo.Repository.ID,
		Since:  since.Unix(),
		Type:   models.CommentTypeComment,
	})
	if err != nil {
		ctx.Error(http.StatusInternalServerError, "FindComments", err)
		return
	}

	if err = models.CommentList(comments).LoadPosters(); err != nil {
		ctx.Error(http.StatusInternalServerError, "LoadPosters", err)
		return
	}

	apiComments := make([]*api.Comment, len(comments))
	if err := models.CommentList(comments).LoadIssues(); err != nil {
		ctx.Error(http.StatusInternalServerError, "LoadIssues", err)
		return
	}
	if err := models.CommentList(comments).LoadPosters(); err != nil {
		ctx.Error(http.StatusInternalServerError, "LoadPosters", err)
		return
	}
	if _, err := models.CommentList(comments).Issues().LoadRepositories(); err != nil {
		ctx.Error(http.StatusInternalServerError, "LoadRepositories", err)
		return
	}
	for i := range comments {
		apiComments[i] = comments[i].APIFormat()
	}
	ctx.JSON(http.StatusOK, &apiComments)
}

// CreateIssueComment create a comment for an issue
func CreateIssueComment(ctx *context.APIContext, form api.CreateIssueCommentOption) {
	// swagger:operation POST /repos/{owner}/{repo}/issues/{index}/comments issue issueCreateComment
	// ---
	// summary: Add a comment to an issue
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
	//   description: index of the issue
	//   type: integer
	//   format: int64
	//   required: true
	// - name: body
	//   in: body
	//   schema:
	//     "$ref": "#/definitions/CreateIssueCommentOption"
	// responses:
	//   "201":
	//     "$ref": "#/responses/Comment"
	//   "403":
	//     "$ref": "#/responses/forbidden"

	issue, err := models.GetIssueByIndex(ctx.Repo.Repository.ID, ctx.ParamsInt64(":index"))
	if err != nil {
		ctx.Error(http.StatusInternalServerError, "GetIssueByIndex", err)
		return
	}

	if issue.IsLocked && !ctx.Repo.CanWrite(models.UnitTypeIssues) && !ctx.User.IsAdmin {
		ctx.Error(http.StatusForbidden, "CreateIssueComment", errors.New(ctx.Tr("repo.issues.comment_on_locked")))
		return
	}

	comment, err := comment_service.CreateIssueComment(ctx.User, ctx.Repo.Repository, issue, form.Body, nil)
	if err != nil {
		ctx.Error(http.StatusInternalServerError, "CreateIssueComment", err)
		return
	}

	ctx.JSON(http.StatusCreated, comment.APIFormat())
}

// EditIssueComment modify a comment of an issue
func EditIssueComment(ctx *context.APIContext, form api.EditIssueCommentOption) {
	// swagger:operation PATCH /repos/{owner}/{repo}/issues/comments/{id} issue issueEditComment
	// ---
	// summary: Edit a comment
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
	// - name: id
	//   in: path
	//   description: id of the comment to edit
	//   type: integer
	//   format: int64
	//   required: true
	// - name: body
	//   in: body
	//   schema:
	//     "$ref": "#/definitions/EditIssueCommentOption"
	// responses:
	//   "200":
	//     "$ref": "#/responses/Comment"
	editIssueComment(ctx, form)
}

// EditIssueCommentDeprecated modify a comment of an issue
func EditIssueCommentDeprecated(ctx *context.APIContext, form api.EditIssueCommentOption) {
	// swagger:operation PATCH /repos/{owner}/{repo}/issues/{index}/comments/{id} issue issueEditCommentDeprecated
	// ---
	// summary: Edit a comment
	// deprecated: true
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
	//   description: this parameter is ignored
	//   type: integer
	//   required: true
	// - name: id
	//   in: path
	//   description: id of the comment to edit
	//   type: integer
	//   format: int64
	//   required: true
	// - name: body
	//   in: body
	//   schema:
	//     "$ref": "#/definitions/EditIssueCommentOption"
	// responses:
	//   "200":
	//     "$ref": "#/responses/Comment"
	//   "204":
	//     "$ref": "#/responses/empty"
	//   "403":
	//     "$ref": "#/responses/forbidden"

	editIssueComment(ctx, form)
}

func editIssueComment(ctx *context.APIContext, form api.EditIssueCommentOption) {
	comment, err := models.GetCommentByID(ctx.ParamsInt64(":id"))
	if err != nil {
		if models.IsErrCommentNotExist(err) {
			ctx.NotFound(err)
		} else {
			ctx.Error(http.StatusInternalServerError, "GetCommentByID", err)
		}
		return
	}

	if !ctx.IsSigned || (ctx.User.ID != comment.PosterID && !ctx.Repo.IsAdmin()) {
		ctx.Status(http.StatusForbidden)
		return
	} else if comment.Type != models.CommentTypeComment {
		ctx.Status(http.StatusNoContent)
		return
	}

	oldContent := comment.Content
	comment.Content = form.Body
	if err := comment_service.UpdateComment(comment, ctx.User, oldContent); err != nil {
		ctx.Error(http.StatusInternalServerError, "UpdateComment", err)
		return
	}

	ctx.JSON(http.StatusOK, comment.APIFormat())
}

// DeleteIssueComment delete a comment from an issue
func DeleteIssueComment(ctx *context.APIContext) {
	// swagger:operation DELETE /repos/{owner}/{repo}/issues/comments/{id} issue issueDeleteComment
	// ---
	// summary: Delete a comment
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
	// - name: id
	//   in: path
	//   description: id of comment to delete
	//   type: integer
	//   format: int64
	//   required: true
	// responses:
	//   "204":
	//     "$ref": "#/responses/empty"
	//   "403":
	//     "$ref": "#/responses/forbidden"

	deleteIssueComment(ctx)
}

// DeleteIssueCommentDeprecated delete a comment from an issue
func DeleteIssueCommentDeprecated(ctx *context.APIContext) {
	// swagger:operation DELETE /repos/{owner}/{repo}/issues/{index}/comments/{id} issue issueDeleteCommentDeprecated
	// ---
	// summary: Delete a comment
	// deprecated: true
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
	//   description: this parameter is ignored
	//   type: integer
	//   required: true
	// - name: id
	//   in: path
	//   description: id of comment to delete
	//   type: integer
	//   format: int64
	//   required: true
	// responses:
	//   "204":
	//     "$ref": "#/responses/empty"
	//   "403":
	//     "$ref": "#/responses/forbidden"

	deleteIssueComment(ctx)
}

func deleteIssueComment(ctx *context.APIContext) {
	comment, err := models.GetCommentByID(ctx.ParamsInt64(":id"))
	if err != nil {
		if models.IsErrCommentNotExist(err) {
			ctx.NotFound(err)
		} else {
			ctx.Error(http.StatusInternalServerError, "GetCommentByID", err)
		}
		return
	}

	if !ctx.IsSigned || (ctx.User.ID != comment.PosterID && !ctx.Repo.IsAdmin()) {
		ctx.Status(http.StatusForbidden)
		return
	} else if comment.Type != models.CommentTypeComment {
		ctx.Status(http.StatusNoContent)
		return
	}

	if err = comment_service.DeleteComment(comment, ctx.User); err != nil {
		ctx.Error(http.StatusInternalServerError, "DeleteCommentByID", err)
		return
	}

	ctx.Status(http.StatusNoContent)
}
