// Copyright 2015 The Gogs Authors. All rights reserved.
// Copyright 2019 The Gitea Authors. All rights reserved.
// Use of this source code is governed by a MIT-style
// license that can be found in the LICENSE file.

package admin

import (
	"errors"
	"net/http"

	"code.gitea.io/gitea/models"
	"code.gitea.io/gitea/modules/context"
	"code.gitea.io/gitea/modules/convert"
	"code.gitea.io/gitea/modules/log"
	"code.gitea.io/gitea/modules/password"
	api "code.gitea.io/gitea/modules/structs"
	"code.gitea.io/gitea/routers/api/v1/user"
	"code.gitea.io/gitea/services/mailer"
)

func parseLoginSource(ctx *context.APIContext, u *models.User, sourceID int64, loginName string) {
	if sourceID == 0 {
		return
	}

	source, err := models.GetLoginSourceByID(sourceID)
	if err != nil {
		if models.IsErrLoginSourceNotExist(err) {
			ctx.Error(http.StatusUnprocessableEntity, "", err)
		} else {
			ctx.Error(http.StatusInternalServerError, "GetLoginSourceByID", err)
		}
		return
	}

	u.LoginType = source.Type
	u.LoginSource = source.ID
	u.LoginName = loginName
}

// CreateUser create a user
func CreateUser(ctx *context.APIContext, form api.CreateUserOption) {
	// swagger:operation POST /admin/users admin adminCreateUser
	// ---
	// summary: Create a user
	// consumes:
	// - application/json
	// produces:
	// - application/json
	// parameters:
	// - name: body
	//   in: body
	//   schema:
	//     "$ref": "#/definitions/CreateUserOption"
	// responses:
	//   "201":
	//     "$ref": "#/responses/User"
	//   "403":
	//     "$ref": "#/responses/forbidden"
	//   "400":
	//     "$ref": "#/responses/error"
	//   "422":
	//     "$ref": "#/responses/validationError"

	u := &models.User{
		Name:               form.Username,
		FullName:           form.FullName,
		Email:              form.Email,
		Passwd:             form.Password,
		MustChangePassword: true,
		IsActive:           true,
		LoginType:          models.LoginPlain,
	}
	if form.MustChangePassword != nil {
		u.MustChangePassword = *form.MustChangePassword
	}

	parseLoginSource(ctx, u, form.SourceID, form.LoginName)
	if ctx.Written() {
		return
	}
	if !password.IsComplexEnough(form.Password) {
		err := errors.New("PasswordComplexity")
		ctx.Error(http.StatusBadRequest, "PasswordComplexity", err)
		return
	}
	if err := models.CreateUser(u); err != nil {
		if models.IsErrUserAlreadyExist(err) ||
			models.IsErrEmailAlreadyUsed(err) ||
			models.IsErrNameReserved(err) ||
			models.IsErrNamePatternNotAllowed(err) {
			ctx.Error(http.StatusUnprocessableEntity, "", err)
		} else {
			ctx.Error(http.StatusInternalServerError, "CreateUser", err)
		}
		return
	}
	log.Trace("Account created by admin (%s): %s", ctx.User.Name, u.Name)

	// Send email notification.
	if form.SendNotify {
		mailer.SendRegisterNotifyMail(ctx.Locale, u)
	}
	ctx.JSON(http.StatusCreated, convert.ToUser(u, ctx.IsSigned, ctx.User.IsAdmin))
}

// EditUser api for modifying a user's information
func EditUser(ctx *context.APIContext, form api.EditUserOption) {
	// swagger:operation PATCH /admin/users/{username} admin adminEditUser
	// ---
	// summary: Edit an existing user
	// consumes:
	// - application/json
	// produces:
	// - application/json
	// parameters:
	// - name: username
	//   in: path
	//   description: username of user to edit
	//   type: string
	//   required: true
	// - name: body
	//   in: body
	//   schema:
	//     "$ref": "#/definitions/EditUserOption"
	// responses:
	//   "200":
	//     "$ref": "#/responses/User"
	//   "403":
	//     "$ref": "#/responses/forbidden"
	//   "422":
	//     "$ref": "#/responses/validationError"

	u := user.GetUserByParams(ctx)
	if ctx.Written() {
		return
	}

	parseLoginSource(ctx, u, form.SourceID, form.LoginName)
	if ctx.Written() {
		return
	}

	if len(form.Password) > 0 {
		if !password.IsComplexEnough(form.Password) {
			err := errors.New("PasswordComplexity")
			ctx.Error(http.StatusBadRequest, "PasswordComplexity", err)
			return
		}
		var err error
		if u.Salt, err = models.GetUserSalt(); err != nil {
			ctx.Error(http.StatusInternalServerError, "UpdateUser", err)
			return
		}
		u.HashPassword(form.Password)
	}

	if form.MustChangePassword != nil {
		u.MustChangePassword = *form.MustChangePassword
	}

	u.LoginName = form.LoginName
	u.FullName = form.FullName
	u.Email = form.Email
	u.Website = form.Website
	u.Location = form.Location
	if form.Active != nil {
		u.IsActive = *form.Active
	}
	if form.Admin != nil {
		u.IsAdmin = *form.Admin
	}
	if form.AllowGitHook != nil {
		u.AllowGitHook = *form.AllowGitHook
	}
	if form.AllowImportLocal != nil {
		u.AllowImportLocal = *form.AllowImportLocal
	}
	if form.MaxRepoCreation != nil {
		u.MaxRepoCreation = *form.MaxRepoCreation
	}
	if form.AllowCreateOrganization != nil {
		u.AllowCreateOrganization = *form.AllowCreateOrganization
	}
	if form.ProhibitLogin != nil {
		u.ProhibitLogin = *form.ProhibitLogin
	}

	if err := models.UpdateUser(u); err != nil {
		if models.IsErrEmailAlreadyUsed(err) {
			ctx.Error(http.StatusUnprocessableEntity, "", err)
		} else {
			ctx.Error(http.StatusInternalServerError, "UpdateUser", err)
		}
		return
	}
	log.Trace("Account profile updated by admin (%s): %s", ctx.User.Name, u.Name)

	ctx.JSON(http.StatusOK, convert.ToUser(u, ctx.IsSigned, ctx.User.IsAdmin))
}

