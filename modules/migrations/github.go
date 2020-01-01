// Copyright 2019 The Gitea Authors. All rights reserved.
// Copyright 2018 Jonas Franz. All rights reserved.
// Use of this source code is governed by a MIT-style
// license that can be found in the LICENSE file.

package migrations

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"code.gitea.io/gitea/modules/log"
	"code.gitea.io/gitea/modules/migrations/base"
	"code.gitea.io/gitea/modules/structs"

	"github.com/google/go-github/v24/github"
	"golang.org/x/oauth2"
)

var (
	_ base.Downloader        = &GithubDownloaderV3{}
	_ base.DownloaderFactory = &GithubDownloaderV3Factory{}
)

func init() {
	RegisterDownloaderFactory(&GithubDownloaderV3Factory{})
}

// GithubDownloaderV3Factory defines a github downloader v3 factory
type GithubDownloaderV3Factory struct {
}

// Match returns ture if the migration remote URL matched this downloader factory
func (f *GithubDownloaderV3Factory) Match(opts base.MigrateOptions) (bool, error) {
	u, err := url.Parse(opts.CloneAddr)
	if err != nil {
		return false, err
	}

	return strings.EqualFold(u.Host, "github.com") && opts.AuthUsername != "", nil
}

// New returns a Downloader related to this factory according MigrateOptions
func (f *GithubDownloaderV3Factory) New(opts base.MigrateOptions) (base.Downloader, error) {
	u, err := url.Parse(opts.CloneAddr)
	if err != nil {
		return nil, err
	}

	fields := strings.Split(u.Path, "/")
	oldOwner := fields[1]
	oldName := strings.TrimSuffix(fields[2], ".git")

	log.Trace("Create github downloader: %s/%s", oldOwner, oldName)

	return NewGithubDownloaderV3(opts.AuthUsername, opts.AuthPassword, oldOwner, oldName), nil
}

// GitServiceType returns the type of git service
func (f *GithubDownloaderV3Factory) GitServiceType() structs.GitServiceType {
	return structs.GithubService
}

// GithubDownloaderV3 implements a Downloader interface to get repository informations
// from github via APIv3
type GithubDownloaderV3 struct {
	ctx       context.Context
	client    *github.Client
	repoOwner string
	repoName  string
	userName  string
	password  string
	rate      *github.Rate
}

// NewGithubDownloaderV3 creates a github Downloader via github v3 API
func NewGithubDownloaderV3(userName, password, repoOwner, repoName string) *GithubDownloaderV3 {
	var downloader = GithubDownloaderV3{
		userName:  userName,
		password:  password,
		ctx:       context.Background(),
		repoOwner: repoOwner,
		repoName:  repoName,
	}

	var client *http.Client
	if userName != "" {
		if password == "" {
			ts := oauth2.StaticTokenSource(
				&oauth2.Token{AccessToken: userName},
			)
			client = oauth2.NewClient(downloader.ctx, ts)
		} else {
			client = &http.Client{
				Transport: &http.Transport{
					Proxy: func(req *http.Request) (*url.URL, error) {
						req.SetBasicAuth(userName, password)
						return nil, nil
					},
				},
			}
		}
	}
	downloader.client = github.NewClient(client)
	return &downloader
}

// SetContext set context
func (g *GithubDownloaderV3) SetContext(ctx context.Context) {
	g.ctx = ctx
}

func (g *GithubDownloaderV3) sleep() {
	for g.rate != nil && g.rate.Remaining <= 0 {
		timer := time.NewTimer(time.Until(g.rate.Reset.Time))
		select {
		case <-g.ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}

		rates, _, err := g.client.RateLimits(g.ctx)
		if err != nil {
			log.Error("g.client.RateLimits: %s", err)
		}

		g.rate = rates.GetCore()
	}
}

