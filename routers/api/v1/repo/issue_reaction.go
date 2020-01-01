// Copyright 2019 The Gitea Authors. All rights reserved.
// Use of this source code is governed by a MIT-style
// license that can be found in the LICENSE file.

package repo

import (
	"errors"
	"net/http"

	"code.gitea.io/gitea/models"
	"code.gitea.io/gitea/modules/context"
	api "code.gitea.io/gitea/modules/structs"
)

// GetIssueCommentReactions list reactions of a issue comment
func GetIssueCommentReactions(ctx *context.APIContext) {
	// swagger:operation GET /repos/{owner}/{repo}/issues/comments/{id}/reactions issue issueGetCommentReactions
	// ---
	// summary: Get a list reactions of a issue comment
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
	// responses:
	//   "200":
	//     "$ref": "#/responses/ReactionResponseList"
	//   "403":
	//     "$ref": "#/responses/forbidden"

	comment, err := models.GetCommentByID(ctx.ParamsInt64(":id"))
	if err != nil {
		if models.IsErrCommentNotExist(err) {
			ctx.NotFound(err)
		} else {
			ctx.Error(http.StatusInternalServerError, "GetCommentByID", err)
		}
		return
	}

	if !ctx.Repo.CanRead(models.UnitTypeIssues) && !ctx.User.IsAdmin {
		ctx.Error(http.StatusForbidden, "GetIssueCommentReactions", errors.New("no permission to get reactions"))
		return
	}

	reactions, err := models.FindCommentReactions(comment)
	if err != nil {
		ctx.Error(http.StatusInternalServerError, "FindIssueReactions", err)
		return
	}
	_, err = reactions.LoadUsers()
	if err != nil {
		ctx.Error(http.StatusInternalServerError, "ReactionList.LoadUsers()", err)
		return
	}

	var result []api.ReactionResponse
	for _, r := range reactions {
		result = append(result, api.ReactionResponse{
			User:     r.User.APIFormat(),
			Reaction: r.Type,
			Created:  r.CreatedUnix.AsTime(),
		})
	}

	ctx.JSON(http.StatusOK, result)
}

// PostIssueCommentReaction add a reaction to a comment of a issue
func PostIssueCommentReaction(ctx *context.APIContext, form api.EditReactionOption) {
	// swagger:operation POST /repos/{owner}/{repo}/issues/comments/{id}/reactions issue issuePostCommentReaction
	// ---
	// summary: Add a reaction to a comment of a issue comment
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
	// - name: content
	//   in: body
	//   schema:
	//     "$ref": "#/definitions/EditReactionOption"
	// responses:
	//   "201":
	//     "$ref": "#/responses/ReactionResponse"
	//   "403":
	//     "$ref": "#/responses/forbidden"

	changeIssueCommentReaction(ctx, form, true)
}

// DeleteIssueCommentReaction list reactions of a issue comment
func DeleteIssueCommentReaction(ctx *context.APIContext, form api.EditReactionOption) {
	// swagger:operation DELETE /repos/{owner}/{repo}/issues/comments/{id}/reactions issue issueDeleteCommentReaction
	// ---
	// summary: Remove a reaction from a comment of a issue comment
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
	// - name: content
	//   in: body
	//   schema:
	//     "$ref": "#/definitions/EditReactionOption"
	// responses:
	//   "200":
	//     "$ref": "#/responses/empty"
	//   "403":
	//     "$ref": "#/responses/forbidden"

	changeIssueCommentReaction(ctx, form, false)
}

func changeIssueCommentReaction(ctx *context.APIContext, form api.EditReactionOption, isCreateType bool) {
	comment, err := models.GetCommentByID(ctx.ParamsInt64(":id"))
	if err != nil {
		if models.IsErrCommentNotExist(err) {
			ctx.NotFound(err)
		} else {
			ctx.Error(http.StatusInternalServerError, "GetCommentByID", err)
		}
		return
	}

	err = comment.LoadIssue()
	if err != nil {
		ctx.Error(http.StatusInternalServerError, "comment.LoadIssue() failed", err)
	}

	if comment.Issue.IsLocked && !ctx.Repo.CanWrite(models.UnitTypeIssues) && !ctx.User.IsAdmin {
		ctx.Error(http.StatusForbidden, "ChangeIssueCommentReaction", errors.New("no permission to change reaction"))
		return
	}

	if isCreateType {
		// PostIssueCommentReaction part
		reaction, err := models.CreateCommentReaction(ctx.User, comment.Issue, comment, form.Reaction)
		if err != nil {
			if models.IsErrForbiddenIssueReaction(err) {
				ctx.Error(http.StatusForbidden, err.Error(), err)
			} else {
				ctx.Error(http.StatusInternalServerError, "CreateCommentReaction", err)
			}
			return
		}
		_, err = reaction.LoadUser()
		if err != nil {
			ctx.Error(http.StatusInternalServerError, "Reaction.LoadUser()", err)
			return
		}

		ctx.JSON(http.StatusCreated, api.ReactionResponse{
			User:     reaction.User.APIFormat(),
			Reaction: reaction.Type,
			Created:  reaction.CreatedUnix.AsTime(),
		})
	} else {
		// DeleteIssueCommentReaction part
		err = models.DeleteCommentReaction(ctx.User, comment.Issue, comment, form.Reaction)
		if err != nil {
			ctx.Error(http.StatusInternalServerError, "DeleteCommentReaction", err)
			return
		}
		//ToDo respond 204
		ctx.Status(http.StatusOK)
	}
}

