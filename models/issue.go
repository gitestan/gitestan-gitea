// Copyright 2014 The Gogs Authors. All rights reserved.
// Use of this source code is governed by a MIT-style
// license that can be found in the LICENSE file.

package models

import (
	"fmt"
	"path"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"code.gitea.io/gitea/modules/base"
	"code.gitea.io/gitea/modules/log"
	"code.gitea.io/gitea/modules/setting"
	"code.gitea.io/gitea/modules/structs"
	api "code.gitea.io/gitea/modules/structs"
	"code.gitea.io/gitea/modules/timeutil"
	"code.gitea.io/gitea/modules/util"

	"github.com/unknwon/com"
	"xorm.io/builder"
	"xorm.io/xorm"
)

// Issue represents an issue or pull request of repository.
type Issue struct {
	ID               int64       `xorm:"pk autoincr"`
	RepoID           int64       `xorm:"INDEX UNIQUE(repo_index)"`
	Repo             *Repository `xorm:"-"`
	Index            int64       `xorm:"UNIQUE(repo_index)"` // Index in one repository.
	PosterID         int64       `xorm:"INDEX"`
	Poster           *User       `xorm:"-"`
	OriginalAuthor   string
	OriginalAuthorID int64      `xorm:"index"`
	Title            string     `xorm:"name"`
	Content          string     `xorm:"TEXT"`
	RenderedContent  string     `xorm:"-"`
	Labels           []*Label   `xorm:"-"`
	MilestoneID      int64      `xorm:"INDEX"`
	Milestone        *Milestone `xorm:"-"`
	Priority         int
	AssigneeID       int64        `xorm:"-"`
	Assignee         *User        `xorm:"-"`
	IsClosed         bool         `xorm:"INDEX"`
	IsRead           bool         `xorm:"-"`
	IsPull           bool         `xorm:"INDEX"` // Indicates whether is a pull request or not.
	PullRequest      *PullRequest `xorm:"-"`
	NumComments      int
	Ref              string

	DeadlineUnix timeutil.TimeStamp `xorm:"INDEX"`

	CreatedUnix timeutil.TimeStamp `xorm:"INDEX created"`
	UpdatedUnix timeutil.TimeStamp `xorm:"INDEX updated"`
	ClosedUnix  timeutil.TimeStamp `xorm:"INDEX"`

	Attachments      []*Attachment `xorm:"-"`
	Comments         []*Comment    `xorm:"-"`
	Reactions        ReactionList  `xorm:"-"`
	TotalTrackedTime int64         `xorm:"-"`
	Assignees        []*User       `xorm:"-"`

	// IsLocked limits commenting abilities to users on an issue
	// with write access
	IsLocked bool `xorm:"NOT NULL DEFAULT false"`
}

var (
	issueTasksPat     *regexp.Regexp
	issueTasksDonePat *regexp.Regexp
)

const issueTasksRegexpStr = `(^\s*[-*]\s\[[\sx]\]\s.)|(\n\s*[-*]\s\[[\sx]\]\s.)`
const issueTasksDoneRegexpStr = `(^\s*[-*]\s\[[x]\]\s.)|(\n\s*[-*]\s\[[x]\]\s.)`
const issueMaxDupIndexAttempts = 3

func init() {
	issueTasksPat = regexp.MustCompile(issueTasksRegexpStr)
	issueTasksDonePat = regexp.MustCompile(issueTasksDoneRegexpStr)
}

func (issue *Issue) loadTotalTimes(e Engine) (err error) {
	opts := FindTrackedTimesOptions{IssueID: issue.ID}
	issue.TotalTrackedTime, err = opts.ToSession(e).SumInt(&TrackedTime{}, "time")
	if err != nil {
		return err
	}
	return nil
}

// IsOverdue checks if the issue is overdue
func (issue *Issue) IsOverdue() bool {
	return timeutil.TimeStampNow() >= issue.DeadlineUnix
}

// LoadRepo loads issue's repository
func (issue *Issue) LoadRepo() error {
	return issue.loadRepo(x)
}

func (issue *Issue) loadRepo(e Engine) (err error) {
	if issue.Repo == nil {
		issue.Repo, err = getRepositoryByID(e, issue.RepoID)
		if err != nil {
			return fmt.Errorf("getRepositoryByID [%d]: %v", issue.RepoID, err)
		}
	}
	return nil
}

// IsTimetrackerEnabled returns true if the repo enables timetracking
func (issue *Issue) IsTimetrackerEnabled() bool {
	return issue.isTimetrackerEnabled(x)
}

func (issue *Issue) isTimetrackerEnabled(e Engine) bool {
	if err := issue.loadRepo(e); err != nil {
		log.Error(fmt.Sprintf("loadRepo: %v", err))
		return false
	}
	return issue.Repo.IsTimetrackerEnabled()
}

// GetPullRequest returns the issue pull request
func (issue *Issue) GetPullRequest() (pr *PullRequest, err error) {
	if !issue.IsPull {
		return nil, fmt.Errorf("Issue is not a pull request")
	}

	pr, err = getPullRequestByIssueID(x, issue.ID)
	if err != nil {
		return nil, err
	}
	pr.Issue = issue
	return
}

func (issue *Issue) loadLabels(e Engine) (err error) {
	if issue.Labels == nil {
		issue.Labels, err = getLabelsByIssueID(e, issue.ID)
		if err != nil {
			return fmt.Errorf("getLabelsByIssueID [%d]: %v", issue.ID, err)
		}
	}
	return nil
}

// LoadPoster loads poster
func (issue *Issue) LoadPoster() error {
	return issue.loadPoster(x)
}

func (issue *Issue) loadPoster(e Engine) (err error) {
	if issue.Poster == nil {
		issue.Poster, err = getUserByID(e, issue.PosterID)
		if err != nil {
			issue.PosterID = -1
			issue.Poster = NewGhostUser()
			if !IsErrUserNotExist(err) {
				return fmt.Errorf("getUserByID.(poster) [%d]: %v", issue.PosterID, err)
			}
			err = nil
			return
		}
	}
	return
}

func (issue *Issue) loadPullRequest(e Engine) (err error) {
	if issue.IsPull && issue.PullRequest == nil {
		issue.PullRequest, err = getPullRequestByIssueID(e, issue.ID)
		if err != nil {
			if IsErrPullRequestNotExist(err) {
				return err
			}
			return fmt.Errorf("getPullRequestByIssueID [%d]: %v", issue.ID, err)
		}
		issue.PullRequest.Issue = issue
	}
	return nil
}

// LoadPullRequest loads pull request info
func (issue *Issue) LoadPullRequest() error {
	return issue.loadPullRequest(x)
}

func (issue *Issue) loadComments(e Engine) (err error) {
	return issue.loadCommentsByType(e, CommentTypeUnknown)
}

// LoadDiscussComments loads discuss comments
func (issue *Issue) LoadDiscussComments() error {
	return issue.loadCommentsByType(x, CommentTypeComment)
}

func (issue *Issue) loadCommentsByType(e Engine, tp CommentType) (err error) {
	if issue.Comments != nil {
		return nil
	}
	issue.Comments, err = findComments(e, FindCommentsOptions{
		IssueID: issue.ID,
		Type:    tp,
	})
	return err
}

