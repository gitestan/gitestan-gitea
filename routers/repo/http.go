// Copyright 2014 The Gogs Authors. All rights reserved.
// Copyright 2019 The Gitea Authors. All rights reserved.
// Use of this source code is governed by a MIT-style
// license that can be found in the LICENSE file.

package repo

import (
	"bytes"
	"compress/gzip"
	gocontext "context"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path"
	"regexp"
	"strconv"
	"strings"
	"time"

	"code.gitea.io/gitea/models"
	"code.gitea.io/gitea/modules/auth/sso"
	"code.gitea.io/gitea/modules/base"
	"code.gitea.io/gitea/modules/context"
	"code.gitea.io/gitea/modules/git"
	"code.gitea.io/gitea/modules/log"
	"code.gitea.io/gitea/modules/process"
	"code.gitea.io/gitea/modules/setting"
	"code.gitea.io/gitea/modules/timeutil"
	repo_service "code.gitea.io/gitea/services/repository"
)

// HTTP implmentation git smart HTTP protocol
func HTTP(ctx *context.Context) {
	if len(setting.Repository.AccessControlAllowOrigin) > 0 {
		allowedOrigin := setting.Repository.AccessControlAllowOrigin
		// Set CORS headers for browser-based git clients
		ctx.Resp.Header().Set("Access-Control-Allow-Origin", allowedOrigin)
		ctx.Resp.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization, User-Agent")

		// Handle preflight OPTIONS request
		if ctx.Req.Method == "OPTIONS" {
			if allowedOrigin == "*" {
				ctx.Status(http.StatusOK)
			} else if allowedOrigin == "null" {
				ctx.Status(http.StatusForbidden)
			} else {
				origin := ctx.Req.Header.Get("Origin")
				if len(origin) > 0 && origin == allowedOrigin {
					ctx.Status(http.StatusOK)
				} else {
					ctx.Status(http.StatusForbidden)
				}
			}
			return
		}
	}

	username := ctx.Params(":username")
	reponame := strings.TrimSuffix(ctx.Params(":reponame"), ".git")

	if ctx.Query("go-get") == "1" {
		context.EarlyResponseForGoGetMeta(ctx)
		return
	}

	var isPull bool
	service := ctx.Query("service")
	if service == "git-receive-pack" ||
		strings.HasSuffix(ctx.Req.URL.Path, "git-receive-pack") {
		isPull = false
	} else if service == "git-upload-pack" ||
		strings.HasSuffix(ctx.Req.URL.Path, "git-upload-pack") {
		isPull = true
	} else if service == "git-upload-archive" ||
		strings.HasSuffix(ctx.Req.URL.Path, "git-upload-archive") {
		isPull = true
	} else {
		isPull = (ctx.Req.Method == "GET")
	}

	var accessMode models.AccessMode
	if isPull {
		accessMode = models.AccessModeRead
	} else {
		accessMode = models.AccessModeWrite
	}

	isWiki := false
	var unitType = models.UnitTypeCode
	if strings.HasSuffix(reponame, ".wiki") {
		isWiki = true
		unitType = models.UnitTypeWiki
		reponame = reponame[:len(reponame)-5]
	}

	owner, err := models.GetUserByName(username)
	if err != nil {
		ctx.NotFoundOrServerError("GetUserByName", models.IsErrUserNotExist, err)
		return
	}

	repoExist := true
	repo, err := models.GetRepositoryByName(owner.ID, reponame)
	if err != nil {
		if models.IsErrRepoNotExist(err) {
			if redirectRepoID, err := models.LookupRepoRedirect(owner.ID, reponame); err == nil {
				context.RedirectToRepo(ctx, redirectRepoID)
				return
			}
			repoExist = false
		} else {
			ctx.ServerError("GetRepositoryByName", err)
			return
		}
	}

	// Don't allow pushing if the repo is archived
	if repoExist && repo.IsArchived && !isPull {
		ctx.HandleText(http.StatusForbidden, "This repo is archived. You can view files and clone it, but cannot push or open issues/pull-requests.")
		return
	}

	// Only public pull don't need auth.
	isPublicPull := repoExist && !repo.IsPrivate && isPull
	var (
		askAuth      = !isPublicPull || setting.Service.RequireSignInView
		authUser     *models.User
		authUsername string
		authPasswd   string
		environ      []string
	)

	// check access
	if askAuth {
		authUsername = ctx.Req.Header.Get(setting.ReverseProxyAuthUser)
		if setting.Service.EnableReverseProxyAuth && len(authUsername) > 0 {
			authUser, err = models.GetUserByName(authUsername)
			if err != nil {
				ctx.HandleText(401, "reverse proxy login error, got error while running GetUserByName")
				return
			}
		} else {
			authHead := ctx.Req.Header.Get("Authorization")
			if len(authHead) == 0 {
				ctx.Resp.Header().Set("WWW-Authenticate", "Basic realm=\".\"")
				ctx.Error(http.StatusUnauthorized)
				return
			}

			auths := strings.Fields(authHead)
			// currently check basic auth
			// TODO: support digit auth
			// FIXME: middlewares/context.go did basic auth check already,
			// maybe could use that one.
			if len(auths) != 2 || auths[0] != "Basic" {
				ctx.HandleText(http.StatusUnauthorized, "no basic auth and digit auth")
				return
			}
			authUsername, authPasswd, err = base.BasicAuthDecode(auths[1])
			if err != nil {
				ctx.HandleText(http.StatusUnauthorized, "no basic auth and digit auth")
				return
			}

			// Check if username or password is a token
			isUsernameToken := len(authPasswd) == 0 || authPasswd == "x-oauth-basic"
			// Assume username is token
			authToken := authUsername
			if !isUsernameToken {
				// Assume password is token
				authToken = authPasswd
			}
			uid := sso.CheckOAuthAccessToken(authToken)
			if uid != 0 {
				ctx.Data["IsApiToken"] = true

				authUser, err = models.GetUserByID(uid)
				if err != nil {
					ctx.ServerError("GetUserByID", err)
					return
				}
			}
			// Assume password is a token.
			token, err := models.GetAccessTokenBySHA(authToken)
			if err == nil {
				if isUsernameToken {
					authUser, err = models.GetUserByID(token.UID)
					if err != nil {
						ctx.ServerError("GetUserByID", err)
						return
					}
				} else {
					authUser, err = models.GetUserByName(authUsername)
					if err != nil {
						if models.IsErrUserNotExist(err) {
							ctx.HandleText(http.StatusUnauthorized, "invalid credentials")
						} else {
							ctx.ServerError("GetUserByName", err)
						}
						return
					}
					if authUser.ID != token.UID {
						ctx.HandleText(http.StatusUnauthorized, "invalid credentials")
						return
					}
				}
				token.UpdatedUnix = timeutil.TimeStampNow()
				if err = models.UpdateAccessToken(token); err != nil {
					ctx.ServerError("UpdateAccessToken", err)
				}
			} else if !models.IsErrAccessTokenNotExist(err) && !models.IsErrAccessTokenEmpty(err) {
				log.Error("GetAccessTokenBySha: %v", err)
			}

			if authUser == nil {
				// Check username and password
				authUser, err = models.UserSignIn(authUsername, authPasswd)
				if err != nil {
					if models.IsErrUserProhibitLogin(err) {
						ctx.HandleText(http.StatusForbidden, "User is not permitted to login")
						return
					} else if !models.IsErrUserNotExist(err) {
						ctx.ServerError("UserSignIn error: %v", err)
						return
					}
				}

				if authUser == nil {
					ctx.HandleText(http.StatusUnauthorized, "invalid credentials")
					return
				}

				_, err = models.GetTwoFactorByUID(authUser.ID)
				if err == nil {
					// TODO: This response should be changed to "invalid credentials" for security reasons once the expectation behind it (creating an app token to authenticate) is properly documented
					ctx.HandleText(http.StatusUnauthorized, "Users with two-factor authentication enabled cannot perform HTTP/HTTPS operations via plain username and password. Please create and use a personal access token on the user settings page")
					return
				} else if !models.IsErrTwoFactorNotEnrolled(err) {
					ctx.ServerError("IsErrTwoFactorNotEnrolled", err)
					return
				}
			}
		}

		if repoExist {
			perm, err := models.GetUserRepoPermission(repo, authUser)
			if err != nil {
				ctx.ServerError("GetUserRepoPermission", err)
				return
			}

			if !perm.CanAccess(accessMode, unitType) {
				ctx.HandleText(http.StatusForbidden, "User permission denied")
				return
			}

			if !isPull && repo.IsMirror {
				ctx.HandleText(http.StatusForbidden, "mirror repository is read-only")
				return
			}
		}

		environ = []string{
			models.EnvRepoUsername + "=" + username,
			models.EnvRepoName + "=" + reponame,
			models.EnvPusherName + "=" + authUser.Name,
			models.EnvPusherID + fmt.Sprintf("=%d", authUser.ID),
			models.EnvIsDeployKey + "=false",
		}

		if !authUser.KeepEmailPrivate {
			environ = append(environ, models.EnvPusherEmail+"="+authUser.Email)
		}

		if isWiki {
			environ = append(environ, models.EnvRepoIsWiki+"=true")
		} else {
			environ = append(environ, models.EnvRepoIsWiki+"=false")
		}
	}

	if !repoExist {
		if owner.IsOrganization() && !setting.Repository.EnablePushCreateOrg {
			ctx.HandleText(http.StatusForbidden, "Push to create is not enabled for organizations.")
			return
		}
		if !owner.IsOrganization() && !setting.Repository.EnablePushCreateUser {
			ctx.HandleText(http.StatusForbidden, "Push to create is not enabled for users.")
			return
		}
		repo, err = repo_service.PushCreateRepo(authUser, owner, reponame)
		if err != nil {
			log.Error("pushCreateRepo: %v", err)
			ctx.Status(http.StatusNotFound)
			return
		}
	}

	environ = append(environ, models.ProtectedBranchRepoID+fmt.Sprintf("=%d", repo.ID))

	w := ctx.Resp
	r := ctx.Req.Request
	cfg := &serviceConfig{
		UploadPack:  true,
		ReceivePack: true,
		Env:         environ,
	}

	for _, route := range routes {
		r.URL.Path = strings.ToLower(r.URL.Path) // blue: In case some repo name has upper case name
		if m := route.reg.FindStringSubmatch(r.URL.Path); m != nil {
			if setting.Repository.DisableHTTPGit {
				w.WriteHeader(http.StatusForbidden)
				_, err := w.Write([]byte("Interacting with repositories by HTTP protocol is not allowed"))
				if err != nil {
					log.Error(err.Error())
				}
				return
			}
			if route.method != r.Method {
				if r.Proto == "HTTP/1.1" {
					w.WriteHeader(http.StatusMethodNotAllowed)
					_, err := w.Write([]byte("Method Not Allowed"))
					if err != nil {
						log.Error(err.Error())
					}
				} else {
					w.WriteHeader(http.StatusBadRequest)
					_, err := w.Write([]byte("Bad Request"))
					if err != nil {
						log.Error(err.Error())
					}
				}
				return
			}

			file := strings.Replace(r.URL.Path, m[1]+"/", "", 1)
			dir, err := getGitRepoPath(m[1])
			if err != nil {
				log.Error(err.Error())
				ctx.NotFound("Smart Git HTTP", err)
				return
			}

			route.handler(serviceHandler{cfg, w, r, dir, file, cfg.Env})
			return
		}
	}

	ctx.NotFound("Smart Git HTTP", nil)
}

