// Copyright 2019 The Gitea Authors. All rights reserved.
// Use of this source code is governed by a MIT-style
// license that can be found in the LICENSE file.

package repofiles

import (
	"fmt"
	"net/url"
	"strings"
	"time"

	"code.gitea.io/gitea/models"
	"code.gitea.io/gitea/modules/git"
	api "code.gitea.io/gitea/modules/structs"
)

// GetFileResponseFromCommit Constructs a FileResponse from a Commit object
func GetFileResponseFromCommit(repo *models.Repository, commit *git.Commit, branch, treeName string) (*api.FileResponse, error) {
	fileContents, _ := GetContents(repo, treeName, branch, false) // ok if fails, then will be nil
	fileCommitResponse, _ := GetFileCommitResponse(repo, commit)  // ok if fails, then will be nil
	verification := GetPayloadCommitVerification(commit)
	fileResponse := &api.FileResponse{
		Content:      fileContents,
		Commit:       fileCommitResponse,
		Verification: verification,
	}
	return fileResponse, nil
}

// GetFileCommitResponse Constructs a FileCommitResponse from a Commit object
func GetFileCommitResponse(repo *models.Repository, commit *git.Commit) (*api.FileCommitResponse, error) {
	if repo == nil {
		return nil, fmt.Errorf("repo cannot be nil")
	}
	if commit == nil {
		return nil, fmt.Errorf("commit cannot be nil")
	}
	commitURL, _ := url.Parse(repo.APIURL() + "/git/commits/" + commit.ID.String())
	commitTreeURL, _ := url.Parse(repo.APIURL() + "/git/trees/" + commit.Tree.ID.String())
	parents := make([]*api.CommitMeta, commit.ParentCount())
	for i := 0; i <= commit.ParentCount(); i++ {
		if parent, err := commit.Parent(i); err == nil && parent != nil {
			parentCommitURL, _ := url.Parse(repo.APIURL() + "/git/commits/" + parent.ID.String())
			parents[i] = &api.CommitMeta{
				SHA: parent.ID.String(),
				URL: parentCommitURL.String(),
			}
		}
	}
	commitHTMLURL, _ := url.Parse(repo.HTMLURL() + "/commit/" + commit.ID.String())
	fileCommit := &api.FileCommitResponse{
		CommitMeta: api.CommitMeta{
			SHA: commit.ID.String(),
			URL: commitURL.String(),
		},
		HTMLURL: commitHTMLURL.String(),
		Author: &api.CommitUser{
			Identity: api.Identity{
				Name:  commit.Author.Name,
				Email: commit.Author.Email,
			},
			Date: commit.Author.When.UTC().Format(time.RFC3339),
		},
		Committer: &api.CommitUser{
			Identity: api.Identity{
				Name:  commit.Committer.Name,
				Email: commit.Committer.Email,
			},
			Date: commit.Committer.When.UTC().Format(time.RFC3339),
		},
		Message: commit.Message(),
		Tree: &api.CommitMeta{
			URL: commitTreeURL.String(),
			SHA: commit.Tree.ID.String(),
		},
		Parents: parents,
	}
	return fileCommit, nil
}

// GetAuthorAndCommitterUsers Gets the author and committer user objects from the IdentityOptions
func GetAuthorAndCommitterUsers(author, committer *IdentityOptions, doer *models.User) (authorUser, committerUser *models.User) {
	// Committer and author are optional. If they are not the doer (not same email address)
	// then we use bogus User objects for them to store their FullName and Email.
	// If only one of the two are provided, we set both of them to it.
	// If neither are provided, both are the doer.
	if committer != nil && committer.Email != "" {
		if doer != nil && strings.EqualFold(doer.Email, committer.Email) {
			committerUser = doer // the committer is the doer, so will use their user object
			if committer.Name != "" {
				committerUser.FullName = committer.Name
			}
		} else {
			committerUser = &models.User{
				FullName: committer.Name,
				Email:    committer.Email,
			}
		}
	}
	if author != nil && author.Email != "" {
		if doer != nil && strings.EqualFold(doer.Email, author.Email) {
			authorUser = doer // the author is the doer, so will use their user object
			if authorUser.Name != "" {
				authorUser.FullName = author.Name
			}
		} else {
			authorUser = &models.User{
				FullName: author.Name,
				Email:    author.Email,
			}
		}
	}
	if authorUser == nil {
		if committerUser != nil {
			authorUser = committerUser // No valid author was given so use the committer
		} else if doer != nil {
			authorUser = doer // No valid author was given and no valid committer so use the doer
		}
	}
	if committerUser == nil {
		committerUser = authorUser // No valid committer so use the author as the committer (was set to a valid user above)
	}
	return authorUser, committerUser
}
