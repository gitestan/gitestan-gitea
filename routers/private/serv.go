// Copyright 2019 The Gitea Authors. All rights reserved.
// Use of this source code is governed by a MIT-style
// license that can be found in the LICENSE file.

// Package private includes all internal routes. The package name internal is ideal but Golang is not allowed, so we use private as package name instead.
package private

import (
	"fmt"
	"net/http"
	"strings"

	"code.gitea.io/gitea/models"
	"code.gitea.io/gitea/modules/log"
	"code.gitea.io/gitea/modules/private"
	"code.gitea.io/gitea/modules/setting"
	repo_service "code.gitea.io/gitea/services/repository"

	"gitea.com/macaron/macaron"
)

// ServNoCommand returns information about the provided keyid
func ServNoCommand(ctx *macaron.Context) {
	keyID := ctx.ParamsInt64(":keyid")
	if keyID <= 0 {
		ctx.JSON(http.StatusBadRequest, map[string]interface{}{
			"err": fmt.Sprintf("Bad key id: %d", keyID),
		})
	}
	results := private.KeyAndOwner{}

	key, err := models.GetPublicKeyByID(keyID)
	if err != nil {
		if models.IsErrKeyNotExist(err) {
			ctx.JSON(http.StatusUnauthorized, map[string]interface{}{
				"err": fmt.Sprintf("Cannot find key: %d", keyID),
			})
			return
		}
		log.Error("Unable to get public key: %d Error: %v", keyID, err)
		ctx.JSON(http.StatusInternalServerError, map[string]interface{}{
			"err": err.Error(),
		})
		return
	}
	results.Key = key

	if key.Type == models.KeyTypeUser {
		user, err := models.GetUserByID(key.OwnerID)
		if err != nil {
			if models.IsErrUserNotExist(err) {
				ctx.JSON(http.StatusUnauthorized, map[string]interface{}{
					"err": fmt.Sprintf("Cannot find owner with id: %d for key: %d", key.OwnerID, keyID),
				})
				return
			}
			log.Error("Unable to get owner with id: %d for public key: %d Error: %v", key.OwnerID, keyID, err)
			ctx.JSON(http.StatusInternalServerError, map[string]interface{}{
				"err": err.Error(),
			})
			return
		}
		results.Owner = user
	}
	ctx.JSON(http.StatusOK, &results)
}

