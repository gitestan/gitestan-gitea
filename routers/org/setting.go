// Copyright 2014 The Gogs Authors. All rights reserved.
// Copyright 2019 The Gitea Authors. All rights reserved.
// Use of this source code is governed by a MIT-style
// license that can be found in the LICENSE file.

package org

import (
	"strings"

	"code.gitea.io/gitea/models"
	"code.gitea.io/gitea/modules/auth"
	"code.gitea.io/gitea/modules/base"
	"code.gitea.io/gitea/modules/context"
	"code.gitea.io/gitea/modules/log"
	"code.gitea.io/gitea/modules/setting"
	userSetting "code.gitea.io/gitea/routers/user/setting"
)

const (
	// tplSettingsOptions template path for render settings
	tplSettingsOptions base.TplName = "org/settings/options"
	// tplSettingsDelete template path for render delete repository
	tplSettingsDelete base.TplName = "org/settings/delete"
	// tplSettingsHooks template path for render hook settings
	tplSettingsHooks base.TplName = "org/settings/hooks"
)

// Settings render the main settings page
func Settings(ctx *context.Context) {
	ctx.Data["Title"] = ctx.Tr("org.settings")
	ctx.Data["PageIsSettingsOptions"] = true
	ctx.Data["CurrentVisibility"] = ctx.Org.Organization.Visibility
	ctx.Data["RepoAdminChangeTeamAccess"] = ctx.Org.Organization.RepoAdminChangeTeamAccess
	ctx.HTML(200, tplSettingsOptions)
}

// SettingsPost response for settings change submited
func SettingsPost(ctx *context.Context, form auth.UpdateOrgSettingForm) {
	ctx.Data["Title"] = ctx.Tr("org.settings")
	ctx.Data["PageIsSettingsOptions"] = true
	ctx.Data["CurrentVisibility"] = ctx.Org.Organization.Visibility

	if ctx.HasError() {
		ctx.HTML(200, tplSettingsOptions)
		return
	}

	org := ctx.Org.Organization

	// Check if organization name has been changed.
	if org.LowerName != strings.ToLower(form.Name) {
		isExist, err := models.IsUserExist(org.ID, form.Name)
		if err != nil {
			ctx.ServerError("IsUserExist", err)
			return
		} else if isExist {
			ctx.Data["OrgName"] = true
			ctx.RenderWithErr(ctx.Tr("form.username_been_taken"), tplSettingsOptions, &form)
			return
		} else if err = models.ChangeUserName(org, form.Name); err != nil {
			if err == models.ErrUserNameIllegal {
				ctx.Data["OrgName"] = true
				ctx.RenderWithErr(ctx.Tr("form.illegal_username"), tplSettingsOptions, &form)
			} else {
				ctx.ServerError("ChangeUserName", err)
			}
			return
		}
		// reset ctx.org.OrgLink with new name
		ctx.Org.OrgLink = setting.AppSubURL + "/org/" + form.Name
		log.Trace("Organization name changed: %s -> %s", org.Name, form.Name)
	}
	// In case it's just a case change.
	org.Name = form.Name
	org.LowerName = strings.ToLower(form.Name)

	if ctx.User.IsAdmin {
		org.MaxRepoCreation = form.MaxRepoCreation
	}

	org.FullName = form.FullName
	org.Description = form.Description
	org.Website = form.Website
	org.Location = form.Location
	org.Visibility = form.Visibility
	org.RepoAdminChangeTeamAccess = form.RepoAdminChangeTeamAccess
	if err := models.UpdateUser(org); err != nil {
		ctx.ServerError("UpdateUser", err)
		return
	}
	log.Trace("Organization setting updated: %s", org.Name)
	ctx.Flash.Success(ctx.Tr("org.settings.update_setting_success"))
	ctx.Redirect(ctx.Org.OrgLink + "/settings")
}

// SettingsAvatar response for change avatar on settings page
func SettingsAvatar(ctx *context.Context, form auth.AvatarForm) {
	form.Source = auth.AvatarLocal
	if err := userSetting.UpdateAvatarSetting(ctx, form, ctx.Org.Organization); err != nil {
		ctx.Flash.Error(err.Error())
	} else {
		ctx.Flash.Success(ctx.Tr("org.settings.update_avatar_success"))
	}

	ctx.Redirect(ctx.Org.OrgLink + "/settings")
}

// SettingsDeleteAvatar response for delete avatar on setings page
func SettingsDeleteAvatar(ctx *context.Context) {
	if err := ctx.Org.Organization.DeleteAvatar(); err != nil {
		ctx.Flash.Error(err.Error())
	}

	ctx.Redirect(ctx.Org.OrgLink + "/settings")
}

// SettingsDelete response for delete repository
func SettingsDelete(ctx *context.Context) {
	ctx.Data["Title"] = ctx.Tr("org.settings")
	ctx.Data["PageIsSettingsDelete"] = true

	org := ctx.Org.Organization
	if ctx.Req.Method == "POST" {
		if _, err := models.UserSignIn(ctx.User.Name, ctx.Query("password")); err != nil {
			if models.IsErrUserNotExist(err) {
				ctx.RenderWithErr(ctx.Tr("form.enterred_invalid_password"), tplSettingsDelete, nil)
			} else {
				ctx.ServerError("UserSignIn", err)
			}
			return
		}

		if err := models.DeleteOrganization(org); err != nil {
			if models.IsErrUserOwnRepos(err) {
				ctx.Flash.Error(ctx.Tr("form.org_still_own_repo"))
				ctx.Redirect(ctx.Org.OrgLink + "/settings/delete")
			} else {
				ctx.ServerError("DeleteOrganization", err)
			}
		} else {
			log.Trace("Organization deleted: %s", org.Name)
			ctx.Redirect(setting.AppSubURL + "/")
		}
		return
	}

	ctx.HTML(200, tplSettingsDelete)
}

// Webhooks render webhook list page
func Webhooks(ctx *context.Context) {
	ctx.Data["Title"] = ctx.Tr("org.settings")
	ctx.Data["PageIsSettingsHooks"] = true
	ctx.Data["BaseLink"] = ctx.Org.OrgLink + "/settings/hooks"
	ctx.Data["Description"] = ctx.Tr("org.settings.hooks_desc")

	ws, err := models.GetWebhooksByOrgID(ctx.Org.Organization.ID)
	if err != nil {
		ctx.ServerError("GetWebhooksByOrgId", err)
		return
	}

	ctx.Data["Webhooks"] = ws
	ctx.HTML(200, tplSettingsHooks)
}

// DeleteWebhook response for delete webhook
func DeleteWebhook(ctx *context.Context) {
	if err := models.DeleteWebhookByOrgID(ctx.Org.Organization.ID, ctx.QueryInt64("id")); err != nil {
		ctx.Flash.Error("DeleteWebhookByOrgID: " + err.Error())
	} else {
		ctx.Flash.Success(ctx.Tr("repo.settings.webhook_deletion_success"))
	}

	ctx.JSON(200, map[string]interface{}{
		"redirect": ctx.Org.OrgLink + "/settings/hooks",
	})
}