func (issue *Issue) loadReactions(e Engine) (err error) {
	if issue.Reactions != nil {
		return nil
	}
	reactions, err := findReactions(e, FindReactionsOptions{
		IssueID: issue.ID,
	})
	if err != nil {
		return err
	}
	// Load reaction user data
	if _, err := ReactionList(reactions).loadUsers(e); err != nil {
		return err
	}

	// Cache comments to map
	comments := make(map[int64]*Comment)
	for _, comment := range issue.Comments {
		comments[comment.ID] = comment
	}
	// Add reactions either to issue or comment
	for _, react := range reactions {
		if react.CommentID == 0 {
			issue.Reactions = append(issue.Reactions, react)
		} else if comment, ok := comments[react.CommentID]; ok {
			comment.Reactions = append(comment.Reactions, react)
		}
	}
	return nil
}

func (issue *Issue) loadAttributes(e Engine) (err error) {
	if err = issue.loadRepo(e); err != nil {
		return
	}

	if err = issue.loadPoster(e); err != nil {
		return
	}

	if err = issue.loadLabels(e); err != nil {
		return
	}

	if issue.Milestone == nil && issue.MilestoneID > 0 {
		issue.Milestone, err = getMilestoneByRepoID(e, issue.RepoID, issue.MilestoneID)
		if err != nil && !IsErrMilestoneNotExist(err) {
			return fmt.Errorf("getMilestoneByRepoID [repo_id: %d, milestone_id: %d]: %v", issue.RepoID, issue.MilestoneID, err)
		}
	}

	if err = issue.loadAssignees(e); err != nil {
		return
	}

	if err = issue.loadPullRequest(e); err != nil && !IsErrPullRequestNotExist(err) {
		// It is possible pull request is not yet created.
		return err
	}

	if issue.Attachments == nil {
		issue.Attachments, err = getAttachmentsByIssueID(e, issue.ID)
		if err != nil {
			return fmt.Errorf("getAttachmentsByIssueID [%d]: %v", issue.ID, err)
		}
	}

	if err = issue.loadComments(e); err != nil {
		return err
	}

	if err = CommentList(issue.Comments).loadAttributes(e); err != nil {
		return err
	}
	if issue.isTimetrackerEnabled(e) {
		if err = issue.loadTotalTimes(e); err != nil {
			return err
		}
	}

	return issue.loadReactions(e)
}

// LoadAttributes loads the attribute of this issue.
func (issue *Issue) LoadAttributes() error {
	return issue.loadAttributes(x)
}

// GetIsRead load the `IsRead` field of the issue
func (issue *Issue) GetIsRead(userID int64) error {
	issueUser := &IssueUser{IssueID: issue.ID, UID: userID}
	if has, err := x.Get(issueUser); err != nil {
		return err
	} else if !has {
		issue.IsRead = false
		return nil
	}
	issue.IsRead = issueUser.IsRead
	return nil
}

// APIURL returns the absolute APIURL to this issue.
func (issue *Issue) APIURL() string {
	return issue.Repo.APIURL() + "/" + path.Join("issues", fmt.Sprint(issue.Index))
}

// HTMLURL returns the absolute URL to this issue.
func (issue *Issue) HTMLURL() string {
	var path string
	if issue.IsPull {
		path = "pulls"
	} else {
		path = "issues"
	}
	return fmt.Sprintf("%s/%s/%d", issue.Repo.HTMLURL(), path, issue.Index)
}

// DiffURL returns the absolute URL to this diff
func (issue *Issue) DiffURL() string {
	if issue.IsPull {
		return fmt.Sprintf("%s/pulls/%d.diff", issue.Repo.HTMLURL(), issue.Index)
	}
	return ""
}

// PatchURL returns the absolute URL to this patch
func (issue *Issue) PatchURL() string {
	if issue.IsPull {
		return fmt.Sprintf("%s/pulls/%d.patch", issue.Repo.HTMLURL(), issue.Index)
	}
	return ""
}

// State returns string representation of issue status.
func (issue *Issue) State() api.StateType {
	if issue.IsClosed {
		return api.StateClosed
	}
	return api.StateOpen
}

// APIFormat assumes some fields assigned with values:
// Required - Poster, Labels,
// Optional - Milestone, Assignee, PullRequest
func (issue *Issue) APIFormat() *api.Issue {
	return issue.apiFormat(x)
}

func (issue *Issue) apiFormat(e Engine) *api.Issue {
	issue.loadLabels(e)
	apiLabels := make([]*api.Label, len(issue.Labels))
	for i := range issue.Labels {
		apiLabels[i] = issue.Labels[i].APIFormat()
	}

	issue.loadPoster(e)
	issue.loadRepo(e)
	apiIssue := &api.Issue{
		ID:       issue.ID,
		URL:      issue.APIURL(),
		Index:    issue.Index,
		Poster:   issue.Poster.APIFormat(),
		Title:    issue.Title,
		Body:     issue.Content,
		Labels:   apiLabels,
		State:    issue.State(),
		Comments: issue.NumComments,
		Created:  issue.CreatedUnix.AsTime(),
		Updated:  issue.UpdatedUnix.AsTime(),
	}

	apiIssue.Repo = &api.RepositoryMeta{
		ID:       issue.Repo.ID,
		Name:     issue.Repo.Name,
		FullName: issue.Repo.FullName(),
	}

	if issue.ClosedUnix != 0 {
		apiIssue.Closed = issue.ClosedUnix.AsTimePtr()
	}

	if issue.Milestone != nil {
		apiIssue.Milestone = issue.Milestone.APIFormat()
	}
	issue.loadAssignees(e)

	if len(issue.Assignees) > 0 {
		for _, assignee := range issue.Assignees {
			apiIssue.Assignees = append(apiIssue.Assignees, assignee.APIFormat())
		}
		apiIssue.Assignee = issue.Assignees[0].APIFormat() // For compatibility, we're keeping the first assignee as `apiIssue.Assignee`
	}
	if issue.IsPull {
		issue.loadPullRequest(e)
		apiIssue.PullRequest = &api.PullRequestMeta{
			HasMerged: issue.PullRequest.HasMerged,
		}
		if issue.PullRequest.HasMerged {
			apiIssue.PullRequest.Merged = issue.PullRequest.MergedUnix.AsTimePtr()
		}
	}
	if issue.DeadlineUnix != 0 {
		apiIssue.Deadline = issue.DeadlineUnix.AsTimePtr()
	}

	return apiIssue
}

// HashTag returns unique hash tag for issue.
func (issue *Issue) HashTag() string {
	return "issue-" + com.ToStr(issue.ID)
}

// IsPoster returns true if given user by ID is the poster.
func (issue *Issue) IsPoster(uid int64) bool {
	return issue.PosterID == uid
}

func (issue *Issue) hasLabel(e Engine, labelID int64) bool {
	return hasIssueLabel(e, issue.ID, labelID)
}

// HasLabel returns true if issue has been labeled by given ID.
func (issue *Issue) HasLabel(labelID int64) bool {
	return issue.hasLabel(x, labelID)
}

// ReplyReference returns tokenized address to use for email reply headers
func (issue *Issue) ReplyReference() string {
	var path string
	if issue.IsPull {
		path = "pulls"
	} else {
		path = "issues"
	}

	return fmt.Sprintf("%s/%s/%d@%s", issue.Repo.FullName(), path, issue.Index, setting.Domain)
}

func (issue *Issue) addLabel(e *xorm.Session, label *Label, doer *User) error {
	return newIssueLabel(e, issue, label, doer)
}

func (issue *Issue) addLabels(e *xorm.Session, labels []*Label, doer *User) error {
	return newIssueLabels(e, issue, labels, doer)
}

