// Copyright 2015 The Gogs Authors. All rights reserved.
// Copyright 2018 The Gitea Authors. All rights reserved.
// Use of this source code is governed by a MIT-style
// license that can be found in the LICENSE file.

package repo

import (
	"fmt"
	"io/ioutil"
	"net/url"
	"path/filepath"
	"strings"

	"code.gitea.io/gitea/models"
	"code.gitea.io/gitea/modules/auth"
	"code.gitea.io/gitea/modules/base"
	"code.gitea.io/gitea/modules/context"
	"code.gitea.io/gitea/modules/git"
	"code.gitea.io/gitea/modules/log"
	"code.gitea.io/gitea/modules/markup"
	"code.gitea.io/gitea/modules/markup/markdown"
	"code.gitea.io/gitea/modules/timeutil"
	"code.gitea.io/gitea/modules/util"
)

const (
	tplWikiStart    base.TplName = "repo/wiki/start"
	tplWikiView     base.TplName = "repo/wiki/view"
	tplWikiRevision base.TplName = "repo/wiki/revision"
	tplWikiNew      base.TplName = "repo/wiki/new"
	tplWikiPages    base.TplName = "repo/wiki/pages"
)

// MustEnableWiki check if wiki is enabled, if external then redirect
func MustEnableWiki(ctx *context.Context) {
	if !ctx.Repo.CanRead(models.UnitTypeWiki) &&
		!ctx.Repo.CanRead(models.UnitTypeExternalWiki) {
		if log.IsTrace() {
			log.Trace("Permission Denied: User %-v cannot read %-v or %-v of repo %-v\n"+
				"User in repo has Permissions: %-+v",
				ctx.User,
				models.UnitTypeWiki,
				models.UnitTypeExternalWiki,
				ctx.Repo.Repository,
				ctx.Repo.Permission)
		}
		ctx.NotFound("MustEnableWiki", nil)
		return
	}

	unit, err := ctx.Repo.Repository.GetUnit(models.UnitTypeExternalWiki)
	if err == nil {
		ctx.Redirect(unit.ExternalWikiConfig().ExternalWikiURL)
		return
	}
}

// PageMeta wiki page meta information
type PageMeta struct {
	Name        string
	SubURL      string
	UpdatedUnix timeutil.TimeStamp
}

// findEntryForFile finds the tree entry for a target filepath.
func findEntryForFile(commit *git.Commit, target string) (*git.TreeEntry, error) {
	entries, err := commit.ListEntries()
	if err != nil {
		return nil, err
	}
	// The longest name should be checked first
	for _, entry := range entries {
		if entry.IsRegular() && entry.Name() == target {
			return entry, nil
		}
	}
	// Then the unescaped, shortest alternative
	var unescapedTarget string
	if unescapedTarget, err = url.QueryUnescape(target); err != nil {
		return nil, err
	}
	for _, entry := range entries {
		if entry.IsRegular() && entry.Name() == unescapedTarget {
			return entry, nil
		}
	}
	return nil, nil
}

func findWikiRepoCommit(ctx *context.Context) (*git.Repository, *git.Commit, error) {
	wikiRepo, err := git.OpenRepository(ctx.Repo.Repository.WikiPath())
	if err != nil {
		ctx.ServerError("OpenRepository", err)
		return nil, nil, err
	}

	commit, err := wikiRepo.GetBranchCommit("master")
	if err != nil {
		return wikiRepo, nil, err
	}
	return wikiRepo, commit, nil
}

// wikiContentsByEntry returns the contents of the wiki page referenced by the
// given tree entry. Writes to ctx if an error occurs.
func wikiContentsByEntry(ctx *context.Context, entry *git.TreeEntry) []byte {
	reader, err := entry.Blob().DataAsync()
	if err != nil {
		ctx.ServerError("Blob.Data", err)
		return nil
	}
	defer reader.Close()
	content, err := ioutil.ReadAll(reader)
	if err != nil {
		ctx.ServerError("ReadAll", err)
		return nil
	}
	return content
}