// GetRepoInfo returns a repository information
func (g *GithubDownloaderV3) GetRepoInfo() (*base.Repository, error) {
	g.sleep()
	gr, resp, err := g.client.Repositories.Get(g.ctx, g.repoOwner, g.repoName)
	if err != nil {
		return nil, err
	}
	g.rate = &resp.Rate

	// convert github repo to stand Repo
	return &base.Repository{
		Owner:       g.repoOwner,
		Name:        gr.GetName(),
		IsPrivate:   *gr.Private,
		Description: gr.GetDescription(),
		OriginalURL: gr.GetHTMLURL(),
		CloneURL:    gr.GetCloneURL(),
	}, nil
}

// GetTopics return github topics
func (g *GithubDownloaderV3) GetTopics() ([]string, error) {
	g.sleep()
	r, resp, err := g.client.Repositories.Get(g.ctx, g.repoOwner, g.repoName)
	if err != nil {
		return nil, err
	}
	g.rate = &resp.Rate
	return r.Topics, nil
}

// GetMilestones returns milestones
func (g *GithubDownloaderV3) GetMilestones() ([]*base.Milestone, error) {
	var perPage = 100
	var milestones = make([]*base.Milestone, 0, perPage)
	for i := 1; ; i++ {
		g.sleep()
		ms, resp, err := g.client.Issues.ListMilestones(g.ctx, g.repoOwner, g.repoName,
			&github.MilestoneListOptions{
				State: "all",
				ListOptions: github.ListOptions{
					Page:    i,
					PerPage: perPage,
				}})
		if err != nil {
			return nil, err
		}
		g.rate = &resp.Rate

		for _, m := range ms {
			var desc string
			if m.Description != nil {
				desc = *m.Description
			}
			var state = "open"
			if m.State != nil {
				state = *m.State
			}
			milestones = append(milestones, &base.Milestone{
				Title:       *m.Title,
				Description: desc,
				Deadline:    m.DueOn,
				State:       state,
				Created:     *m.CreatedAt,
				Updated:     m.UpdatedAt,
				Closed:      m.ClosedAt,
			})
		}
		if len(ms) < perPage {
			break
		}
	}
	return milestones, nil
}

func convertGithubLabel(label *github.Label) *base.Label {
	var desc string
	if label.Description != nil {
		desc = *label.Description
	}
	return &base.Label{
		Name:        *label.Name,
		Color:       *label.Color,
		Description: desc,
	}
}

// GetLabels returns labels
func (g *GithubDownloaderV3) GetLabels() ([]*base.Label, error) {
	var perPage = 100
	var labels = make([]*base.Label, 0, perPage)
	for i := 1; ; i++ {
		g.sleep()
		ls, resp, err := g.client.Issues.ListLabels(g.ctx, g.repoOwner, g.repoName,
			&github.ListOptions{
				Page:    i,
				PerPage: perPage,
			})
		if err != nil {
			return nil, err
		}
		g.rate = &resp.Rate

		for _, label := range ls {
			labels = append(labels, convertGithubLabel(label))
		}
		if len(ls) < perPage {
			break
		}
	}
	return labels, nil
}

func (g *GithubDownloaderV3) convertGithubRelease(rel *github.RepositoryRelease) *base.Release {
	var (
		name string
		desc string
	)
	if rel.Body != nil {
		desc = *rel.Body
	}
	if rel.Name != nil {
		name = *rel.Name
	}

	var email string
	if rel.Author.Email != nil {
		email = *rel.Author.Email
	}

	r := &base.Release{
		TagName:         *rel.TagName,
		TargetCommitish: *rel.TargetCommitish,
		Name:            name,
		Body:            desc,
		Draft:           *rel.Draft,
		Prerelease:      *rel.Prerelease,
		Created:         rel.CreatedAt.Time,
		PublisherID:     *rel.Author.ID,
		PublisherName:   *rel.Author.Login,
		PublisherEmail:  email,
		Published:       rel.PublishedAt.Time,
	}

	for _, asset := range rel.Assets {
		u, _ := url.Parse(*asset.BrowserDownloadURL)
		u.User = url.UserPassword(g.userName, g.password)
		r.Assets = append(r.Assets, base.ReleaseAsset{
			URL:           u.String(),
			Name:          *asset.Name,
			ContentType:   asset.ContentType,
			Size:          asset.Size,
			DownloadCount: asset.DownloadCount,
			Created:       asset.CreatedAt.Time,
			Updated:       asset.UpdatedAt.Time,
		})
	}
	return r
}