func (issue *Issue) getLabels(e Engine) (err error) {
	if len(issue.Labels) > 0 {
		return nil
	}

	issue.Labels, err = getLabelsByIssueID(e, issue.ID)
	if err != nil {
		return fmt.Errorf("getLabelsByIssueID: %v", err)
	}
	return nil
}

func (issue *Issue) removeLabel(e *xorm.Session, doer *User, label *Label) error {
	return deleteIssueLabel(e, issue, label, doer)
}

func (issue *Issue) clearLabels(e *xorm.Session, doer *User) (err error) {
	if err = issue.getLabels(e); err != nil {
		return fmt.Errorf("getLabels: %v", err)
	}

	for i := range issue.Labels {
		if err = issue.removeLabel(e, doer, issue.Labels[i]); err != nil {
			return fmt.Errorf("removeLabel: %v", err)
		}
	}

	return nil
}

// ClearLabels removes all issue labels as the given user.
// Triggers appropriate WebHooks, if any.
func (issue *Issue) ClearLabels(doer *User) (err error) {
	sess := x.NewSession()
	defer sess.Close()
	if err = sess.Begin(); err != nil {
		return err
	}

	if err := issue.loadRepo(sess); err != nil {
		return err
	} else if err = issue.loadPullRequest(sess); err != nil {
		return err
	}

	perm, err := getUserRepoPermission(sess, issue.Repo, doer)
	if err != nil {
		return err
	}
	if !perm.CanWriteIssuesOrPulls(issue.IsPull) {
		return ErrLabelNotExist{}
	}

	if err = issue.clearLabels(sess, doer); err != nil {
		return err
	}

	if err = sess.Commit(); err != nil {
		return fmt.Errorf("Commit: %v", err)
	}

	return nil
}

type labelSorter []*Label

func (ts labelSorter) Len() int {
	return len([]*Label(ts))
}

func (ts labelSorter) Less(i, j int) bool {
	return []*Label(ts)[i].ID < []*Label(ts)[j].ID
}

func (ts labelSorter) Swap(i, j int) {
	[]*Label(ts)[i], []*Label(ts)[j] = []*Label(ts)[j], []*Label(ts)[i]
}

// ReplaceLabels removes all current labels and add new labels to the issue.
// Triggers appropriate WebHooks, if any.
func (issue *Issue) ReplaceLabels(labels []*Label, doer *User) (err error) {
	sess := x.NewSession()
	defer sess.Close()
	if err = sess.Begin(); err != nil {
		return err
	}

	if err = issue.loadLabels(sess); err != nil {
		return err
	}

	sort.Sort(labelSorter(labels))
	sort.Sort(labelSorter(issue.Labels))

	var toAdd, toRemove []*Label

	addIndex, removeIndex := 0, 0
	for addIndex < len(labels) && removeIndex < len(issue.Labels) {
		addLabel := labels[addIndex]
		removeLabel := issue.Labels[removeIndex]
		if addLabel.ID == removeLabel.ID {
			addIndex++
			removeIndex++
		} else if addLabel.ID < removeLabel.ID {
			toAdd = append(toAdd, addLabel)
			addIndex++
		} else {
			toRemove = append(toRemove, removeLabel)
			removeIndex++
		}
	}
	toAdd = append(toAdd, labels[addIndex:]...)
	toRemove = append(toRemove, issue.Labels[removeIndex:]...)

	if len(toAdd) > 0 {
		if err = issue.addLabels(sess, toAdd, doer); err != nil {
			return fmt.Errorf("addLabels: %v", err)
		}
	}

	for _, l := range toRemove {
		if err = issue.removeLabel(sess, doer, l); err != nil {
			return fmt.Errorf("removeLabel: %v", err)
		}
	}

	return sess.Commit()
}

// ReadBy sets issue to be read by given user.
func (issue *Issue) ReadBy(userID int64) error {
	if err := UpdateIssueUserByRead(userID, issue.ID); err != nil {
		return err
	}

	return setNotificationStatusReadIfUnread(x, userID, issue.ID)
}

func updateIssueCols(e Engine, issue *Issue, cols ...string) error {
	if _, err := e.ID(issue.ID).Cols(cols...).Update(issue); err != nil {
		return err
	}
	return nil
}

func (issue *Issue) changeStatus(e *xorm.Session, doer *User, isClosed bool) (*Comment, error) {
	// Reload the issue
	currentIssue, err := getIssueByID(e, issue.ID)
	if err != nil {
		return nil, err
	}

	// Nothing should be performed if current status is same as target status
	if currentIssue.IsClosed == isClosed {
		if !issue.IsPull {
			return nil, ErrIssueWasClosed{
				ID: issue.ID,
			}
		}
		return nil, ErrPullWasClosed{
			ID: issue.ID,
		}
	}

	// Check for open dependencies
	if isClosed && issue.Repo.isDependenciesEnabled(e) {
		// only check if dependencies are enabled and we're about to close an issue, otherwise reopening an issue would fail when there are unsatisfied dependencies
		noDeps, err := issueNoDependenciesLeft(e, issue)
		if err != nil {
			return nil, err
		}

		if !noDeps {
			return nil, ErrDependenciesLeft{issue.ID}
		}
	}

	issue.IsClosed = isClosed
	if isClosed {
		issue.ClosedUnix = timeutil.TimeStampNow()
	} else {
		issue.ClosedUnix = 0
	}

	if err = updateIssueCols(e, issue, "is_closed", "closed_unix"); err != nil {
		return nil, err
	}

	// Update issue count of labels
	if err = issue.getLabels(e); err != nil {
		return nil, err
	}
	for idx := range issue.Labels {
		if err = updateLabel(e, issue.Labels[idx]); err != nil {
			return nil, err
		}
	}

	// Update issue count of milestone
	if err := updateMilestoneClosedNum(e, issue.MilestoneID); err != nil {
		return nil, err
	}

	// New action comment
	cmtType := CommentTypeClose
	if !issue.IsClosed {
		cmtType = CommentTypeReopen
	}

	return createComment(e, &CreateCommentOptions{
		Type:  cmtType,
		Doer:  doer,
		Repo:  issue.Repo,
		Issue: issue,
	})
}

// ChangeStatus changes issue status to open or closed.
func (issue *Issue) ChangeStatus(doer *User, isClosed bool) (*Comment, error) {
	sess := x.NewSession()
	defer sess.Close()
	if err := sess.Begin(); err != nil {
		return nil, err
	}

	if err := issue.loadRepo(sess); err != nil {
		return nil, err
	}
	if err := issue.loadPoster(sess); err != nil {
		return nil, err
	}

	comment, err := issue.changeStatus(sess, doer, isClosed)
	if err != nil {
		return nil, err
	}

	if err = sess.Commit(); err != nil {
		return nil, fmt.Errorf("Commit: %v", err)
	}

	return comment, nil
}

// ChangeTitle changes the title of this issue, as the given user.
func (issue *Issue) ChangeTitle(doer *User, oldTitle string) (err error) {
	sess := x.NewSession()
	defer sess.Close()

	if err = sess.Begin(); err != nil {
		return err
	}

	if err = updateIssueCols(sess, issue, "name"); err != nil {
		return fmt.Errorf("updateIssueCols: %v", err)
	}

	if err = issue.loadRepo(sess); err != nil {
		return fmt.Errorf("loadRepo: %v", err)
	}

	var opts = &CreateCommentOptions{
		Type:     CommentTypeChangeTitle,
		Doer:     doer,
		Repo:     issue.Repo,
		Issue:    issue,
		OldTitle: oldTitle,
		NewTitle: issue.Title,
	}
	if _, err = createComment(sess, opts); err != nil {
		return fmt.Errorf("createComment: %v", err)
	}
	if err = issue.addCrossReferences(sess, doer, true); err != nil {
		return err
	}

	return sess.Commit()
}