type serviceConfig struct {
	UploadPack  bool
	ReceivePack bool
	Env         []string
}

type serviceHandler struct {
	cfg     *serviceConfig
	w       http.ResponseWriter
	r       *http.Request
	dir     string
	file    string
	environ []string
}

func (h *serviceHandler) setHeaderNoCache() {
	h.w.Header().Set("Expires", "Fri, 01 Jan 1980 00:00:00 GMT")
	h.w.Header().Set("Pragma", "no-cache")
	h.w.Header().Set("Cache-Control", "no-cache, max-age=0, must-revalidate")
}

func (h *serviceHandler) setHeaderCacheForever() {
	now := time.Now().Unix()
	expires := now + 31536000
	h.w.Header().Set("Date", fmt.Sprintf("%d", now))
	h.w.Header().Set("Expires", fmt.Sprintf("%d", expires))
	h.w.Header().Set("Cache-Control", "public, max-age=31536000")
}

func (h *serviceHandler) sendFile(contentType string) {
	reqFile := path.Join(h.dir, h.file)

	fi, err := os.Stat(reqFile)
	if os.IsNotExist(err) {
		h.w.WriteHeader(http.StatusNotFound)
		return
	}

	h.w.Header().Set("Content-Type", contentType)
	h.w.Header().Set("Content-Length", fmt.Sprintf("%d", fi.Size()))
	h.w.Header().Set("Last-Modified", fi.ModTime().Format(http.TimeFormat))
	http.ServeFile(h.w, h.r, reqFile)
}