// GetReleases returns releases
func (g *GithubDownloaderV3) GetReleases() ([]*base.Release, error) {
	var perPage = 100
	var releases = make([]*base.Release, 0, perPage)
	for i := 1; ; i++ {
		g.sleep()
		ls, resp, err := g.client.Repositories.ListReleases(g.ctx, g.repoOwner, g.repoName,
			&github.ListOptions{
				Page:    i,
				PerPage: perPage,
			})
		if err != nil {
			return nil, err
		}
		g.rate = &resp.Rate

		for _, release := range ls {
			releases = append(releases, g.convertGithubRelease(release))
		}
		if len(ls) < perPage {
			break
		}
	}
	return releases, nil
}

func convertGithubReactions(reactions *github.Reactions) *base.Reactions {
	return &base.Reactions{
		TotalCount: *reactions.TotalCount,
		PlusOne:    *reactions.PlusOne,
		MinusOne:   *reactions.MinusOne,
		Laugh:      *reactions.Laugh,
		Confused:   *reactions.Confused,
		Heart:      *reactions.Heart,
		Hooray:     *reactions.Hooray,
	}
}

// GetIssues returns issues according start and limit
func (g *GithubDownloaderV3) GetIssues(page, perPage int) ([]*base.Issue, bool, error) {
	opt := &github.IssueListByRepoOptions{
		Sort:      "created",
		Direction: "asc",
		State:     "all",
		ListOptions: github.ListOptions{
			PerPage: perPage,
			Page:    page,
		},
	}

	var allIssues = make([]*base.Issue, 0, perPage)
	g.sleep()
	issues, resp, err := g.client.Issues.ListByRepo(g.ctx, g.repoOwner, g.repoName, opt)
	if err != nil {
		return nil, false, fmt.Errorf("error while listing repos: %v", err)
	}
	g.rate = &resp.Rate
	for _, issue := range issues {
		if issue.IsPullRequest() {
			continue
		}
		var body string
		if issue.Body != nil {
			body = *issue.Body
		}
		var milestone string
		if issue.Milestone != nil {
			milestone = *issue.Milestone.Title
		}
		var labels = make([]*base.Label, 0, len(issue.Labels))
		for _, l := range issue.Labels {
			labels = append(labels, convertGithubLabel(&l))
		}
		var reactions *base.Reactions
		if issue.Reactions != nil {
			reactions = convertGithubReactions(issue.Reactions)
		}

		var email string
		if issue.User.Email != nil {
			email = *issue.User.Email
		}
		allIssues = append(allIssues, &base.Issue{
			Title:       *issue.Title,
			Number:      int64(*issue.Number),
			PosterID:    *issue.User.ID,
			PosterName:  *issue.User.Login,
			PosterEmail: email,
			Content:     body,
			Milestone:   milestone,
			State:       *issue.State,
			Created:     *issue.CreatedAt,
			Labels:      labels,
			Reactions:   reactions,
			Closed:      issue.ClosedAt,
			IsLocked:    *issue.Locked,
		})
	}

	return allIssues, len(issues) < perPage, nil
}

// GetComments returns comments according issueNumber
func (g *GithubDownloaderV3) GetComments(issueNumber int64) ([]*base.Comment, error) {
	var allComments = make([]*base.Comment, 0, 100)
	opt := &github.IssueListCommentsOptions{
		Sort:      "created",
		Direction: "asc",
		ListOptions: github.ListOptions{
			PerPage: 100,
		},
	}
	for {
		g.sleep()
		comments, resp, err := g.client.Issues.ListComments(g.ctx, g.repoOwner, g.repoName, int(issueNumber), opt)
		if err != nil {
			return nil, fmt.Errorf("error while listing repos: %v", err)
		}
		g.rate = &resp.Rate
		for _, comment := range comments {
			var email string
			if comment.User.Email != nil {
				email = *comment.User.Email
			}
			var reactions *base.Reactions
			if comment.Reactions != nil {
				reactions = convertGithubReactions(comment.Reactions)
			}
			allComments = append(allComments, &base.Comment{
				IssueIndex:  issueNumber,
				PosterID:    *comment.User.ID,
				PosterName:  *comment.User.Login,
				PosterEmail: email,
				Content:     *comment.Body,
				Created:     *comment.CreatedAt,
				Reactions:   reactions,
			})
		}
		if resp.NextPage == 0 {
			break
		}
		opt.Page = resp.NextPage
	}
	return allComments, nil
}