// wikiContentsByName returns the contents of a wiki page, along with a boolean
// indicating whether the page exists. Writes to ctx if an error occurs.
func wikiContentsByName(ctx *context.Context, commit *git.Commit, wikiName string) ([]byte, *git.TreeEntry, string, bool) {
	var entry *git.TreeEntry
	var err error
	pageFilename := models.WikiNameToFilename(wikiName)
	if entry, err = findEntryForFile(commit, pageFilename); err != nil {
		ctx.ServerError("findEntryForFile", err)
		return nil, nil, "", false
	} else if entry == nil {
		return nil, nil, "", true
	}
	return wikiContentsByEntry(ctx, entry), entry, pageFilename, false
}

func renderViewPage(ctx *context.Context) (*git.Repository, *git.TreeEntry) {
	wikiRepo, commit, err := findWikiRepoCommit(ctx)
	if err != nil {
		if !git.IsErrNotExist(err) {
			ctx.ServerError("GetBranchCommit", err)
		}
		return nil, nil
	}

	// Get page list.
	entries, err := commit.ListEntries()
	if err != nil {
		if wikiRepo != nil {
			wikiRepo.Close()
		}
		ctx.ServerError("ListEntries", err)
		return nil, nil
	}
	pages := make([]PageMeta, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsRegular() {
			continue
		}
		wikiName, err := models.WikiFilenameToName(entry.Name())
		if err != nil {
			if models.IsErrWikiInvalidFileName(err) {
				continue
			}
			if wikiRepo != nil {
				wikiRepo.Close()
			}
			ctx.ServerError("WikiFilenameToName", err)
			return nil, nil
		} else if wikiName == "_Sidebar" || wikiName == "_Footer" {
			continue
		}
		pages = append(pages, PageMeta{
			Name:   wikiName,
			SubURL: models.WikiNameToSubURL(wikiName),
		})
	}
	ctx.Data["Pages"] = pages

	// get requested pagename
	pageName := models.NormalizeWikiName(ctx.Params(":page"))
	if len(pageName) == 0 {
		pageName = "Home"
	}
	ctx.Data["PageURL"] = models.WikiNameToSubURL(pageName)
	ctx.Data["old_title"] = pageName
	ctx.Data["Title"] = pageName
	ctx.Data["title"] = pageName
	ctx.Data["RequireHighlightJS"] = true

	//lookup filename in wiki - get filecontent, gitTree entry , real filename
	data, entry, pageFilename, noEntry := wikiContentsByName(ctx, commit, pageName)
	if noEntry {
		ctx.Redirect(ctx.Repo.RepoLink + "/wiki/_pages")
	}
	if entry == nil || ctx.Written() {
		if wikiRepo != nil {
			wikiRepo.Close()
		}
		return nil, nil
	}

	sidebarContent, _, _, _ := wikiContentsByName(ctx, commit, "_Sidebar")
	if ctx.Written() {
		if wikiRepo != nil {
			wikiRepo.Close()
		}
		return nil, nil
	}

	footerContent, _, _, _ := wikiContentsByName(ctx, commit, "_Footer")
	if ctx.Written() {
		if wikiRepo != nil {
			wikiRepo.Close()
		}
		return nil, nil
	}

	metas := ctx.Repo.Repository.ComposeMetas()
	ctx.Data["content"] = markdown.RenderWiki(data, ctx.Repo.RepoLink, metas)
	ctx.Data["sidebarPresent"] = sidebarContent != nil
	ctx.Data["sidebarContent"] = markdown.RenderWiki(sidebarContent, ctx.Repo.RepoLink, metas)
	ctx.Data["footerPresent"] = footerContent != nil
	ctx.Data["footerContent"] = markdown.RenderWiki(footerContent, ctx.Repo.RepoLink, metas)

	// get commit count - wiki revisions
	commitsCount, _ := wikiRepo.FileCommitsCount("master", pageFilename)
	ctx.Data["CommitCount"] = commitsCount

	return wikiRepo, entry
}