// ServCommand returns information about the provided keyid
func ServCommand(ctx *macaron.Context) {
	// Although we provide the verbs we don't need them at present they're just for logging purposes
	keyID := ctx.ParamsInt64(":keyid")
	ownerName := ctx.Params(":owner")
	repoName := ctx.Params(":repo")
	mode := models.AccessMode(ctx.QueryInt("mode"))

	// Set the basic parts of the results to return
	results := private.ServCommandResults{
		RepoName:  repoName,
		OwnerName: ownerName,
		KeyID:     keyID,
	}

	// Now because we're not translating things properly let's just default some Engish strings here
	modeString := "read"
	if mode > models.AccessModeRead {
		modeString = "write to"
	}

	// The default unit we're trying to look at is code
	unitType := models.UnitTypeCode

	// Unless we're a wiki...
	if strings.HasSuffix(repoName, ".wiki") {
		// in which case we need to look at the wiki
		unitType = models.UnitTypeWiki
		// And we'd better munge the reponame and tell downstream we're looking at a wiki
		results.IsWiki = true
		results.RepoName = repoName[:len(repoName)-5]
	}

	// Now get the Repository and set the results section
	repoExist := true
	repo, err := models.GetRepositoryByOwnerAndName(results.OwnerName, results.RepoName)
	if err != nil {
		if models.IsErrRepoNotExist(err) {
			repoExist = false
		} else {
			log.Error("Unable to get repository: %s/%s Error: %v", results.OwnerName, results.RepoName, err)
			ctx.JSON(http.StatusInternalServerError, map[string]interface{}{
				"results": results,
				"type":    "InternalServerError",
				"err":     fmt.Sprintf("Unable to get repository: %s/%s %v", results.OwnerName, results.RepoName, err),
			})
			return
		}
	}

	if repoExist {
		repo.OwnerName = ownerName
		results.RepoID = repo.ID

		if repo.IsBeingCreated() {
			ctx.JSON(http.StatusInternalServerError, map[string]interface{}{
				"results": results,
				"type":    "InternalServerError",
				"err":     "Repository is being created, you could retry after it finished",
			})
			return
		}

		// We can shortcut at this point if the repo is a mirror
		if mode > models.AccessModeRead && repo.IsMirror {
			ctx.JSON(http.StatusUnauthorized, map[string]interface{}{
				"results": results,
				"type":    "ErrMirrorReadOnly",
				"err":     fmt.Sprintf("Mirror Repository %s/%s is read-only", results.OwnerName, results.RepoName),
			})
			return
		}
	}

	// Get the Public Key represented by the keyID
	key, err := models.GetPublicKeyByID(keyID)
	if err != nil {
		if models.IsErrKeyNotExist(err) {
			ctx.JSON(http.StatusUnauthorized, map[string]interface{}{
				"results": results,
				"type":    "ErrKeyNotExist",
				"err":     fmt.Sprintf("Cannot find key: %d", keyID),
			})
			return
		}
		log.Error("Unable to get public key: %d Error: %v", keyID, err)
		ctx.JSON(http.StatusInternalServerError, map[string]interface{}{
			"results": results,
			"type":    "InternalServerError",
			"err":     fmt.Sprintf("Unable to get key: %d  Error: %v", keyID, err),
		})
		return
	}
	results.KeyName = key.Name
	results.KeyID = key.ID
	results.UserID = key.OwnerID

	// If repo doesn't exist, deploy key doesn't make sense
	if !repoExist && key.Type == models.KeyTypeDeploy {
		ctx.JSON(http.StatusNotFound, map[string]interface{}{
			"results": results,
			"type":    "ErrRepoNotExist",
			"err":     fmt.Sprintf("Cannot find repository %s/%s", results.OwnerName, results.RepoName),
		})
		return
	}

	// Deploy Keys have ownerID set to 0 therefore we can't use the owner
	// So now we need to check if the key is a deploy key
	// We'll keep hold of the deploy key here for permissions checking
	var deployKey *models.DeployKey
	var user *models.User
	if key.Type == models.KeyTypeDeploy {
		results.IsDeployKey = true

		var err error
		deployKey, err = models.GetDeployKeyByRepo(key.ID, repo.ID)
		if err != nil {
			if models.IsErrDeployKeyNotExist(err) {
				ctx.JSON(http.StatusUnauthorized, map[string]interface{}{
					"results": results,
					"type":    "ErrDeployKeyNotExist",
					"err":     fmt.Sprintf("Public (Deploy) Key: %d:%s is not authorized to %s %s/%s.", key.ID, key.Name, modeString, results.OwnerName, results.RepoName),
				})
				return
			}
			log.Error("Unable to get deploy for public (deploy) key: %d in %-v Error: %v", key.ID, repo, err)
			ctx.JSON(http.StatusInternalServerError, map[string]interface{}{
				"results": results,
				"type":    "InternalServerError",
				"err":     fmt.Sprintf("Unable to get Deploy Key for Public Key: %d:%s in %s/%s.", key.ID, key.Name, results.OwnerName, results.RepoName),
			})
			return
		}
		results.KeyName = deployKey.Name

		// FIXME: Deploy keys aren't really the owner of the repo pushing changes
		// however we don't have good way of representing deploy keys in hook.go
		// so for now use the owner of the repository
		results.UserName = results.OwnerName
		results.UserID = repo.OwnerID
	} else {
		// Get the user represented by the Key
		var err error
		user, err = models.GetUserByID(key.OwnerID)
		if err != nil {
			if models.IsErrUserNotExist(err) {
				ctx.JSON(http.StatusUnauthorized, map[string]interface{}{
					"results": results,
					"type":    "ErrUserNotExist",
					"err":     fmt.Sprintf("Public Key: %d:%s owner %d does not exist.", key.ID, key.Name, key.OwnerID),
				})
				return
			}
			log.Error("Unable to get owner: %d for public key: %d:%s Error: %v", key.OwnerID, key.ID, key.Name, err)
			ctx.JSON(http.StatusInternalServerError, map[string]interface{}{
				"results": results,
				"type":    "InternalServerError",
				"err":     fmt.Sprintf("Unable to get Owner: %d for Deploy Key: %d:%s in %s/%s.", key.OwnerID, key.ID, key.Name, ownerName, repoName),
			})
			return
		}
		results.UserName = user.Name
	}

	// Don't allow pushing if the repo is archived
	if repoExist && mode > models.AccessModeRead && repo.IsArchived {
		ctx.JSON(http.StatusUnauthorized, map[string]interface{}{
			"results": results,
			"type":    "ErrRepoIsArchived",
			"err":     fmt.Sprintf("Repo: %s/%s is archived.", results.OwnerName, results.RepoName),
		})
		return
	}

	// Permissions checking:
	if repoExist && (mode > models.AccessModeRead || repo.IsPrivate || setting.Service.RequireSignInView) {
		if key.Type == models.KeyTypeDeploy {
			if deployKey.Mode < mode {
				ctx.JSON(http.StatusUnauthorized, map[string]interface{}{
					"results": results,
					"type":    "ErrUnauthorized",
					"err":     fmt.Sprintf("Deploy Key: %d:%s is not authorized to %s %s/%s.", key.ID, key.Name, modeString, results.OwnerName, results.RepoName),
				})
				return
			}
		} else {
			perm, err := models.GetUserRepoPermission(repo, user)
			if err != nil {
				log.Error("Unable to get permissions for %-v with key %d in %-v Error: %v", user, key.ID, repo, err)
				ctx.JSON(http.StatusInternalServerError, map[string]interface{}{
					"results": results,
					"type":    "InternalServerError",
					"err":     fmt.Sprintf("Unable to get permissions for user %d:%s with key %d in %s/%s Error: %v", user.ID, user.Name, key.ID, results.OwnerName, results.RepoName, err),
				})
				return
			}

			userMode := perm.UnitAccessMode(unitType)

			if userMode < mode {
				ctx.JSON(http.StatusUnauthorized, map[string]interface{}{
					"results": results,
					"type":    "ErrUnauthorized",
					"err":     fmt.Sprintf("User: %d:%s with Key: %d:%s is not authorized to %s %s/%s.", user.ID, user.Name, key.ID, key.Name, modeString, ownerName, repoName),
				})
				return
			}
		}
	}

	// We already know we aren't using a deploy key
	if !repoExist {
		owner, err := models.GetUserByName(ownerName)
		if err != nil {
			ctx.JSON(http.StatusInternalServerError, map[string]interface{}{
				"results": results,
				"type":    "InternalServerError",
				"err":     fmt.Sprintf("Unable to get owner: %s %v", results.OwnerName, err),
			})
			return
		}

		if owner.IsOrganization() && !setting.Repository.EnablePushCreateOrg {
			ctx.JSON(http.StatusForbidden, map[string]interface{}{
				"results": results,
				"type":    "ErrForbidden",
				"err":     "Push to create is not enabled for organizations.",
			})
			return
		}
		if !owner.IsOrganization() && !setting.Repository.EnablePushCreateUser {
			ctx.JSON(http.StatusForbidden, map[string]interface{}{
				"results": results,
				"type":    "ErrForbidden",
				"err":     "Push to create is not enabled for users.",
			})
			return
		}

		repo, err = repo_service.PushCreateRepo(user, owner, results.RepoName)
		if err != nil {
			log.Error("pushCreateRepo: %v", err)
			ctx.JSON(http.StatusNotFound, map[string]interface{}{
				"results": results,
				"type":    "ErrRepoNotExist",
				"err":     fmt.Sprintf("Cannot find repository: %s/%s", results.OwnerName, results.RepoName),
			})
			return
		}
		results.RepoID = repo.ID
	}

	// Finally if we're trying to touch the wiki we should init it
	if results.IsWiki {
		if err = repo.InitWiki(); err != nil {
			log.Error("Failed to initialize the wiki in %-v Error: %v", repo, err)
			ctx.JSON(http.StatusInternalServerError, map[string]interface{}{
				"results": results,
				"type":    "InternalServerError",
				"err":     fmt.Sprintf("Failed to initialize the wiki in %s/%s Error: %v", ownerName, repoName, err),
			})
			return
		}
	}
	log.Debug("Serv Results:\nIsWiki: %t\nIsDeployKey: %t\nKeyID: %d\tKeyName: %s\nUserName: %s\nUserID: %d\nOwnerName: %s\nRepoName: %s\nRepoID: %d",
		results.IsWiki,
		results.IsDeployKey,
		results.KeyID,
		results.KeyName,
		results.UserName,
		results.UserID,
		results.OwnerName,
		results.RepoName,
		results.RepoID)

	ctx.JSON(http.StatusOK, results)
	// We will update the keys in a different call.
}