// GetIssueReactions list reactions of a issue comment
func GetIssueReactions(ctx *context.APIContext) {
	// swagger:operation GET /repos/{owner}/{repo}/issues/{index}/reactions issue issueGetIssueReactions
	// ---
	// summary: Get a list reactions of a issue
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
	// responses:
	//   "200":
	//     "$ref": "#/responses/ReactionResponseList"
	//   "403":
	//     "$ref": "#/responses/forbidden"

	issue, err := models.GetIssueWithAttrsByIndex(ctx.Repo.Repository.ID, ctx.ParamsInt64(":index"))
	if err != nil {
		if models.IsErrIssueNotExist(err) {
			ctx.NotFound()
		} else {
			ctx.Error(http.StatusInternalServerError, "GetIssueByIndex", err)
		}
		return
	}

	if !ctx.Repo.CanRead(models.UnitTypeIssues) && !ctx.User.IsAdmin {
		ctx.Error(http.StatusForbidden, "GetIssueReactions", errors.New("no permission to get reactions"))
		return
	}

	reactions, err := models.FindIssueReactions(issue)
	if err != nil {
		ctx.Error(http.StatusInternalServerError, "FindIssueReactions", err)
		return
	}
	_, err = reactions.LoadUsers()
	if err != nil {
		ctx.Error(http.StatusInternalServerError, "ReactionList.LoadUsers()", err)
		return
	}

	var result []api.ReactionResponse
	for _, r := range reactions {
		result = append(result, api.ReactionResponse{
			User:     r.User.APIFormat(),
			Reaction: r.Type,
			Created:  r.CreatedUnix.AsTime(),
		})
	}

	ctx.JSON(http.StatusOK, result)
}

// PostIssueReaction add a reaction to a comment of a issue
func PostIssueReaction(ctx *context.APIContext, form api.EditReactionOption) {
	// swagger:operation POST /repos/{owner}/{repo}/issues/{index}/reactions issue issuePostIssueReaction
	// ---
	// summary: Add a reaction to a comment of a issue
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
	// - name: content
	//   in: body
	//   schema:
	//     "$ref": "#/definitions/EditReactionOption"
	// responses:
	//   "201":
	//     "$ref": "#/responses/ReactionResponse"
	//   "403":
	//     "$ref": "#/responses/forbidden"

	changeIssueReaction(ctx, form, true)
}

// DeleteIssueReaction list reactions of a issue comment
func DeleteIssueReaction(ctx *context.APIContext, form api.EditReactionOption) {
	// swagger:operation DELETE /repos/{owner}/{repo}/issues/{index}/reactions issue issueDeleteIssueReaction
	// ---
	// summary: Remove a reaction from a comment of a issue
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
	// - name: content
	//   in: body
	//   schema:
	//     "$ref": "#/definitions/EditReactionOption"
	// responses:
	//   "200":
	//     "$ref": "#/responses/empty"
	//   "403":
	//     "$ref": "#/responses/forbidden"

	changeIssueReaction(ctx, form, false)
}

func changeIssueReaction(ctx *context.APIContext, form api.EditReactionOption, isCreateType bool) {
	issue, err := models.GetIssueWithAttrsByIndex(ctx.Repo.Repository.ID, ctx.ParamsInt64(":index"))
	if err != nil {
		if models.IsErrIssueNotExist(err) {
			ctx.NotFound()
		} else {
			ctx.Error(http.StatusInternalServerError, "GetIssueByIndex", err)
		}
		return
	}

	if issue.IsLocked && !ctx.Repo.CanWrite(models.UnitTypeIssues) && !ctx.User.IsAdmin {
		ctx.Error(http.StatusForbidden, "ChangeIssueCommentReaction", errors.New("no permission to change reaction"))
		return
	}

	if isCreateType {
		// PostIssueReaction part
		reaction, err := models.CreateIssueReaction(ctx.User, issue, form.Reaction)
		if err != nil {
			if models.IsErrForbiddenIssueReaction(err) {
				ctx.Error(http.StatusForbidden, err.Error(), err)
			} else {
				ctx.Error(http.StatusInternalServerError, "CreateCommentReaction", err)
			}
			return
		}
		_, err = reaction.LoadUser()
		if err != nil {
			ctx.Error(http.StatusInternalServerError, "Reaction.LoadUser()", err)
			return
		}

		ctx.JSON(http.StatusCreated, api.ReactionResponse{
			User:     reaction.User.APIFormat(),
			Reaction: reaction.Type,
			Created:  reaction.CreatedUnix.AsTime(),
		})
	} else {
		// DeleteIssueReaction part
		err = models.DeleteIssueReaction(ctx.User, issue, form.Reaction)
		if err != nil {
			ctx.Error(http.StatusInternalServerError, "DeleteIssueReaction", err)
			return
		}
		//ToDo respond 204
		ctx.Status(http.StatusOK)
	}
}