// AddDeletePRBranchComment adds delete branch comment for pull request issue
func AddDeletePRBranchComment(doer *User, repo *Repository, issueID int64, branchName string) error {
	issue, err := getIssueByID(x, issueID)
	if err != nil {
		return err
	}
	sess := x.NewSession()
	defer sess.Close()
	if err := sess.Begin(); err != nil {
		return err
	}
	var opts = &CreateCommentOptions{
		Type:      CommentTypeDeleteBranch,
		Doer:      doer,
		Repo:      repo,
		Issue:     issue,
		CommitSHA: branchName,
	}
	if _, err = createComment(sess, opts); err != nil {
		return err
	}

	return sess.Commit()
}

// UpdateAttachments update attachments by UUIDs for the issue
func (issue *Issue) UpdateAttachments(uuids []string) (err error) {
	sess := x.NewSession()
	defer sess.Close()
	if err = sess.Begin(); err != nil {
		return err
	}
	attachments, err := getAttachmentsByUUIDs(sess, uuids)
	if err != nil {
		return fmt.Errorf("getAttachmentsByUUIDs [uuids: %v]: %v", uuids, err)
	}
	for i := 0; i < len(attachments); i++ {
		attachments[i].IssueID = issue.ID
		if err := updateAttachment(sess, attachments[i]); err != nil {
			return fmt.Errorf("update attachment [id: %d]: %v", attachments[i].ID, err)
		}
	}
	return sess.Commit()
}

// ChangeContent changes issue content, as the given user.
func (issue *Issue) ChangeContent(doer *User, content string) (err error) {
	issue.Content = content

	sess := x.NewSession()
	defer sess.Close()
	if err = sess.Begin(); err != nil {
		return err
	}

	if err = updateIssueCols(sess, issue, "content"); err != nil {
		return fmt.Errorf("UpdateIssueCols: %v", err)
	}

	if err = issue.addCrossReferences(sess, doer, true); err != nil {
		return err
	}

	return sess.Commit()
}

// GetTasks returns the amount of tasks in the issues content
func (issue *Issue) GetTasks() int {
	return len(issueTasksPat.FindAllStringIndex(issue.Content, -1))
}

// GetTasksDone returns the amount of completed tasks in the issues content
func (issue *Issue) GetTasksDone() int {
	return len(issueTasksDonePat.FindAllStringIndex(issue.Content, -1))
}

// GetLastEventTimestamp returns the last user visible event timestamp, either the creation of this issue or the close.
func (issue *Issue) GetLastEventTimestamp() timeutil.TimeStamp {
	if issue.IsClosed {
		return issue.ClosedUnix
	}
	return issue.CreatedUnix
}

// GetLastEventLabel returns the localization label for the current issue.
func (issue *Issue) GetLastEventLabel() string {
	if issue.IsClosed {
		if issue.IsPull && issue.PullRequest.HasMerged {
			return "repo.pulls.merged_by"
		}
		return "repo.issues.closed_by"
	}
	return "repo.issues.opened_by"
}

// GetLastEventLabelFake returns the localization label for the current issue without providing a link in the username.
func (issue *Issue) GetLastEventLabelFake() string {
	if issue.IsClosed {
		if issue.IsPull && issue.PullRequest.HasMerged {
			return "repo.pulls.merged_by_fake"
		}
		return "repo.issues.closed_by_fake"
	}
	return "repo.issues.opened_by_fake"
}

// NewIssueOptions represents the options of a new issue.
type NewIssueOptions struct {
	Repo        *Repository
	Issue       *Issue
	LabelIDs    []int64
	Attachments []string // In UUID format.
	IsPull      bool
}

func newIssue(e *xorm.Session, doer *User, opts NewIssueOptions) (err error) {
	opts.Issue.Title = strings.TrimSpace(opts.Issue.Title)

	if opts.Issue.MilestoneID > 0 {
		milestone, err := getMilestoneByRepoID(e, opts.Issue.RepoID, opts.Issue.MilestoneID)
		if err != nil && !IsErrMilestoneNotExist(err) {
			return fmt.Errorf("getMilestoneByID: %v", err)
		}

		// Assume milestone is invalid and drop silently.
		opts.Issue.MilestoneID = 0
		if milestone != nil {
			opts.Issue.MilestoneID = milestone.ID
			opts.Issue.Milestone = milestone
		}
	}

	// Milestone validation should happen before insert actual object.
	if _, err := e.SetExpr("`index`", "coalesce(MAX(`index`),0)+1").
		Where("repo_id=?", opts.Issue.RepoID).
		Insert(opts.Issue); err != nil {
		return ErrNewIssueInsert{err}
	}

	inserted, err := getIssueByID(e, opts.Issue.ID)
	if err != nil {
		return err
	}

	// Patch Index with the value calculated by the database
	opts.Issue.Index = inserted.Index

	if opts.Issue.MilestoneID > 0 {
		if _, err = e.Exec("UPDATE `milestone` SET num_issues=num_issues+1 WHERE id=?", opts.Issue.MilestoneID); err != nil {
			return err
		}

		var opts = &CreateCommentOptions{
			Type:           CommentTypeMilestone,
			Doer:           doer,
			Repo:           opts.Repo,
			Issue:          opts.Issue,
			OldMilestoneID: 0,
			MilestoneID:    opts.Issue.MilestoneID,
		}
		if _, err = createComment(e, opts); err != nil {
			return err
		}
	}

	if opts.IsPull {
		_, err = e.Exec("UPDATE `repository` SET num_pulls = num_pulls + 1 WHERE id = ?", opts.Issue.RepoID)
	} else {
		_, err = e.Exec("UPDATE `repository` SET num_issues = num_issues + 1 WHERE id = ?", opts.Issue.RepoID)
	}
	if err != nil {
		return err
	}

	if len(opts.LabelIDs) > 0 {
		// During the session, SQLite3 driver cannot handle retrieve objects after update something.
		// So we have to get all needed labels first.
		labels := make([]*Label, 0, len(opts.LabelIDs))
		if err = e.In("id", opts.LabelIDs).Find(&labels); err != nil {
			return fmt.Errorf("find all labels [label_ids: %v]: %v", opts.LabelIDs, err)
		}

		if err = opts.Issue.loadPoster(e); err != nil {
			return err
		}

		for _, label := range labels {
			// Silently drop invalid labels.
			if label.RepoID != opts.Repo.ID {
				continue
			}

			if err = opts.Issue.addLabel(e, label, opts.Issue.Poster); err != nil {
				return fmt.Errorf("addLabel [id: %d]: %v", label.ID, err)
			}
		}
	}

	if err = newIssueUsers(e, opts.Repo, opts.Issue); err != nil {
		return err
	}

	if len(opts.Attachments) > 0 {
		attachments, err := getAttachmentsByUUIDs(e, opts.Attachments)
		if err != nil {
			return fmt.Errorf("getAttachmentsByUUIDs [uuids: %v]: %v", opts.Attachments, err)
		}

		for i := 0; i < len(attachments); i++ {
			attachments[i].IssueID = opts.Issue.ID
			if _, err = e.ID(attachments[i].ID).Update(attachments[i]); err != nil {
				return fmt.Errorf("update attachment [id: %d]: %v", attachments[i].ID, err)
			}
		}
	}
	if err = opts.Issue.loadAttributes(e); err != nil {
		return err
	}
	return opts.Issue.addCrossReferences(e, doer, false)
}