// GetPullRequests returns pull requests according page and perPage
func (g *GithubDownloaderV3) GetPullRequests(page, perPage int) ([]*base.PullRequest, error) {
	opt := &github.PullRequestListOptions{
		Sort:      "created",
		Direction: "asc",
		State:     "all",
		ListOptions: github.ListOptions{
			PerPage: perPage,
			Page:    page,
		},
	}
	var allPRs = make([]*base.PullRequest, 0, perPage)
	g.sleep()
	prs, resp, err := g.client.PullRequests.List(g.ctx, g.repoOwner, g.repoName, opt)
	if err != nil {
		return nil, fmt.Errorf("error while listing repos: %v", err)
	}
	g.rate = &resp.Rate
	for _, pr := range prs {
		var body string
		if pr.Body != nil {
			body = *pr.Body
		}
		var milestone string
		if pr.Milestone != nil {
			milestone = *pr.Milestone.Title
		}
		var labels = make([]*base.Label, 0, len(pr.Labels))
		for _, l := range pr.Labels {
			labels = append(labels, convertGithubLabel(l))
		}

		// FIXME: This API missing reactions, we may need another extra request to get reactions

		var email string
		if pr.User.Email != nil {
			email = *pr.User.Email
		}
		var merged bool
		// pr.Merged is not valid, so use MergedAt to test if it's merged
		if pr.MergedAt != nil {
			merged = true
		}

		var (
			headRepoName string
			cloneURL     string
			headRef      string
			headSHA      string
		)
		if pr.Head.Repo != nil {
			if pr.Head.Repo.Name != nil {
				headRepoName = *pr.Head.Repo.Name
			}
			if pr.Head.Repo.CloneURL != nil {
				cloneURL = *pr.Head.Repo.CloneURL
			}
		}
		if pr.Head.Ref != nil {
			headRef = *pr.Head.Ref
		}
		if pr.Head.SHA != nil {
			headSHA = *pr.Head.SHA
		}
		var mergeCommitSHA string
		if pr.MergeCommitSHA != nil {
			mergeCommitSHA = *pr.MergeCommitSHA
		}

		var headUserName string
		if pr.Head.User != nil && pr.Head.User.Login != nil {
			headUserName = *pr.Head.User.Login
		}

		allPRs = append(allPRs, &base.PullRequest{
			Title:          *pr.Title,
			Number:         int64(*pr.Number),
			PosterName:     *pr.User.Login,
			PosterID:       *pr.User.ID,
			PosterEmail:    email,
			Content:        body,
			Milestone:      milestone,
			State:          *pr.State,
			Created:        *pr.CreatedAt,
			Closed:         pr.ClosedAt,
			Labels:         labels,
			Merged:         merged,
			MergeCommitSHA: mergeCommitSHA,
			MergedTime:     pr.MergedAt,
			IsLocked:       pr.ActiveLockReason != nil,
			Head: base.PullRequestBranch{
				Ref:       headRef,
				SHA:       headSHA,
				RepoName:  headRepoName,
				OwnerName: headUserName,
				CloneURL:  cloneURL,
			},
			Base: base.PullRequestBranch{
				Ref:       *pr.Base.Ref,
				SHA:       *pr.Base.SHA,
				RepoName:  *pr.Base.Repo.Name,
				OwnerName: *pr.Base.User.Login,
			},
			PatchURL: *pr.PatchURL,
		})
	}

	return allPRs, nil
}