func renderRevisionPage(ctx *context.Context) (*git.Repository, *git.TreeEntry) {
	wikiRepo, commit, err := findWikiRepoCommit(ctx)
	if err != nil {
		if wikiRepo != nil {
			wikiRepo.Close()
		}
		if !git.IsErrNotExist(err) {
			ctx.ServerError("GetBranchCommit", err)
		}
		return nil, nil
	}

	// get requested pagename
	pageName := models.NormalizeWikiName(ctx.Params(":page"))
	if len(pageName) == 0 {
		pageName = "Home"
	}
	ctx.Data["PageURL"] = models.WikiNameToSubURL(pageName)
	ctx.Data["old_title"] = pageName
	ctx.Data["Title"] = pageName
	ctx.Data["title"] = pageName
	ctx.Data["RequireHighlightJS"] = true

	//lookup filename in wiki - get filecontent, gitTree entry , real filename
	data, entry, pageFilename, noEntry := wikiContentsByName(ctx, commit, pageName)
	if noEntry {
		ctx.Redirect(ctx.Repo.RepoLink + "/wiki/_pages")
	}
	if entry == nil || ctx.Written() {
		if wikiRepo != nil {
			wikiRepo.Close()
		}
		return nil, nil
	}

	ctx.Data["content"] = string(data)
	ctx.Data["sidebarPresent"] = false
	ctx.Data["sidebarContent"] = ""
	ctx.Data["footerPresent"] = false
	ctx.Data["footerContent"] = ""

	// get commit count - wiki revisions
	commitsCount, _ := wikiRepo.FileCommitsCount("master", pageFilename)
	ctx.Data["CommitCount"] = commitsCount

	// get page
	page := ctx.QueryInt("page")
	if page <= 1 {
		page = 1
	}

	// get Commit Count
	commitsHistory, err := wikiRepo.CommitsByFileAndRangeNoFollow("master", pageFilename, page)
	if err != nil {
		if wikiRepo != nil {
			wikiRepo.Close()
		}
		ctx.ServerError("CommitsByFileAndRangeNoFollow", err)
		return nil, nil
	}
	commitsHistory = models.ValidateCommitsWithEmails(commitsHistory)
	commitsHistory = models.ParseCommitsWithSignature(commitsHistory)

	ctx.Data["Commits"] = commitsHistory

	pager := context.NewPagination(int(commitsCount), git.CommitsRangeSize, page, 5)
	pager.SetDefaultParams(ctx)
	ctx.Data["Page"] = pager

	return wikiRepo, entry
}

func renderEditPage(ctx *context.Context) {
	wikiRepo, commit, err := findWikiRepoCommit(ctx)
	if err != nil {
		if wikiRepo != nil {
			wikiRepo.Close()
		}
		if !git.IsErrNotExist(err) {
			ctx.ServerError("GetBranchCommit", err)
		}
		return
	}
	defer func() {
		if wikiRepo != nil {
			wikiRepo.Close()
		}
	}()

	// get requested pagename
	pageName := models.NormalizeWikiName(ctx.Params(":page"))
	if len(pageName) == 0 {
		pageName = "Home"
	}
	ctx.Data["PageURL"] = models.WikiNameToSubURL(pageName)
	ctx.Data["old_title"] = pageName
	ctx.Data["Title"] = pageName
	ctx.Data["title"] = pageName
	ctx.Data["RequireHighlightJS"] = true

	//lookup filename in wiki - get filecontent, gitTree entry , real filename
	data, entry, _, noEntry := wikiContentsByName(ctx, commit, pageName)
	if noEntry {
		ctx.Redirect(ctx.Repo.RepoLink + "/wiki/_pages")
	}
	if entry == nil || ctx.Written() {
		return
	}

	ctx.Data["content"] = string(data)
	ctx.Data["sidebarPresent"] = false
	ctx.Data["sidebarContent"] = ""
	ctx.Data["footerPresent"] = false
	ctx.Data["footerContent"] = ""
}

// Wiki renders single wiki page
func Wiki(ctx *context.Context) {
	ctx.Data["PageIsWiki"] = true
	ctx.Data["CanWriteWiki"] = ctx.Repo.CanWrite(models.UnitTypeWiki) && !ctx.Repo.Repository.IsArchived

	if !ctx.Repo.Repository.HasWiki() {
		ctx.Data["Title"] = ctx.Tr("repo.wiki")
		ctx.HTML(200, tplWikiStart)
		return
	}

	wikiRepo, entry := renderViewPage(ctx)
	if ctx.Written() {
		if wikiRepo != nil {
			wikiRepo.Close()
		}
		return
	}
	defer func() {
		if wikiRepo != nil {
			wikiRepo.Close()
		}
	}()
	if entry == nil {
		ctx.Data["Title"] = ctx.Tr("repo.wiki")
		ctx.HTML(200, tplWikiStart)
		return
	}

	wikiPath := entry.Name()
	if markup.Type(wikiPath) != markdown.MarkupName {
		ext := strings.ToUpper(filepath.Ext(wikiPath))
		ctx.Data["FormatWarning"] = fmt.Sprintf("%s rendering is not supported at the moment. Rendered as Markdown.", ext)
	}
	// Get last change information.
	lastCommit, err := wikiRepo.GetCommitByPath(wikiPath)
	if err != nil {
		ctx.ServerError("GetCommitByPath", err)
		return
	}
	ctx.Data["Author"] = lastCommit.Author

	ctx.HTML(200, tplWikiView)
}

