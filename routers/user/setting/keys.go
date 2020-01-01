// Copyright 2014 The Gogs Authors. All rights reserved.
// Copyright 2018 The Gitea Authors. All rights reserved.
// Use of this source code is governed by a MIT-style
// license that can be found in the LICENSE file.

package setting

import (
	"code.gitea.io/gitea/models"
	"code.gitea.io/gitea/modules/auth"
	"code.gitea.io/gitea/modules/base"
	"code.gitea.io/gitea/modules/context"
	"code.gitea.io/gitea/modules/setting"
)

const (
	tplSettingsKeys base.TplName = "user/settings/keys"
)

// Keys render user's SSH/GPG public keys page
func Keys(ctx *context.Context) {
	ctx.Data["Title"] = ctx.Tr("settings")
	ctx.Data["PageIsSettingsKeys"] = true
	ctx.Data["DisableSSH"] = setting.SSH.Disabled

	loadKeysData(ctx)

	ctx.HTML(200, tplSettingsKeys)
}

// KeysPost response for change user's SSH/GPG keys
func KeysPost(ctx *context.Context, form auth.AddKeyForm) {
	ctx.Data["Title"] = ctx.Tr("settings")
	ctx.Data["PageIsSettingsKeys"] = true

	if ctx.HasError() {
		loadKeysData(ctx)

		ctx.HTML(200, tplSettingsKeys)
		return
	}
	switch form.Type {
	case "gpg":
		key, err := models.AddGPGKey(ctx.User.ID, form.Content)
		if err != nil {
			ctx.Data["HasGPGError"] = true
			switch {
			case models.IsErrGPGKeyParsing(err):
				ctx.Flash.Error(ctx.Tr("form.invalid_gpg_key", err.Error()))
				ctx.Redirect(setting.AppSubURL + "/user/settings/keys")
			case models.IsErrGPGKeyIDAlreadyUsed(err):
				loadKeysData(ctx)

				ctx.Data["Err_Content"] = true
				ctx.RenderWithErr(ctx.Tr("settings.gpg_key_id_used"), tplSettingsKeys, &form)
			case models.IsErrGPGNoEmailFound(err):
				loadKeysData(ctx)

				ctx.Data["Err_Content"] = true
				ctx.RenderWithErr(ctx.Tr("settings.gpg_no_key_email_found"), tplSettingsKeys, &form)
			default:
				ctx.ServerError("AddPublicKey", err)
			}
			return
		}
		ctx.Flash.Success(ctx.Tr("settings.add_gpg_key_success", key.KeyID))
		ctx.Redirect(setting.AppSubURL + "/user/settings/keys")
	case "ssh":
		content, err := models.CheckPublicKeyString(form.Content)
		if err != nil {
			if models.IsErrSSHDisabled(err) {
				ctx.Flash.Info(ctx.Tr("settings.ssh_disabled"))
			} else if models.IsErrKeyUnableVerify(err) {
				ctx.Flash.Info(ctx.Tr("form.unable_verify_ssh_key"))
			} else {
				ctx.Flash.Error(ctx.Tr("form.invalid_ssh_key", err.Error()))
			}
			ctx.Redirect(setting.AppSubURL + "/user/settings/keys")
			return
		}

		if _, err = models.AddPublicKey(ctx.User.ID, form.Title, content, 0); err != nil {
			ctx.Data["HasSSHError"] = true
			switch {
			case models.IsErrKeyAlreadyExist(err):
				loadKeysData(ctx)

				ctx.Data["Err_Content"] = true
				ctx.RenderWithErr(ctx.Tr("settings.ssh_key_been_used"), tplSettingsKeys, &form)
			case models.IsErrKeyNameAlreadyUsed(err):
				loadKeysData(ctx)

				ctx.Data["Err_Title"] = true
				ctx.RenderWithErr(ctx.Tr("settings.ssh_key_name_used"), tplSettingsKeys, &form)
			default:
				ctx.ServerError("AddPublicKey", err)
			}
			return
		}
		ctx.Flash.Success(ctx.Tr("settings.add_key_success", form.Title))
		ctx.Redirect(setting.AppSubURL + "/user/settings/keys")

	default:
		ctx.Flash.Warning("Function not implemented")
		ctx.Redirect(setting.AppSubURL + "/user/settings/keys")
	}

}

// DeleteKey response for delete user's SSH/GPG key
func DeleteKey(ctx *context.Context) {

	switch ctx.Query("type") {
	case "gpg":
		if err := models.DeleteGPGKey(ctx.User, ctx.QueryInt64("id")); err != nil {
			ctx.Flash.Error("DeleteGPGKey: " + err.Error())
		} else {
			ctx.Flash.Success(ctx.Tr("settings.gpg_key_deletion_success"))
		}
	case "ssh":
		if err := models.DeletePublicKey(ctx.User, ctx.QueryInt64("id")); err != nil {
			ctx.Flash.Error("DeletePublicKey: " + err.Error())
		} else {
			ctx.Flash.Success(ctx.Tr("settings.ssh_key_deletion_success"))
		}
	default:
		ctx.Flash.Warning("Function not implemented")
		ctx.Redirect(setting.AppSubURL + "/user/settings/keys")
	}
	ctx.JSON(200, map[string]interface{}{
		"redirect": setting.AppSubURL + "/user/settings/keys",
	})
}

func loadKeysData(ctx *context.Context) {
	keys, err := models.ListPublicKeys(ctx.User.ID)
	if err != nil {
		ctx.ServerError("ListPublicKeys", err)
		return
	}
	ctx.Data["Keys"] = keys

	gpgkeys, err := models.ListGPGKeys(ctx.User.ID)
	if err != nil {
		ctx.ServerError("ListGPGKeys", err)
		return
	}
	ctx.Data["GPGKeys"] = gpgkeys
}