// DeleteUser api for deleting a user
func DeleteUser(ctx *context.APIContext) {
	// swagger:operation DELETE /admin/users/{username} admin adminDeleteUser
	// ---
	// summary: Delete a user
	// produces:
	// - application/json
	// parameters:
	// - name: username
	//   in: path
	//   description: username of user to delete
	//   type: string
	//   required: true
	// responses:
	//   "204":
	//     "$ref": "#/responses/empty"
	//   "403":
	//     "$ref": "#/responses/forbidden"
	//   "422":
	//     "$ref": "#/responses/validationError"

	u := user.GetUserByParams(ctx)
	if ctx.Written() {
		return
	}

	if err := models.DeleteUser(u); err != nil {
		if models.IsErrUserOwnRepos(err) ||
			models.IsErrUserHasOrgs(err) {
			ctx.Error(http.StatusUnprocessableEntity, "", err)
		} else {
			ctx.Error(http.StatusInternalServerError, "DeleteUser", err)
		}
		return
	}
	log.Trace("Account deleted by admin(%s): %s", ctx.User.Name, u.Name)

	ctx.Status(http.StatusNoContent)
}

// CreatePublicKey api for creating a public key to a user
func CreatePublicKey(ctx *context.APIContext, form api.CreateKeyOption) {
	// swagger:operation POST /admin/users/{username}/keys admin adminCreatePublicKey
	// ---
	// summary: Add a public key on behalf of a user
	// consumes:
	// - application/json
	// produces:
	// - application/json
	// parameters:
	// - name: username
	//   in: path
	//   description: username of the user
	//   type: string
	//   required: true
	// - name: key
	//   in: body
	//   schema:
	//     "$ref": "#/definitions/CreateKeyOption"
	// responses:
	//   "201":
	//     "$ref": "#/responses/PublicKey"
	//   "403":
	//     "$ref": "#/responses/forbidden"
	//   "422":
	//     "$ref": "#/responses/validationError"

	u := user.GetUserByParams(ctx)
	if ctx.Written() {
		return
	}
	user.CreateUserPublicKey(ctx, form, u.ID)
}

// DeleteUserPublicKey api for deleting a user's public key
func DeleteUserPublicKey(ctx *context.APIContext) {
	// swagger:operation DELETE /admin/users/{username}/keys/{id} admin adminDeleteUserPublicKey
	// ---
	// summary: Delete a user's public key
	// produces:
	// - application/json
	// parameters:
	// - name: username
	//   in: path
	//   description: username of user
	//   type: string
	//   required: true
	// - name: id
	//   in: path
	//   description: id of the key to delete
	//   type: integer
	//   format: int64
	//   required: true
	// responses:
	//   "204":
	//     "$ref": "#/responses/empty"
	//   "403":
	//     "$ref": "#/responses/forbidden"
	//   "404":
	//     "$ref": "#/responses/notFound"

	u := user.GetUserByParams(ctx)
	if ctx.Written() {
		return
	}

	if err := models.DeletePublicKey(u, ctx.ParamsInt64(":id")); err != nil {
		if models.IsErrKeyNotExist(err) {
			ctx.NotFound()
		} else if models.IsErrKeyAccessDenied(err) {
			ctx.Error(http.StatusForbidden, "", "You do not have access to this key")
		} else {
			ctx.Error(http.StatusInternalServerError, "DeleteUserPublicKey", err)
		}
		return
	}
	log.Trace("Key deleted by admin(%s): %s", ctx.User.Name, u.Name)

	ctx.Status(http.StatusNoContent)
}

//GetAllUsers API for getting information of all the users
func GetAllUsers(ctx *context.APIContext) {
	// swagger:operation GET /admin/users admin adminGetAllUsers
	// ---
	// summary: List all users
	// produces:
	// - application/json
	// responses:
	//   "200":
	//     "$ref": "#/responses/UserList"
	//   "403":
	//     "$ref": "#/responses/forbidden"

	users, _, err := models.SearchUsers(&models.SearchUserOptions{
		Type:     models.UserTypeIndividual,
		OrderBy:  models.SearchOrderByAlphabetically,
		PageSize: -1,
	})
	if err != nil {
		ctx.Error(http.StatusInternalServerError, "GetAllUsers", err)
		return
	}

	results := make([]*api.User, len(users))
	for i := range users {
		results[i] = convert.ToUser(users[i], ctx.IsSigned, ctx.User.IsAdmin)
	}

	ctx.JSON(http.StatusOK, &results)
}