// NewIssue creates new issue with labels for repository.
func NewIssue(repo *Repository, issue *Issue, labelIDs []int64, uuids []string) (err error) {
	// Retry several times in case INSERT fails due to duplicate key for (repo_id, index); see #7887
	i := 0
	for {
		if err = newIssueAttempt(repo, issue, labelIDs, uuids); err == nil {
			return nil
		}
		if !IsErrNewIssueInsert(err) {
			return err
		}
		if i++; i == issueMaxDupIndexAttempts {
			break
		}
		log.Error("NewIssue: error attempting to insert the new issue; will retry. Original error: %v", err)
	}
	return fmt.Errorf("NewIssue: too many errors attempting to insert the new issue. Last error was: %v", err)
}

func newIssueAttempt(repo *Repository, issue *Issue, labelIDs []int64, uuids []string) (err error) {
	sess := x.NewSession()
	defer sess.Close()
	if err = sess.Begin(); err != nil {
		return err
	}

	if err = newIssue(sess, issue.Poster, NewIssueOptions{
		Repo:        repo,
		Issue:       issue,
		LabelIDs:    labelIDs,
		Attachments: uuids,
	}); err != nil {
		if IsErrUserDoesNotHaveAccessToRepo(err) || IsErrNewIssueInsert(err) {
			return err
		}
		return fmt.Errorf("newIssue: %v", err)
	}

	if err = sess.Commit(); err != nil {
		return fmt.Errorf("Commit: %v", err)
	}

	return nil
}

// GetIssueByIndex returns raw issue without loading attributes by index in a repository.
func GetIssueByIndex(repoID, index int64) (*Issue, error) {
	issue := &Issue{
		RepoID: repoID,
		Index:  index,
	}
	has, err := x.Get(issue)
	if err != nil {
		return nil, err
	} else if !has {
		return nil, ErrIssueNotExist{0, repoID, index}
	}
	return issue, nil
}

// GetIssueWithAttrsByIndex returns issue by index in a repository.
func GetIssueWithAttrsByIndex(repoID, index int64) (*Issue, error) {
	issue, err := GetIssueByIndex(repoID, index)
	if err != nil {
		return nil, err
	}
	return issue, issue.LoadAttributes()
}

func getIssueByID(e Engine, id int64) (*Issue, error) {
	issue := new(Issue)
	has, err := e.ID(id).Get(issue)
	if err != nil {
		return nil, err
	} else if !has {
		return nil, ErrIssueNotExist{id, 0, 0}
	}
	return issue, nil
}

// GetIssueWithAttrsByID returns an issue with attributes by given ID.
func GetIssueWithAttrsByID(id int64) (*Issue, error) {
	issue, err := getIssueByID(x, id)
	if err != nil {
		return nil, err
	}
	return issue, issue.loadAttributes(x)
}

// GetIssueByID returns an issue by given ID.
func GetIssueByID(id int64) (*Issue, error) {
	return getIssueByID(x, id)
}

func getIssuesByIDs(e Engine, issueIDs []int64) ([]*Issue, error) {
	issues := make([]*Issue, 0, 10)
	return issues, e.In("id", issueIDs).Find(&issues)
}

func getIssueIDsByRepoID(e Engine, repoID int64) ([]int64, error) {
	var ids = make([]int64, 0, 10)
	err := e.Table("issue").Where("repo_id = ?", repoID).Find(&ids)
	return ids, err
}

// GetIssueIDsByRepoID returns all issue ids by repo id
func GetIssueIDsByRepoID(repoID int64) ([]int64, error) {
	return getIssueIDsByRepoID(x, repoID)
}

// GetIssuesByIDs return issues with the given IDs.
func GetIssuesByIDs(issueIDs []int64) ([]*Issue, error) {
	return getIssuesByIDs(x, issueIDs)
}

// IssuesOptions represents options of an issue.
type IssuesOptions struct {
	RepoIDs     []int64 // include all repos if empty
	AssigneeID  int64
	PosterID    int64
	MentionedID int64
	MilestoneID int64
	Page        int
	PageSize    int
	IsClosed    util.OptionalBool
	IsPull      util.OptionalBool
	LabelIDs    []int64
	SortType    string
	IssueIDs    []int64
	// prioritize issues from this repo
	PriorityRepoID int64
}

// sortIssuesSession sort an issues-related session based on the provided
// sortType string
func sortIssuesSession(sess *xorm.Session, sortType string, priorityRepoID int64) {
	switch sortType {
	case "oldest":
		sess.Asc("issue.created_unix")
	case "recentupdate":
		sess.Desc("issue.updated_unix")
	case "leastupdate":
		sess.Asc("issue.updated_unix")
	case "mostcomment":
		sess.Desc("issue.num_comments")
	case "leastcomment":
		sess.Asc("issue.num_comments")
	case "priority":
		sess.Desc("issue.priority")
	case "nearduedate":
		// 253370764800 is 01/01/9999 @ 12:00am (UTC)
		sess.OrderBy("CASE WHEN issue.deadline_unix = 0 THEN 253370764800 ELSE issue.deadline_unix END ASC")
	case "farduedate":
		sess.Desc("issue.deadline_unix")
	case "priorityrepo":
		sess.OrderBy("CASE WHEN issue.repo_id = " + strconv.FormatInt(priorityRepoID, 10) + " THEN 1 ELSE 2 END, issue.created_unix DESC")
	default:
		sess.Desc("issue.created_unix")
	}
}

func (opts *IssuesOptions) setupSession(sess *xorm.Session) {
	if opts.Page >= 0 && opts.PageSize > 0 {
		var start int
		if opts.Page == 0 {
			start = 0
		} else {
			start = (opts.Page - 1) * opts.PageSize
		}
		sess.Limit(opts.PageSize, start)
	}

	if len(opts.IssueIDs) > 0 {
		sess.In("issue.id", opts.IssueIDs)
	}

	if len(opts.RepoIDs) > 0 {
		// In case repository IDs are provided but actually no repository has issue.
		sess.In("issue.repo_id", opts.RepoIDs)
	}

	switch opts.IsClosed {
	case util.OptionalBoolTrue:
		sess.And("issue.is_closed=?", true)
	case util.OptionalBoolFalse:
		sess.And("issue.is_closed=?", false)
	}

	if opts.AssigneeID > 0 {
		sess.Join("INNER", "issue_assignees", "issue.id = issue_assignees.issue_id").
			And("issue_assignees.assignee_id = ?", opts.AssigneeID)
	}

	if opts.PosterID > 0 {
		sess.And("issue.poster_id=?", opts.PosterID)
	}

	if opts.MentionedID > 0 {
		sess.Join("INNER", "issue_user", "issue.id = issue_user.issue_id").
			And("issue_user.is_mentioned = ?", true).
			And("issue_user.uid = ?", opts.MentionedID)
	}

	if opts.MilestoneID > 0 {
		sess.And("issue.milestone_id=?", opts.MilestoneID)
	}

	switch opts.IsPull {
	case util.OptionalBoolTrue:
		sess.And("issue.is_pull=?", true)
	case util.OptionalBoolFalse:
		sess.And("issue.is_pull=?", false)
	}

	if opts.LabelIDs != nil {
		for i, labelID := range opts.LabelIDs {
			if labelID > 0 {
				sess.Join("INNER", fmt.Sprintf("issue_label il%d", i),
					fmt.Sprintf("issue.id = il%[1]d.issue_id AND il%[1]d.label_id = %[2]d", i, labelID))
			} else {
				sess.Where("issue.id not in (select issue_id from issue_label where label_id = ?)", -labelID)
			}
		}
	}
}