type route struct {
	reg     *regexp.Regexp
	method  string
	handler func(serviceHandler)
}

var routes = []route{
	{regexp.MustCompile(`(.*?)/git-upload-pack$`), "POST", serviceUploadPack},
	{regexp.MustCompile(`(.*?)/git-receive-pack$`), "POST", serviceReceivePack},
	{regexp.MustCompile(`(.*?)/info/refs$`), "GET", getInfoRefs},
	{regexp.MustCompile(`(.*?)/HEAD$`), "GET", getTextFile},
	{regexp.MustCompile(`(.*?)/objects/info/alternates$`), "GET", getTextFile},
	{regexp.MustCompile(`(.*?)/objects/info/http-alternates$`), "GET", getTextFile},
	{regexp.MustCompile(`(.*?)/objects/info/packs$`), "GET", getInfoPacks},
	{regexp.MustCompile(`(.*?)/objects/info/[^/]*$`), "GET", getTextFile},
	{regexp.MustCompile(`(.*?)/objects/[0-9a-f]{2}/[0-9a-f]{38}$`), "GET", getLooseObject},
	{regexp.MustCompile(`(.*?)/objects/pack/pack-[0-9a-f]{40}\.pack$`), "GET", getPackFile},
	{regexp.MustCompile(`(.*?)/objects/pack/pack-[0-9a-f]{40}\.idx$`), "GET", getIdxFile},
}