// WikiRevision renders file revision list of wiki page
func WikiRevision(ctx *context.Context) {
	ctx.Data["PageIsWiki"] = true
	ctx.Data["CanWriteWiki"] = ctx.Repo.CanWrite(models.UnitTypeWiki) && !ctx.Repo.Repository.IsArchived

	if !ctx.Repo.Repository.HasWiki() {
		ctx.Data["Title"] = ctx.Tr("repo.wiki")
		ctx.HTML(200, tplWikiStart)
		return
	}

	wikiRepo, entry := renderRevisionPage(ctx)
	if ctx.Written() {
		if wikiRepo != nil {
			wikiRepo.Close()
		}
		return
	}
	defer func() {
		if wikiRepo != nil {
			wikiRepo.Close()
		}
	}()
	if entry == nil {
		ctx.Data["Title"] = ctx.Tr("repo.wiki")
		ctx.HTML(200, tplWikiStart)
		return
	}

	// Get last change information.
	wikiPath := entry.Name()
	lastCommit, err := wikiRepo.GetCommitByPath(wikiPath)
	if err != nil {
		ctx.ServerError("GetCommitByPath", err)
		return
	}
	ctx.Data["Author"] = lastCommit.Author

	ctx.HTML(200, tplWikiRevision)
}

// WikiPages render wiki pages list page
func WikiPages(ctx *context.Context) {
	if !ctx.Repo.Repository.HasWiki() {
		ctx.Redirect(ctx.Repo.RepoLink + "/wiki")
		return
	}

	ctx.Data["Title"] = ctx.Tr("repo.wiki.pages")
	ctx.Data["PageIsWiki"] = true
	ctx.Data["CanWriteWiki"] = ctx.Repo.CanWrite(models.UnitTypeWiki) && !ctx.Repo.Repository.IsArchived

	wikiRepo, commit, err := findWikiRepoCommit(ctx)
	if err != nil {
		if wikiRepo != nil {
			wikiRepo.Close()
		}
		return
	}

	entries, err := commit.ListEntries()
	if err != nil {
		if wikiRepo != nil {
			wikiRepo.Close()
		}

		ctx.ServerError("ListEntries", err)
		return
	}
	pages := make([]PageMeta, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsRegular() {
			continue
		}
		c, err := wikiRepo.GetCommitByPath(entry.Name())
		if err != nil {
			if wikiRepo != nil {
				wikiRepo.Close()
			}

			ctx.ServerError("GetCommit", err)
			return
		}
		wikiName, err := models.WikiFilenameToName(entry.Name())
		if err != nil {
			if models.IsErrWikiInvalidFileName(err) {
				continue
			}
			if wikiRepo != nil {
				wikiRepo.Close()
			}

			ctx.ServerError("WikiFilenameToName", err)
			return
		}
		pages = append(pages, PageMeta{
			Name:        wikiName,
			SubURL:      models.WikiNameToSubURL(wikiName),
			UpdatedUnix: timeutil.TimeStamp(c.Author.When.Unix()),
		})
	}
	ctx.Data["Pages"] = pages

	defer func() {
		if wikiRepo != nil {
			wikiRepo.Close()
		}
	}()
	ctx.HTML(200, tplWikiPages)
}

// WikiRaw outputs raw blob requested by user (image for example)
func WikiRaw(ctx *context.Context) {
	wikiRepo, commit, err := findWikiRepoCommit(ctx)
	if err != nil {
		if wikiRepo != nil {
			return
		}
	}

	providedPath := ctx.Params("*")

	var entry *git.TreeEntry
	if commit != nil {
		// Try to find a file with that name
		entry, err = findEntryForFile(commit, providedPath)
		if err != nil {
			ctx.ServerError("findFile", err)
			return
		}

		if entry == nil {
			// Try to find a wiki page with that name
			if strings.HasSuffix(providedPath, ".md") {
				providedPath = providedPath[:len(providedPath)-3]
			}

			wikiPath := models.WikiNameToFilename(providedPath)
			entry, err = findEntryForFile(commit, wikiPath)
			if err != nil {
				ctx.ServerError("findFile", err)
				return
			}
		}
	}

	if entry != nil {
		if err = ServeBlob(ctx, entry.Blob()); err != nil {
			ctx.ServerError("ServeBlob", err)
		}
		return
	}

	ctx.NotFound("findEntryForFile", nil)
}