// CountIssuesByRepo map from repoID to number of issues matching the options
func CountIssuesByRepo(opts *IssuesOptions) (map[int64]int64, error) {
	sess := x.NewSession()
	defer sess.Close()

	opts.setupSession(sess)

	countsSlice := make([]*struct {
		RepoID int64
		Count  int64
	}, 0, 10)
	if err := sess.GroupBy("issue.repo_id").
		Select("issue.repo_id AS repo_id, COUNT(*) AS count").
		Table("issue").
		Find(&countsSlice); err != nil {
		return nil, err
	}

	countMap := make(map[int64]int64, len(countsSlice))
	for _, c := range countsSlice {
		countMap[c.RepoID] = c.Count
	}
	return countMap, nil
}

// Issues returns a list of issues by given conditions.
func Issues(opts *IssuesOptions) ([]*Issue, error) {
	sess := x.NewSession()
	defer sess.Close()

	opts.setupSession(sess)
	sortIssuesSession(sess, opts.SortType, opts.PriorityRepoID)

	issues := make([]*Issue, 0, setting.UI.IssuePagingNum)
	if err := sess.Find(&issues); err != nil {
		return nil, fmt.Errorf("Find: %v", err)
	}
	sess.Close()

	if err := IssueList(issues).LoadAttributes(); err != nil {
		return nil, fmt.Errorf("LoadAttributes: %v", err)
	}

	return issues, nil
}

// GetParticipantsIDsByIssueID returns the IDs of all users who participated in comments of an issue,
// but skips joining with `user` for performance reasons.
// User permissions must be verified elsewhere if required.
func GetParticipantsIDsByIssueID(issueID int64) ([]int64, error) {
	userIDs := make([]int64, 0, 5)
	return userIDs, x.Table("comment").
		Cols("poster_id").
		Where("issue_id = ?", issueID).
		And("type in (?,?,?)", CommentTypeComment, CommentTypeCode, CommentTypeReview).
		Distinct("poster_id").
		Find(&userIDs)
}

// GetParticipantsByIssueID returns all users who are participated in comments of an issue.
func GetParticipantsByIssueID(issueID int64) ([]*User, error) {
	return getParticipantsByIssueID(x, issueID)
}

func getParticipantsByIssueID(e Engine, issueID int64) ([]*User, error) {
	userIDs := make([]int64, 0, 5)
	if err := e.Table("comment").Cols("poster_id").
		Where("`comment`.issue_id = ?", issueID).
		And("`comment`.type in (?,?,?)", CommentTypeComment, CommentTypeCode, CommentTypeReview).
		And("`user`.is_active = ?", true).
		And("`user`.prohibit_login = ?", false).
		Join("INNER", "`user`", "`user`.id = `comment`.poster_id").
		Distinct("poster_id").
		Find(&userIDs); err != nil {
		return nil, fmt.Errorf("get poster IDs: %v", err)
	}
	if len(userIDs) == 0 {
		return nil, nil
	}

	users := make([]*User, 0, len(userIDs))
	return users, e.In("id", userIDs).Find(&users)
}

// UpdateIssueMentions updates issue-user relations for mentioned users.
func UpdateIssueMentions(ctx DBContext, issueID int64, mentions []*User) error {
	if len(mentions) == 0 {
		return nil
	}
	ids := make([]int64, len(mentions))
	for i, u := range mentions {
		ids[i] = u.ID
	}
	if err := UpdateIssueUsersByMentions(ctx, issueID, ids); err != nil {
		return fmt.Errorf("UpdateIssueUsersByMentions: %v", err)
	}
	return nil
}

// IssueStats represents issue statistic information.
type IssueStats struct {
	OpenCount, ClosedCount int64
	YourRepositoriesCount  int64
	AssignCount            int64
	CreateCount            int64
	MentionCount           int64
}

// Filter modes.
const (
	FilterModeAll = iota
	FilterModeAssign
	FilterModeCreate
	FilterModeMention
)

func parseCountResult(results []map[string][]byte) int64 {
	if len(results) == 0 {
		return 0
	}
	for _, result := range results[0] {
		return com.StrTo(string(result)).MustInt64()
	}
	return 0
}

// IssueStatsOptions contains parameters accepted by GetIssueStats.
type IssueStatsOptions struct {
	RepoID      int64
	Labels      string
	MilestoneID int64
	AssigneeID  int64
	MentionedID int64
	PosterID    int64
	IsPull      util.OptionalBool
	IssueIDs    []int64
}

// GetIssueStats returns issue statistic information by given conditions.
func GetIssueStats(opts *IssueStatsOptions) (*IssueStats, error) {
	stats := &IssueStats{}

	countSession := func(opts *IssueStatsOptions) *xorm.Session {
		sess := x.
			Where("issue.repo_id = ?", opts.RepoID)

		if len(opts.IssueIDs) > 0 {
			sess.In("issue.id", opts.IssueIDs)
		}

		if len(opts.Labels) > 0 && opts.Labels != "0" {
			labelIDs, err := base.StringsToInt64s(strings.Split(opts.Labels, ","))
			if err != nil {
				log.Warn("Malformed Labels argument: %s", opts.Labels)
			} else {
				for i, labelID := range labelIDs {
					if labelID > 0 {
						sess.Join("INNER", fmt.Sprintf("issue_label il%d", i),
							fmt.Sprintf("issue.id = il%[1]d.issue_id AND il%[1]d.label_id = %[2]d", i, labelID))
					} else {
						sess.Where("issue.id NOT IN (SELECT issue_id FROM issue_label WHERE label_id = ?)", -labelID)
					}
				}
			}
		}

		if opts.MilestoneID > 0 {
			sess.And("issue.milestone_id = ?", opts.MilestoneID)
		}

		if opts.AssigneeID > 0 {
			sess.Join("INNER", "issue_assignees", "issue.id = issue_assignees.issue_id").
				And("issue_assignees.assignee_id = ?", opts.AssigneeID)
		}

		if opts.PosterID > 0 {
			sess.And("issue.poster_id = ?", opts.PosterID)
		}

		if opts.MentionedID > 0 {
			sess.Join("INNER", "issue_user", "issue.id = issue_user.issue_id").
				And("issue_user.uid = ?", opts.MentionedID).
				And("issue_user.is_mentioned = ?", true)
		}

		switch opts.IsPull {
		case util.OptionalBoolTrue:
			sess.And("issue.is_pull=?", true)
		case util.OptionalBoolFalse:
			sess.And("issue.is_pull=?", false)
		}

		return sess
	}

	var err error
	stats.OpenCount, err = countSession(opts).
		And("issue.is_closed = ?", false).
		Count(new(Issue))
	if err != nil {
		return stats, err
	}
	stats.ClosedCount, err = countSession(opts).
		And("issue.is_closed = ?", true).
		Count(new(Issue))
	return stats, err
}

// UserIssueStatsOptions contains parameters accepted by GetUserIssueStats.
type UserIssueStatsOptions struct {
	UserID      int64
	RepoIDs     []int64
	UserRepoIDs []int64
	FilterMode  int
	IsPull      bool
	IsClosed    bool
}