func getGitConfig(option, dir string) string {
	out, err := git.NewCommand("config", option).RunInDir(dir)
	if err != nil {
		log.Error("%v - %s", err, out)
	}
	return out[0 : len(out)-1]
}

func getConfigSetting(service, dir string) bool {
	service = strings.Replace(service, "-", "", -1)
	setting := getGitConfig("http."+service, dir)

	if service == "uploadpack" {
		return setting != "false"
	}

	return setting == "true"
}

func hasAccess(service string, h serviceHandler, checkContentType bool) bool {
	if checkContentType {
		if h.r.Header.Get("Content-Type") != fmt.Sprintf("application/x-git-%s-request", service) {
			return false
		}
	}

	if !(service == "upload-pack" || service == "receive-pack") {
		return false
	}
	if service == "receive-pack" {
		return h.cfg.ReceivePack
	}
	if service == "upload-pack" {
		return h.cfg.UploadPack
	}

	return getConfigSetting(service, h.dir)
}

func serviceRPC(h serviceHandler, service string) {
	defer func() {
		if err := h.r.Body.Close(); err != nil {
			log.Error("serviceRPC: Close: %v", err)
		}

	}()

	if !hasAccess(service, h, true) {
		h.w.WriteHeader(http.StatusUnauthorized)
		return
	}

	h.w.Header().Set("Content-Type", fmt.Sprintf("application/x-git-%s-result", service))

	var err error
	var reqBody = h.r.Body

	// Handle GZIP.
	if h.r.Header.Get("Content-Encoding") == "gzip" {
		reqBody, err = gzip.NewReader(reqBody)
		if err != nil {
			log.Error("Fail to create gzip reader: %v", err)
			h.w.WriteHeader(http.StatusInternalServerError)
			return
		}
	}

	// set this for allow pre-receive and post-receive execute
	h.environ = append(h.environ, "SSH_ORIGINAL_COMMAND="+service)

	ctx, cancel := gocontext.WithCancel(git.DefaultContext)
	defer cancel()
	var stderr bytes.Buffer
	cmd := exec.CommandContext(ctx, git.GitExecutable, service, "--stateless-rpc", h.dir)
	cmd.Dir = h.dir
	if service == "receive-pack" {
		cmd.Env = append(os.Environ(), h.environ...)
	}
	cmd.Stdout = h.w
	cmd.Stdin = reqBody
	cmd.Stderr = &stderr

	pid := process.GetManager().Add(fmt.Sprintf("%s %s %s [repo_path: %s]", git.GitExecutable, service, "--stateless-rpc", h.dir), cancel)
	defer process.GetManager().Remove(pid)

	if err := cmd.Run(); err != nil {
		log.Error("Fail to serve RPC(%s): %v - %s", service, err, stderr.String())
		return
	}
}