// NewWiki render wiki create page
func NewWiki(ctx *context.Context) {
	ctx.Data["Title"] = ctx.Tr("repo.wiki.new_page")
	ctx.Data["PageIsWiki"] = true
	ctx.Data["RequireSimpleMDE"] = true

	if !ctx.Repo.Repository.HasWiki() {
		ctx.Data["title"] = "Home"
	}

	ctx.HTML(200, tplWikiNew)
}

// NewWikiPost response for wiki create request
func NewWikiPost(ctx *context.Context, form auth.NewWikiForm) {
	ctx.Data["Title"] = ctx.Tr("repo.wiki.new_page")
	ctx.Data["PageIsWiki"] = true
	ctx.Data["RequireSimpleMDE"] = true

	if ctx.HasError() {
		ctx.HTML(200, tplWikiNew)
		return
	}

	if util.IsEmptyString(form.Title) {
		ctx.RenderWithErr(ctx.Tr("repo.issues.new.title_empty"), tplWikiNew, form)
		return
	}

	wikiName := models.NormalizeWikiName(form.Title)
	if err := ctx.Repo.Repository.AddWikiPage(ctx.User, wikiName, form.Content, form.Message); err != nil {
		if models.IsErrWikiReservedName(err) {
			ctx.Data["Err_Title"] = true
			ctx.RenderWithErr(ctx.Tr("repo.wiki.reserved_page", wikiName), tplWikiNew, &form)
		} else if models.IsErrWikiAlreadyExist(err) {
			ctx.Data["Err_Title"] = true
			ctx.RenderWithErr(ctx.Tr("repo.wiki.page_already_exists"), tplWikiNew, &form)
		} else {
			ctx.ServerError("AddWikiPage", err)
		}
		return
	}

	ctx.Redirect(ctx.Repo.RepoLink + "/wiki/" + models.WikiNameToSubURL(wikiName))
}

// EditWiki render wiki modify page
func EditWiki(ctx *context.Context) {
	ctx.Data["PageIsWiki"] = true
	ctx.Data["PageIsWikiEdit"] = true
	ctx.Data["RequireSimpleMDE"] = true

	if !ctx.Repo.Repository.HasWiki() {
		ctx.Redirect(ctx.Repo.RepoLink + "/wiki")
		return
	}

	renderEditPage(ctx)
	if ctx.Written() {
		return
	}

	ctx.HTML(200, tplWikiNew)
}

// EditWikiPost response for wiki modify request
func EditWikiPost(ctx *context.Context, form auth.NewWikiForm) {
	ctx.Data["Title"] = ctx.Tr("repo.wiki.new_page")
	ctx.Data["PageIsWiki"] = true
	ctx.Data["RequireSimpleMDE"] = true

	if ctx.HasError() {
		ctx.HTML(200, tplWikiNew)
		return
	}

	oldWikiName := models.NormalizeWikiName(ctx.Params(":page"))
	newWikiName := models.NormalizeWikiName(form.Title)

	if err := ctx.Repo.Repository.EditWikiPage(ctx.User, oldWikiName, newWikiName, form.Content, form.Message); err != nil {
		ctx.ServerError("EditWikiPage", err)
		return
	}

	ctx.Redirect(ctx.Repo.RepoLink + "/wiki/" + models.WikiNameToSubURL(newWikiName))
}

// DeleteWikiPagePost delete wiki page
func DeleteWikiPagePost(ctx *context.Context) {
	wikiName := models.NormalizeWikiName(ctx.Params(":page"))
	if len(wikiName) == 0 {
		wikiName = "Home"
	}

	if err := ctx.Repo.Repository.DeleteWikiPage(ctx.User, wikiName); err != nil {
		ctx.ServerError("DeleteWikiPage", err)
		return
	}

	ctx.JSON(200, map[string]interface{}{
		"redirect": ctx.Repo.RepoLink + "/wiki/",
	})
}