// GetUserIssueStats returns issue statistic information for dashboard by given conditions.
func GetUserIssueStats(opts UserIssueStatsOptions) (*IssueStats, error) {
	var err error
	stats := &IssueStats{}

	cond := builder.NewCond()
	cond = cond.And(builder.Eq{"issue.is_pull": opts.IsPull})
	if len(opts.RepoIDs) > 0 {
		cond = cond.And(builder.In("issue.repo_id", opts.RepoIDs))
	}

	switch opts.FilterMode {
	case FilterModeAll:
		stats.OpenCount, err = x.Where(cond).And("issue.is_closed = ?", false).
			And(builder.In("issue.repo_id", opts.UserRepoIDs)).
			Count(new(Issue))
		if err != nil {
			return nil, err
		}
		stats.ClosedCount, err = x.Where(cond).And("issue.is_closed = ?", true).
			And(builder.In("issue.repo_id", opts.UserRepoIDs)).
			Count(new(Issue))
		if err != nil {
			return nil, err
		}
	case FilterModeAssign:
		stats.OpenCount, err = x.Where(cond).And("issue.is_closed = ?", false).
			Join("INNER", "issue_assignees", "issue.id = issue_assignees.issue_id").
			And("issue_assignees.assignee_id = ?", opts.UserID).
			Count(new(Issue))
		if err != nil {
			return nil, err
		}
		stats.ClosedCount, err = x.Where(cond).And("issue.is_closed = ?", true).
			Join("INNER", "issue_assignees", "issue.id = issue_assignees.issue_id").
			And("issue_assignees.assignee_id = ?", opts.UserID).
			Count(new(Issue))
		if err != nil {
			return nil, err
		}
	case FilterModeCreate:
		stats.OpenCount, err = x.Where(cond).And("issue.is_closed = ?", false).
			And("issue.poster_id = ?", opts.UserID).
			Count(new(Issue))
		if err != nil {
			return nil, err
		}
		stats.ClosedCount, err = x.Where(cond).And("issue.is_closed = ?", true).
			And("issue.poster_id = ?", opts.UserID).
			Count(new(Issue))
		if err != nil {
			return nil, err
		}
	case FilterModeMention:
		stats.OpenCount, err = x.Where(cond).And("issue.is_closed = ?", false).
			Join("INNER", "issue_user", "issue.id = issue_user.issue_id and issue_user.is_mentioned = ?", true).
			And("issue_user.uid = ?", opts.UserID).
			Count(new(Issue))
		if err != nil {
			return nil, err
		}
		stats.ClosedCount, err = x.Where(cond).And("issue.is_closed = ?", true).
			Join("INNER", "issue_user", "issue.id = issue_user.issue_id and issue_user.is_mentioned = ?", true).
			And("issue_user.uid = ?", opts.UserID).
			Count(new(Issue))
		if err != nil {
			return nil, err
		}
	}

	cond = cond.And(builder.Eq{"issue.is_closed": opts.IsClosed})
	stats.AssignCount, err = x.Where(cond).
		Join("INNER", "issue_assignees", "issue.id = issue_assignees.issue_id").
		And("issue_assignees.assignee_id = ?", opts.UserID).
		Count(new(Issue))
	if err != nil {
		return nil, err
	}

	stats.CreateCount, err = x.Where(cond).
		And("poster_id = ?", opts.UserID).
		Count(new(Issue))
	if err != nil {
		return nil, err
	}

	stats.MentionCount, err = x.Where(cond).
		Join("INNER", "issue_user", "issue.id = issue_user.issue_id and issue_user.is_mentioned = ?", true).
		And("issue_user.uid = ?", opts.UserID).
		Count(new(Issue))
	if err != nil {
		return nil, err
	}

	stats.YourRepositoriesCount, err = x.Where(cond).
		And(builder.In("issue.repo_id", opts.UserRepoIDs)).
		Count(new(Issue))
	if err != nil {
		return nil, err
	}

	return stats, nil
}

// GetRepoIssueStats returns number of open and closed repository issues by given filter mode.
func GetRepoIssueStats(repoID, uid int64, filterMode int, isPull bool) (numOpen int64, numClosed int64) {
	countSession := func(isClosed, isPull bool, repoID int64) *xorm.Session {
		sess := x.
			Where("is_closed = ?", isClosed).
			And("is_pull = ?", isPull).
			And("repo_id = ?", repoID)

		return sess
	}

	openCountSession := countSession(false, isPull, repoID)
	closedCountSession := countSession(true, isPull, repoID)

	switch filterMode {
	case FilterModeAssign:
		openCountSession.Join("INNER", "issue_assignees", "issue.id = issue_assignees.issue_id").
			And("issue_assignees.assignee_id = ?", uid)
		closedCountSession.Join("INNER", "issue_assignees", "issue.id = issue_assignees.issue_id").
			And("issue_assignees.assignee_id = ?", uid)
	case FilterModeCreate:
		openCountSession.And("poster_id = ?", uid)
		closedCountSession.And("poster_id = ?", uid)
	}

	openResult, _ := openCountSession.Count(new(Issue))
	closedResult, _ := closedCountSession.Count(new(Issue))

	return openResult, closedResult
}

// SearchIssueIDsByKeyword search issues on database
func SearchIssueIDsByKeyword(kw string, repoIDs []int64, limit, start int) (int64, []int64, error) {
	var repoCond = builder.In("repo_id", repoIDs)
	var subQuery = builder.Select("id").From("issue").Where(repoCond)
	var cond = builder.And(
		repoCond,
		builder.Or(
			builder.Like{"name", kw},
			builder.Like{"content", kw},
			builder.In("id", builder.Select("issue_id").
				From("comment").
				Where(builder.And(
					builder.Eq{"type": CommentTypeComment},
					builder.In("issue_id", subQuery),
					builder.Like{"content", kw},
				)),
			),
		),
	)

	var ids = make([]int64, 0, limit)
	err := x.Distinct("id").Table("issue").Where(cond).Limit(limit, start).Find(&ids)
	if err != nil {
		return 0, nil, err
	}

	total, err := x.Distinct("id").Table("issue").Where(cond).Count()
	if err != nil {
		return 0, nil, err
	}

	return total, ids, nil
}

func updateIssue(e Engine, issue *Issue) error {
	_, err := e.ID(issue.ID).AllCols().Update(issue)
	if err != nil {
		return err
	}
	return nil
}

// UpdateIssue updates all fields of given issue.
func UpdateIssue(issue *Issue) error {
	sess := x.NewSession()
	defer sess.Close()
	if err := sess.Begin(); err != nil {
		return err
	}
	if err := updateIssue(sess, issue); err != nil {
		return err
	}
	if err := issue.loadPoster(sess); err != nil {
		return err
	}
	if err := issue.addCrossReferences(sess, issue.Poster, true); err != nil {
		return err
	}
	return sess.Commit()
}

// UpdateIssueDeadline updates an issue deadline and adds comments. Setting a deadline to 0 means deleting it.
func UpdateIssueDeadline(issue *Issue, deadlineUnix timeutil.TimeStamp, doer *User) (err error) {

	// if the deadline hasn't changed do nothing
	if issue.DeadlineUnix == deadlineUnix {
		return nil
	}

	sess := x.NewSession()
	defer sess.Close()
	if err := sess.Begin(); err != nil {
		return err
	}

	// Update the deadline
	if err = updateIssueCols(sess, &Issue{ID: issue.ID, DeadlineUnix: deadlineUnix}, "deadline_unix"); err != nil {
		return err
	}

	// Make the comment
	if _, err = createDeadlineComment(sess, doer, issue, deadlineUnix); err != nil {
		return fmt.Errorf("createRemovedDueDateComment: %v", err)
	}

	return sess.Commit()
}