func serviceUploadPack(h serviceHandler) {
	serviceRPC(h, "upload-pack")
}

func serviceReceivePack(h serviceHandler) {
	serviceRPC(h, "receive-pack")
}

func getServiceType(r *http.Request) string {
	serviceType := r.FormValue("service")
	if !strings.HasPrefix(serviceType, "git-") {
		return ""
	}
	return strings.Replace(serviceType, "git-", "", 1)
}

func updateServerInfo(dir string) []byte {
	out, err := git.NewCommand("update-server-info").RunInDirBytes(dir)
	if err != nil {
		log.Error(fmt.Sprintf("%v - %s", err, string(out)))
	}
	return out
}

func packetWrite(str string) []byte {
	s := strconv.FormatInt(int64(len(str)+4), 16)
	if len(s)%4 != 0 {
		s = strings.Repeat("0", 4-len(s)%4) + s
	}
	return []byte(s + str)
}

func getInfoRefs(h serviceHandler) {
	h.setHeaderNoCache()
	if hasAccess(getServiceType(h.r), h, false) {
		service := getServiceType(h.r)
		refs, err := git.NewCommand(service, "--stateless-rpc", "--advertise-refs", ".").RunInDirBytes(h.dir)
		if err != nil {
			log.Error(fmt.Sprintf("%v - %s", err, string(refs)))
		}

		h.w.Header().Set("Content-Type", fmt.Sprintf("application/x-git-%s-advertisement", service))
		h.w.WriteHeader(http.StatusOK)
		_, _ = h.w.Write(packetWrite("# service=git-" + service + "\n"))
		_, _ = h.w.Write([]byte("0000"))
		_, _ = h.w.Write(refs)
	} else {
		updateServerInfo(h.dir)
		h.sendFile("text/plain; charset=utf-8")
	}
}

func getTextFile(h serviceHandler) {
	h.setHeaderNoCache()
	h.sendFile("text/plain")
}

func getInfoPacks(h serviceHandler) {
	h.setHeaderCacheForever()
	h.sendFile("text/plain; charset=utf-8")
}

func getLooseObject(h serviceHandler) {
	h.setHeaderCacheForever()
	h.sendFile("application/x-git-loose-object")
}

func getPackFile(h serviceHandler) {
	h.setHeaderCacheForever()
	h.sendFile("application/x-git-packed-objects")
}

func getIdxFile(h serviceHandler) {
	h.setHeaderCacheForever()
	h.sendFile("application/x-git-packed-objects-toc")
}

func getGitRepoPath(subdir string) (string, error) {
	if !strings.HasSuffix(subdir, ".git") {
		subdir += ".git"
	}

	fpath := path.Join(setting.RepoRootPath, subdir)
	if _, err := os.Stat(fpath); os.IsNotExist(err) {
		return "", err
	}

	return fpath, nil
}