// DependencyInfo represents high level information about an issue which is a dependency of another issue.
type DependencyInfo struct {
	Issue      `xorm:"extends"`
	Repository `xorm:"extends"`
}

// Get Blocked By Dependencies, aka all issues this issue is blocked by.
func (issue *Issue) getBlockedByDependencies(e Engine) (issueDeps []*DependencyInfo, err error) {
	return issueDeps, e.
		Table("issue").
		Join("INNER", "repository", "repository.id = issue.repo_id").
		Join("INNER", "issue_dependency", "issue_dependency.dependency_id = issue.id").
		Where("issue_id = ?", issue.ID).
		//sort by repo id then created date, with the issues of the same repo at the beginning of the list
		OrderBy("CASE WHEN issue.repo_id = " + strconv.FormatInt(issue.RepoID, 10) + " THEN 0 ELSE issue.repo_id END, issue.created_unix DESC").
		Find(&issueDeps)
}

// Get Blocking Dependencies, aka all issues this issue blocks.
func (issue *Issue) getBlockingDependencies(e Engine) (issueDeps []*DependencyInfo, err error) {
	return issueDeps, e.
		Table("issue").
		Join("INNER", "repository", "repository.id = issue.repo_id").
		Join("INNER", "issue_dependency", "issue_dependency.issue_id = issue.id").
		Where("dependency_id = ?", issue.ID).
		//sort by repo id then created date, with the issues of the same repo at the beginning of the list
		OrderBy("CASE WHEN issue.repo_id = " + strconv.FormatInt(issue.RepoID, 10) + " THEN 0 ELSE issue.repo_id END, issue.created_unix DESC").
		Find(&issueDeps)
}

// BlockedByDependencies finds all Dependencies an issue is blocked by
func (issue *Issue) BlockedByDependencies() ([]*DependencyInfo, error) {
	return issue.getBlockedByDependencies(x)
}

// BlockingDependencies returns all blocking dependencies, aka all other issues a given issue blocks
func (issue *Issue) BlockingDependencies() ([]*DependencyInfo, error) {
	return issue.getBlockingDependencies(x)
}

func (issue *Issue) updateClosedNum(e Engine) (err error) {
	if issue.IsPull {
		_, err = e.Exec("UPDATE `repository` SET num_closed_pulls=(SELECT count(*) FROM issue WHERE repo_id=? AND is_pull=? AND is_closed=?) WHERE id=?",
			issue.RepoID,
			true,
			true,
			issue.RepoID,
		)
	} else {
		_, err = e.Exec("UPDATE `repository` SET num_closed_issues=(SELECT count(*) FROM issue WHERE repo_id=? AND is_pull=? AND is_closed=?) WHERE id=?",
			issue.RepoID,
			false,
			true,
			issue.RepoID,
		)
	}
	return
}

// ResolveMentionsByVisibility returns the users mentioned in an issue, removing those that
// don't have access to reading it. Teams are expanded into their users, but organizations are ignored.
func (issue *Issue) ResolveMentionsByVisibility(ctx DBContext, doer *User, mentions []string) (users []*User, err error) {
	if len(mentions) == 0 {
		return
	}
	if err = issue.loadRepo(ctx.e); err != nil {
		return
	}
	resolved := make(map[string]bool, 20)
	names := make([]string, 0, 20)
	resolved[doer.LowerName] = true
	for _, name := range mentions {
		name := strings.ToLower(name)
		if _, ok := resolved[name]; ok {
			continue
		}
		resolved[name] = false
		names = append(names, name)
	}

	if err := issue.Repo.getOwner(ctx.e); err != nil {
		return nil, err
	}

	if issue.Repo.Owner.IsOrganization() {
		// Since there can be users with names that match the name of a team,
		// if the team exists and can read the issue, the team takes precedence.
		teams := make([]*Team, 0, len(names))
		if err := ctx.e.
			Join("INNER", "team_repo", "team_repo.team_id = team.id").
			Where("team_repo.repo_id=?", issue.Repo.ID).
			In("team.lower_name", names).
			Find(&teams); err != nil {
			return nil, fmt.Errorf("find mentioned teams: %v", err)
		}
		if len(teams) != 0 {
			checked := make([]int64, 0, len(teams))
			unittype := UnitTypeIssues
			if issue.IsPull {
				unittype = UnitTypePullRequests
			}
			for _, team := range teams {
				if team.Authorize >= AccessModeOwner {
					checked = append(checked, team.ID)
					resolved[team.LowerName] = true
					continue
				}
				has, err := ctx.e.Get(&TeamUnit{OrgID: issue.Repo.Owner.ID, TeamID: team.ID, Type: unittype})
				if err != nil {
					return nil, fmt.Errorf("get team units (%d): %v", team.ID, err)
				}
				if has {
					checked = append(checked, team.ID)
					resolved[team.LowerName] = true
				}
			}
			if len(checked) != 0 {
				teamusers := make([]*User, 0, 20)
				if err := ctx.e.
					Join("INNER", "team_user", "team_user.uid = `user`.id").
					In("`team_user`.team_id", checked).
					And("`user`.is_active = ?", true).
					And("`user`.prohibit_login = ?", false).
					Find(&teamusers); err != nil {
					return nil, fmt.Errorf("get teams users: %v", err)
				}
				if len(teamusers) > 0 {
					users = make([]*User, 0, len(teamusers))
					for _, user := range teamusers {
						if already, ok := resolved[user.LowerName]; !ok || !already {
							users = append(users, user)
							resolved[user.LowerName] = true
						}
					}
				}
			}
		}

		// Remove names already in the list to avoid querying the database if pending names remain
		names = make([]string, 0, len(resolved))
		for name, already := range resolved {
			if !already {
				names = append(names, name)
			}
		}
		if len(names) == 0 {
			return
		}
	}

	unchecked := make([]*User, 0, len(names))
	if err := ctx.e.
		Where("`user`.is_active = ?", true).
		And("`user`.prohibit_login = ?", false).
		In("`user`.lower_name", names).
		Find(&unchecked); err != nil {
		return nil, fmt.Errorf("find mentioned users: %v", err)
	}
	for _, user := range unchecked {
		if already := resolved[user.LowerName]; already || user.IsOrganization() {
			continue
		}
		// Normal users must have read access to the referencing issue
		perm, err := getUserRepoPermission(ctx.e, issue.Repo, user)
		if err != nil {
			return nil, fmt.Errorf("getUserRepoPermission [%d]: %v", user.ID, err)
		}
		if !perm.CanReadIssuesOrPulls(issue.IsPull) {
			continue
		}
		users = append(users, user)
	}

	return
}

// UpdateIssuesMigrationsByType updates all migrated repositories' issues from gitServiceType to replace originalAuthorID to posterID
func UpdateIssuesMigrationsByType(gitServiceType structs.GitServiceType, originalAuthorID string, posterID int64) error {
	_, err := x.Table("issue").
		Where("repo_id IN (SELECT id FROM repository WHERE original_service_type = ?)", gitServiceType).
		And("original_author_id = ?", originalAuthorID).
		Update(map[string]interface{}{
			"poster_id":          posterID,
			"original_author":    "",
			"original_author_id": 0,
		})
	return err
}
